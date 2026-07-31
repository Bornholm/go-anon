package model

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"math"
)

// Format v4 : en-tête gob (métadonnées) suivi des tableaux de poids en binaire
// brut, little-endian, lus et écrits en flux.
//
// Motivation — le pic mémoire de chargement. Les formats v1/v2/v3 encodent le
// modèle entier dans un unique message gob, ce qui impose deux surcoûts que le
// décodeur ne permet pas d'éviter :
//
//   - gob bufferise le message complet avant de le décoder (internal/saferio),
//     en faisant croître ce tampon par recopies successives ;
//   - gob reconstruit chaque slice par doublements (encoding/gob.growSlice)
//     au lieu de l'allouer à sa taille finale, connue pourtant dès l'en-tête.
//
// Mesuré sur le modèle fr v3 (165 Mio de poids utiles) : 1,4 Gio alloués au
// total et ~1,1 Gio de pic RSS, soit près de 7× la taille des données. Sur un
// hôte contraint, ce pic suffit à faire tuer le processus au chargement.
//
// En v4, les longueurs sont connues avant lecture : chaque tableau est alloué
// exactement une fois, puis rempli par blocs via un tampon fixe. Le pic se
// réduit à la taille finale des poids, plus quelques centaines de kio.
const (
	modelVersionStream = "4"

	// streamMagic identifie le format v4 en tête du flux décompressé. Les
	// formats antérieurs commencent par un message gob, qui ne peut pas
	// débuter par cette séquence : la détection est donc sans ambiguïté.
	streamMagic = "GOANONv4"

	// streamChunkBytes borne le tampon de conversion little-endian utilisé à
	// la lecture comme à l'écriture. Assez grand pour amortir les appels au
	// io.Reader sous-jacent, assez petit pour rester négligeable dans le pic.
	streamChunkBytes = 1 << 18 // 256 kio

	// maxStreamArrayLen borne le nombre d'éléments d'un tableau annoncé par
	// l'en-tête. Un fichier tronqué ou corrompu ne doit pas provoquer une
	// allocation démesurée avant que la lecture n'échoue.
	maxStreamArrayLen = 1 << 32
)

// streamHeader porte les métadonnées du modèle ainsi que la longueur de chaque
// tableau de poids. Les tableaux eux-mêmes suivent l'en-tête, dans l'ordre :
// WeightKeys, WeightVals, BaseKeys, BlockVals. Une longueur nulle signifie que
// le tableau est absent, ce qui distingue un modèle plat (v2) d'un modèle
// groupé (v3) une fois converti.
type streamHeader struct {
	Version      string
	Lang         string
	Labels       []string
	Transition   [][]float64
	Features     FeatureConfig
	NWeightKeys  int
	NWeightVals  int
	NBaseKeys    int
	NBlockVals   int
	SourceFormat string // format d'origine ("2" ou "3"), pour que Save le restitue
}

// SaveStream sérialise le CRF au format v4 (gzip + en-tête gob + poids bruts).
// À la différence de Save, l'écriture ne matérialise jamais l'intégralité des
// poids dans un tampon intermédiaire.
func (crf *CRF) SaveStream(w io.Writer) error {
	gz := gzip.NewWriter(w)

	if _, err := io.WriteString(gz, streamMagic); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}

	sm := crf.toSerializable()

	hdr := streamHeader{
		Version:      modelVersionStream,
		Lang:         sm.Lang,
		Labels:       sm.Labels,
		Transition:   sm.Transition,
		Features:     sm.Features,
		NWeightKeys:  len(sm.WeightKeys),
		NWeightVals:  len(sm.WeightVals),
		NBaseKeys:    len(sm.BaseKeys),
		NBlockVals:   len(sm.BlockVals),
		SourceFormat: sm.Version,
	}

	// Un modèle v1 (poids encore dans la map) doit être compacté avant d'être
	// écrit en flux : le format v4 ne transporte que les représentations
	// plates ou groupées.
	if hdr.NWeightKeys == 0 && hdr.NBaseKeys == 0 && len(sm.Weights) > 0 {
		return fmt.Errorf("save stream: modèle v1 non compacté, appeler Compact() avant SaveStream")
	}

	if err := gob.NewEncoder(gz).Encode(hdr); err != nil {
		return fmt.Errorf("encode header: %w", err)
	}

	if err := writeUint64s(gz, sm.WeightKeys); err != nil {
		return fmt.Errorf("write weight keys: %w", err)
	}
	if err := writeFloat32s(gz, sm.WeightVals); err != nil {
		return fmt.Errorf("write weight vals: %w", err)
	}
	if err := writeUint64s(gz, sm.BaseKeys); err != nil {
		return fmt.Errorf("write base keys: %w", err)
	}
	if err := writeFloat32s(gz, sm.BlockVals); err != nil {
		return fmt.Errorf("write block vals: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip stream: %w", err)
	}

	return nil
}

