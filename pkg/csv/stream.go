package csv

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// DefaultBatchBytes est la taille par défaut d'un lot de Stream, en octets de
// texte de cellules : de quoi donner du contexte aux contrôles par lot sans
// que la mémoire dépende de la taille du fichier.
const DefaultBatchBytes = 64 << 10

// HeaderMode dit comment Stream traite la première ligne.
type HeaderMode int

const (
	// HeaderAuto conserve la première ligne telle quelle quand elle a l'allure
	// d'un en-tête (cf. LooksLikeHeader) ; sinon elle est traitée comme les
	// autres. Défaut.
	HeaderAuto HeaderMode = iota
	// HeaderNone traite la première ligne comme une ligne de données.
	HeaderNone
	// HeaderPresent conserve toujours la première ligne telle quelle.
	HeaderPresent
)

// Row est une ligne d'un lot de Stream. Les cellules se réécrivent en place :
// la ligne est écrite en sortie avec le lot.
type Row struct {
	// Line est le rang de la ligne dans le fichier, en-tête compris, à partir
	// de 1. Une cellule sur plusieurs lignes physiques compte pour une.
	Line  int
	Cells []string
}

// Stream lit un fichier CSV ou TSV au fil de l'eau et en écrit la version
// réécrite, lot par lot : contrairement à Walker, qui charge tout le fichier,
// la mémoire ne dépend pas de sa taille. Stream implémente
// docprocessor.StreamWalker (une cellule non vide par segment) ; WalkRows
// donne en plus la structure des lignes.
//
// Le séparateur, un BOM UTF-8 et les fins de ligne CRLF sont conservés. Le
// guillemetage peut changer : seules les cellules qui en ont besoin sont
// guillemetées en sortie.
//
// Un Stream ne se parcourt qu'une fois.
type Stream struct {
	r      *csv.Reader
	br     *bufio.Reader
	count  *countingReader
	w      *csv.Writer
	bw     *bufio.Writer
	bom    bool
	sep    rune
	crlf   bool
	batch  int
	mode   HeaderMode
	header []string
	walked bool
}

// StreamOption configure un Stream.
type StreamOption func(*Stream)

// WithSeparator impose le séparateur au lieu de le détecter.
func WithSeparator(sep rune) StreamOption {
	return func(s *Stream) { s.sep = sep }
}

// WithBatchBytes fixe la taille des lots, en octets de texte de cellules.
func WithBatchBytes(n int) StreamOption {
	return func(s *Stream) { s.batch = n }
}

// WithHeader fixe le traitement de la première ligne (défaut : HeaderAuto).
func WithHeader(mode HeaderMode) StreamOption {
	return func(s *Stream) { s.mode = mode }
}

// NewStream prépare la lecture de r et l'écriture dans w. Le séparateur est
// détecté sur le début du fichier parmi la tabulation, la virgule et le
// point-virgule, sauf WithSeparator.
func NewStream(r io.Reader, w io.Writer, opts ...StreamOption) (*Stream, error) {
	s := &Stream{batch: DefaultBatchBytes}
	for _, opt := range opts {
		opt(s)
	}
	s.count = &countingReader{r: r}
	s.br = bufio.NewReaderSize(s.count, 1<<20)
	head, err := s.br.Peek(64 << 10)
	if err != nil && err != io.EOF && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("lecture CSV : %w", err)
	}
	if bytes.HasPrefix(head, []byte(bom)) {
		s.bom = true
		s.br.Discard(len(bom))
		head = head[len(bom):]
	}
	if s.sep == 0 {
		s.sep = detectSeparator(head)
	}
	s.crlf = bytes.Contains(head, []byte("\r\n"))

	s.r = csv.NewReader(s.br)
	s.r.Comma = s.sep
	s.r.LazyQuotes = true
	s.r.FieldsPerRecord = -1
	s.bw = bufio.NewWriterSize(w, 1<<20)
	s.w = csv.NewWriter(s.bw)
	s.w.Comma = s.sep
	s.w.UseCRLF = s.crlf
	return s, nil
}

const bom = "\xef\xbb\xbf"

// Separator retourne le séparateur détecté ou imposé.
func (s *Stream) Separator() rune { return s.sep }

// Header retourne la ligne d'en-tête conservée, nil sinon. Renseigné dès le
// premier lot.
func (s *Stream) Header() []string { return s.header }

