package lang

// NewFrenchProfile retourne un LangProfile pré-configuré pour le français.
func NewFrenchProfile() *LangProfile {
	return &LangProfile{
		Code: "fr",
		StopWords: makeSet([]string{
			"le", "la", "les", "de", "du", "des", "un", "une",
			"en", "et", "est", "à", "au", "aux", "je", "tu",
			"il", "elle", "nous", "vous", "ils", "elles", "se",
			"sa", "son", "ses", "que", "qui", "ce", "cet", "cette",
			"ces", "mon", "ton", "on", "y", "par", "pour", "dans",
			"sur", "avec", "comme", "mais", "ou", "car", "ne",
			"pas", "plus", "très", "bien", "aussi", "même", "tout",
			"tous", "toute", "toutes",
		}),
		CommonPrefixes: []string{
			"de", "du", "d'", "le", "la", "les", "von", "van",
		},
		Abbreviations: makeSet([]string{
			"M.", "Mme.", "Mlle.", "Dr.", "Pr.", "Me.", "St.",
		}),
	}
}
