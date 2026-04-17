package lang

// NewEnglishProfile retourne un LangProfile pré-configuré pour l'anglais.
func NewEnglishProfile() *LangProfile {
	return &LangProfile{
		Code: "en",
		StopWords: makeSet([]string{
			"the", "a", "an", "of", "in", "on", "at", "to", "for",
			"with", "by", "from", "is", "are", "was", "were", "be",
			"been", "being", "have", "has", "had", "do", "does", "did",
			"will", "would", "could", "should", "may", "might", "it",
			"its", "this", "that", "these", "those", "he", "she", "they",
			"we", "you", "i", "my", "your", "his", "her", "our", "their",
		}),
		CommonPrefixes: []string{
			"de", "von", "van", "of", "mac", "mc",
		},
		Abbreviations: makeSet([]string{
			"Mr.", "Mrs.", "Ms.", "Dr.", "Prof.", "St.", "Jr.", "Sr.",
		}),
	}
}