// BytesRead retourne le nombre d'octets lus dans le fichier source à la fin
// du dernier lot : de quoi suivre la progression.
func (s *Stream) BytesRead() int64 { return s.count.n - int64(s.br.Buffered()) }

// WalkBatches implémente docprocessor.StreamWalker : un segment par cellule non
// vide, dans l'ordre des lignes puis des colonnes.
func (s *Stream) WalkBatches(fn func([]docprocessor.Segment) error) error {
	var segs []docprocessor.Segment
	return s.WalkRows(func(rows []Row) error {
		segs = segs[:0]
		for _, row := range rows {
			for j := range row.Cells {
				if row.Cells[j] == "" {
					continue
				}
				cells, j := row.Cells, j
				segs = append(segs, docprocessor.Segment{
					Text:    cells[j],
					Replace: func(anonymized string) { cells[j] = anonymized },
				})
			}
		}
		return fn(segs)
	})
}

// WalkRows appelle fn pour chaque lot de lignes, puis écrit le lot. Une erreur
// de fn interrompt le parcours sans écrire le lot ; la sortie est alors
// incomplète. L'en-tête conservé est écrit sans passer par fn.
func (s *Stream) WalkRows(fn func([]Row) error) error {
	if s.walked {
		return errors.New("csv: un Stream ne se parcourt qu'une fois")
	}
	s.walked = true
	if s.bom {
		if _, err := io.WriteString(s.bw, bom); err != nil {
			return err
		}
	}

	first, err := s.read()
	if err == io.EOF {
		return s.bw.Flush()
	}
	if err != nil {
		return err
	}
	var pending [][]string
	keep := s.mode == HeaderPresent
	if s.mode == HeaderAuto {
		second, err := s.read()
		if err != nil && err != io.EOF {
			return err
		}
		keep = second != nil && LooksLikeHeader(first, second)
		if second != nil {
			pending = append(pending, second)
		}
	}
	if keep {
		s.header = first
		if err := s.w.Write(first); err != nil {
			return err
		}
	} else {
		pending = append([][]string{first}, pending...)
	}

	var rows []Row
	size := 0
	line := 1
	if keep {
		line = 2
	}
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := fn(rows); err != nil {
			return err
		}
		for _, row := range rows {
			if err := s.w.Write(row.Cells); err != nil {
				return err
			}
		}
		s.w.Flush()
		if err := s.w.Error(); err != nil {
			return err
		}
		rows, size = rows[:0], 0
		return nil
	}
	for {
		var cells []string
		if len(pending) > 0 {
			cells, pending = pending[0], pending[1:]
		} else if cells, err = s.read(); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		rows = append(rows, Row{Line: line, Cells: cells})
		line++
		for _, c := range cells {
			size += len(c) + 1
		}
		if size >= s.batch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return s.bw.Flush()
}

func (s *Stream) read() ([]string, error) {
	cells, err := s.r.Read()
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("lecture CSV : %w", err)
	}
	return cells, err
}

// LooksLikeHeader dit si first, première ligne d'un fichier, nomme ses
// colonnes : des cellules courtes, distinctes, non vides, aucune ne commençant
// par un chiffre, et au moins une colonne qui a des chiffres ou un « @ » dans
// second, la deuxième ligne, mais pas dans first — « téléphone » au-dessus de
// « 06 12 34 56 78 », « courriel » au-dessus d'une adresse.
//
// Le critère est prudent : sans une telle colonne, la première ligne est traitée
// comme une ligne de données, de sorte qu'une ligne de données ne passe pas
// pour un en-tête et ne soit conservée en clair.
func LooksLikeHeader(first, second []string) bool {
	if len(first) < 2 {
		return false
	}
	seen := make(map[string]bool, len(first))
	for _, c := range first {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] || utf8.RuneCountInString(c) > 40 || strings.ContainsAny(c[:1], "0123456789+") {
			return false
		}
		seen[c] = true
	}
	for j := range min(len(first), len(second)) {
		if strings.ContainsAny(second[j], "0123456789@") && !strings.ContainsAny(first[j], "0123456789@") {
			return true
		}
	}
	return false
}

// countingReader compte les octets lus, pour BytesRead.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Sanitize est un no-op, comme pour Walker : un CSV n'a pas de surface cachée.
func (s *Stream) Sanitize(policy docprocessor.SanitizePolicy) (docprocessor.SanitizeReport, error) {
	return docprocessor.SanitizeReport{}, nil
}
