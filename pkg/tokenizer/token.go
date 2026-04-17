package tokenizer

// Token représente un mot ou symbole avec sa position dans le texte source.
// Les offsets Start et End sont en bytes (pas en runes), compatibles avec
// les opérations de slicing Go standard sur les strings UTF-8.
type Token struct {
	Text   string // forme de surface du token
	Start  int    // offset de début en bytes dans le texte original
	End    int    // offset de fin en bytes (exclusif) dans le texte original
	IsWord bool   // true = mot/nombre ; false = ponctuation, symbole, espace
}

// Tokenizer segmente un texte en tokens.
type Tokenizer interface {
	Tokenize(text string) []Token
}
