package lang

// NewSpanishProfile retourne un LangProfile pré-configuré pour l'espagnol.
func NewSpanishProfile() *LangProfile {
	return &LangProfile{
		Code: "es",
		StopWords: makeSet([]string{
			"el", "la", "los", "las", "un", "una", "unos", "unas",
			"de", "del", "al", "en", "y", "e", "o", "u", "a", "ante",
			"con", "contra", "desde", "hacia", "hasta", "para", "por",
			"según", "sin", "sobre", "tras", "durante", "mediante",
			"que", "se", "su", "sus", "le", "les", "lo", "me", "te",
			"nos", "os", "yo", "tú", "él", "ella", "nosotros", "vosotros",
			"ellos", "ellas", "este", "esta", "estos", "estas",
			"ese", "esa", "esos", "esas", "aquel", "aquella",
			"es", "son", "fue", "era", "ser", "estar", "hay",
			"no", "sí", "ya", "más", "muy", "bien", "también", "todo",
		}),
		CommonPrefixes: []string{
			"de", "del", "de la", "de los", "de las", "van", "von",
		},
		Abbreviations: makeSet([]string{
			"Sr.", "Sra.", "Srta.", "Dr.", "Dra.", "Prof.", "Lic.", "Ing.",
		}),
	}
}
