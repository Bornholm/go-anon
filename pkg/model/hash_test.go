package model

import (
	"encoding/binary"
	"hash/fnv"
	"testing"
)

// hashFeatureLabelReference est l'implémentation d'origine via hash/fnv.
// Les clés étant sérialisées dans les modèles, l'implémentation manuelle
// (hashFeatureBase + hashFeatureLabelFromBase) doit lui rester bit-à-bit identique.
func hashFeatureLabelReference(feat string, labelIdx int) uint64 {
	h := fnv.New64a()
	h.Write([]byte(feat))
	h.Write([]byte{0xFF})
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(labelIdx))
	h.Write(b[:])
	return h.Sum64()
}

func TestHashFeatureLabel_FNVReference(t *testing.T) {
	feats := []string{
		"", "bias", "word.lower=jean", "w[+2].suf3=ont", "gaz.firstnames.rare",
		"bigram.w[-1]+w[0]=jean dupont", "wshape[-3]=Xxxx", "word.lower=éléonore",
		"cluster4=0110", "emb3lg", "word.suffix4=çois",
	}
	for _, feat := range feats {
		for _, label := range []int{0, 1, 8, 255, 256, 1 << 20} {
			want := hashFeatureLabelReference(feat, label)
			if got := hashFeatureLabel(feat, label); got != want {
				t.Errorf("hashFeatureLabel(%q, %d) = %x, référence fnv = %x", feat, label, got, want)
			}
		}
	}
}

func TestScoreAll_MatchesScore(t *testing.T) {
	feats := benchFeatures(5)
	crf := newBenchCRF(feats, 10_000)

	L := len(crf.Labels)
	out := make([]float64, L)
	for t2, f := range feats {
		crf.Weights.ScoreAll(f, out)
		for l := 0; l < L; l++ {
			if want := crf.Weights.Score(f, l); out[l] != want {
				t.Errorf("token %d label %d : ScoreAll=%v, Score=%v", t2, l, out[l], want)
			}
		}
	}
}
