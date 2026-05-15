package pdf

import (
	"bufio"
	"encoding/hex"
	"strings"
)

// toUnicodeMap maps char codes (glyph indices) to Unicode runes.
type toUnicodeMap struct {
	glyphToRune map[uint32]rune
	runeToGlyph map[rune]uint32
	codeLen     int // bytes per char code (1 or 2)
}

func newToUnicodeMap() *toUnicodeMap {
	return &toUnicodeMap{
		glyphToRune: make(map[uint32]rune),
		runeToGlyph: make(map[rune]uint32),
	}
}

// decode converts a byte sequence (glyph indices) to a Unicode string.
func (m *toUnicodeMap) decode(raw []byte) string {
	if m == nil || len(raw) == 0 {
		return ""
	}
	var sb strings.Builder
	step := m.codeLen
	if step < 1 {
		step = 1
	}
	for i := 0; i+step <= len(raw); i += step {
		var code uint32
		for k := 0; k < step; k++ {
			code = code<<8 | uint32(raw[i+k])
		}
		if r, ok := m.glyphToRune[code]; ok {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// encodeRune returns the glyph bytes for a rune, if present in the font subset.
func (m *toUnicodeMap) encodeRune(r rune) ([]byte, bool) {
	if m == nil {
		return nil, false
	}
	code, ok := m.runeToGlyph[r]
	if !ok {
		return nil, false
	}
	if m.codeLen == 2 {
		return []byte{byte(code >> 8), byte(code)}, true
	}
	return []byte{byte(code)}, true
}

// encodeString tries to encode every rune in s using the font's glyph map.
// Returns nil if any rune is missing from the subset.
func (m *toUnicodeMap) encodeString(s string) []byte {
	if m == nil {
		return nil
	}
	var out []byte
	for _, r := range s {
		b, ok := m.encodeRune(r)
		if !ok {
			return nil
		}
		out = append(out, b...)
	}
	return out
}

// parseToUnicodeCMap parses a PDF ToUnicode CMap stream and builds the mapping.
func parseToUnicodeCMap(content []byte) *toUnicodeMap {
	m := newToUnicodeMap()
	scanner := bufio.NewScanner(strings.NewReader(string(content)))

	// Determine codeLen from codespacerange
	inCodeSpace := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasSuffix(line, "begincodespacerange") {
			inCodeSpace = true
			continue
		}
		if inCodeSpace {
			if line == "endcodespacerange" {
				break
			}
			// e.g. "<00> <FF>", "<0000> <FFFF>", or "<0000><ffff>" (no space)
			// Extract the first <hex> token regardless of spacing.
			if start := strings.Index(line, "<"); start >= 0 {
				if end := strings.Index(line[start:], ">"); end > 0 {
					hexVal := line[start+1 : start+end]
					m.codeLen = len(hexVal) / 2
					if m.codeLen < 1 {
						m.codeLen = 1
					}
				}
			}
			inCodeSpace = false
		}
	}

	// Reset and parse bfchar / bfrange sections
	scanner = bufio.NewScanner(strings.NewReader(string(content)))
	inBFChar := false
	inBFRange := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '%' {
			continue
		}

		if strings.HasSuffix(line, "beginbfchar") {
			inBFChar = true
			inBFRange = false
			continue
		}
		if line == "endbfchar" {
			inBFChar = false
			continue
		}
		if strings.HasSuffix(line, "beginbfrange") {
			inBFRange = true
			inBFChar = false
			continue
		}
		if line == "endbfrange" {
			inBFRange = false
			continue
		}

		if inBFChar {
			// Format: <charcode> <unicode>
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			code, ok1 := parseHexCode(parts[0])
			uni, ok2 := parseHexCode(parts[1])
			if ok1 && ok2 {
				r := rune(uni)
				m.glyphToRune[code] = r
				if _, exists := m.runeToGlyph[r]; !exists {
					m.runeToGlyph[r] = code
				}
			}
			continue
		}

		if inBFRange {
			// Format: <start> <end> <unicode_start>  OR  <start> <end> [<u1> <u2> ...]
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}
			start, ok1 := parseHexCode(parts[0])
			end, ok2 := parseHexCode(parts[1])
			if !ok1 || !ok2 {
				continue
			}
			if parts[2][0] == '[' {
				// Array of unicode values
				arrStr := strings.Join(parts[2:], " ")
				arrStr = strings.Trim(arrStr, "[]")
				uniParts := strings.Fields(arrStr)
				for i, code := uint32(0), start; code <= end && i < uint32(len(uniParts)); code, i = code+1, i+1 {
					uni, ok := parseHexCode(uniParts[i])
					if ok {
						r := rune(uni)
						m.glyphToRune[code] = r
						if _, exists := m.runeToGlyph[r]; !exists {
							m.runeToGlyph[r] = code
						}
					}
				}
			} else {
				// Contiguous range
				uniStart, ok := parseHexCode(parts[2])
				if !ok {
					continue
				}
				for code := start; code <= end; code++ {
					offset := code - start
					r := rune(uniStart + offset)
					m.glyphToRune[code] = r
					if _, exists := m.runeToGlyph[r]; !exists {
						m.runeToGlyph[r] = code
					}
				}
			}
		}
	}

	return m
}

// parseHexCode parses a PDF hex string like <004A> into a uint32.
func parseHexCode(s string) (uint32, bool) {
	s = strings.Trim(s, "<>")
	if len(s)%2 != 0 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) == 0 {
		return 0, false
	}
	var v uint32
	for _, x := range b {
		v = v<<8 | uint32(x)
	}
	return v, true
}