// loadModelStream décode un modèle v4 depuis r, positionné juste après le
// magic. compact indique le mode voulu : lecture seule (poids assignés tels
// quels) ou mutable (poids reconstruits dans une map).
func loadModelStream(r io.Reader, mutable bool) (*CRF, error) {
	dec := gob.NewDecoder(r)

	var hdr streamHeader
	if err := dec.Decode(&hdr); err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	sm := SerializableModel{
		Version:    hdr.SourceFormat,
		Lang:       hdr.Lang,
		Labels:     hdr.Labels,
		Transition: hdr.Transition,
		Features:   hdr.Features,
	}

	var err error
	if sm.WeightKeys, err = readUint64s(r, hdr.NWeightKeys); err != nil {
		return nil, fmt.Errorf("read weight keys: %w", err)
	}
	if sm.WeightVals, err = readFloat32s(r, hdr.NWeightVals); err != nil {
		return nil, fmt.Errorf("read weight vals: %w", err)
	}
	if sm.BaseKeys, err = readUint64s(r, hdr.NBaseKeys); err != nil {
		return nil, fmt.Errorf("read base keys: %w", err)
	}
	if sm.BlockVals, err = readFloat32s(r, hdr.NBlockVals); err != nil {
		return nil, fmt.Errorf("read block vals: %w", err)
	}

	if len(sm.WeightVals) != len(sm.WeightKeys) {
		return nil, fmt.Errorf("model: %d clés de poids pour %d valeurs", len(sm.WeightKeys), len(sm.WeightVals))
	}
	if L := len(sm.Labels); len(sm.BaseKeys) > 0 && len(sm.BlockVals) != len(sm.BaseKeys)*L {
		return nil, fmt.Errorf("model: %d bases pour %d poids groupés (attendu %d)",
			len(sm.BaseKeys), len(sm.BlockVals), len(sm.BaseKeys)*L)
	}

	if mutable {
		return sm.toCRFMutable(), nil
	}
	return sm.toCRF(), nil
}

// hasStreamMagic indique si br débute par le magic v4, sans le consommer.
// L'absence de magic (y compris sur un flux trop court) désigne un format
// antérieur, que l'appelant décodera en gob.
func hasStreamMagic(br *bufio.Reader) bool {
	prefix, err := br.Peek(len(streamMagic))
	if err != nil {
		return false
	}
	return string(prefix) == streamMagic
}

func writeUint64s(w io.Writer, values []uint64) error {
	buf := make([]byte, 0, streamChunkBytes)
	for _, v := range values {
		buf = binary.LittleEndian.AppendUint64(buf, v)
		if len(buf) >= streamChunkBytes {
			if _, err := w.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

func writeFloat32s(w io.Writer, values []float32) error {
	buf := make([]byte, 0, streamChunkBytes)
	for _, v := range values {
		buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(v))
		if len(buf) >= streamChunkBytes {
			if _, err := w.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// readUint64s alloue exactement n éléments puis les remplit par blocs. C'est
// le cœur du gain : une allocation unique à la taille finale, là où gob
// procédait par doublements successifs.
func readUint64s(r io.Reader, n int) ([]uint64, error) {
	if n == 0 {
		return nil, nil
	}
	if n < 0 || n > maxStreamArrayLen {
		return nil, fmt.Errorf("longueur de tableau invalide: %d", n)
	}

	out := make([]uint64, n)
	buf := make([]byte, streamChunkBytes)

	for i := 0; i < n; {
		count := min(n-i, len(buf)/8)
		chunk := buf[:count*8]
		if _, err := io.ReadFull(r, chunk); err != nil {
			return nil, err
		}
		for j := 0; j < count; j++ {
			out[i+j] = binary.LittleEndian.Uint64(chunk[j*8:])
		}
		i += count
	}

	return out, nil
}

func readFloat32s(r io.Reader, n int) ([]float32, error) {
	if n == 0 {
		return nil, nil
	}
	if n < 0 || n > maxStreamArrayLen {
		return nil, fmt.Errorf("longueur de tableau invalide: %d", n)
	}

	out := make([]float32, n)
	buf := make([]byte, streamChunkBytes)

	for i := 0; i < n; {
		count := min(n-i, len(buf)/4)
		chunk := buf[:count*4]
		if _, err := io.ReadFull(r, chunk); err != nil {
			return nil, err
		}
		for j := 0; j < count; j++ {
			out[i+j] = math.Float32frombits(binary.LittleEndian.Uint32(chunk[j*4:]))
		}
		i += count
	}

	return out, nil
}
