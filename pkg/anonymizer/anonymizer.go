package anonymizer

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

// Strategy définit le mode de remplacement d'une entité.
type Strategy int

const (
	TagReplace Strategy = iota // "John" → "[PERSON_1]"
	Redact                     // "John" → "████"
	Hash                       // "John" → "[PER_a1b2c3]"
	Consistent                 // même entity fuzzy → même placeholder
)

// AnonymizePass est une fonction de post-traitement appliquée après le
// remplacement principal des entités. Elle reçoit le texte original et le
// résultat courant, et retourne le texte mis à jour.
type AnonymizePass func(original string, result *Result) string

// Config configure l'anonymiseur.
type Config struct {
	Strategy        Strategy
	EntityTypes     []ner.EntityType // nil = toutes les entités
	ConsistentMap   bool
	CustomReplacers map[ner.EntityType]ReplacerFunc
	// Passes liste les passes de post-traitement appliquées dans l'ordre après
	// le remplacement des entités. nil déclenche les passes par défaut :
	// ConsistencyPass() puis SurnameCompletionPass().
	// Passer une slice vide désactive tout post-traitement.
	Passes []AnonymizePass
}

// ReplacerFunc permet un remplacement personnalisé.
// entity est l'entité détectée, index est le numéro séquentiel pour ce type.
type ReplacerFunc func(entity ner.Entity, index int) string

// Recognizer définit l'interface pour la reconnaissance d'entités.
type Recognizer interface {
	Recognize(text string) ([]ner.Entity, error)
}

// Anonymizer anonymise les entités nommées dans un texte.
type Anonymizer struct {
	recognizer Recognizer
	config     Config
}

// Result contient le résultat de l'anonymisation.
type Result struct {
	Text                  string            // texte anonymisé
	Entities              []ner.Entity      // entités détectées
	Mapping               map[string]string // "[PERSON_1]" → "Jean Dupont"
	OriginalToPlaceholder map[string]string // "Jean Dupont" → "[PERSON_1]"
	// Verification est renseigné par WithVerification ; nil sinon. En mode
	// strict, le rapport voyage dans l'erreur, pas dans un Result.
	Verification *VerificationReport
}

// New crée un nouvel Anonymizer. Si config.Passes est nil, les passes par
// défaut (ConsistencyPass + SurnameCompletionPass) sont activées.
func New(recognizer Recognizer, config Config) *Anonymizer {
	if config.Passes == nil {
		config.Passes = []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}
	}
	return &Anonymizer{
		recognizer: recognizer,
		config:     config,
	}
}

// Anonymize anonymise les entités dans le texte.
// Accepte des AnonymizeOption optionnelles, notamment WithSession pour partager
// l'état (compteurs, cache de cohérence) entre plusieurs appels.
// Detect expose la détection sous-jacente, sans rien remplacer ni toucher à
// une session. Sert aux contrôles qui doivent repasser le recognizer sur une
// sortie déjà anonymisée — notamment la vérification au niveau document, qui
// recompose le texte pour retrouver les entités coupées par la segmentation.
func (a *Anonymizer) Detect(text string) ([]ner.Entity, error) {
	return a.recognizer.Recognize(text)
}

func (a *Anonymizer) Anonymize(text string, opts ...AnonymizeOption) (*Result, error) {
	params := &anonymizeParams{}
	for _, opt := range opts {
		opt(params)
	}

	var (
		counters        map[ner.EntityType]int
		consistentCache map[string]string
	)
	if params.session != nil {
		if params.session.closed {
			return nil, ErrSessionClosed
		}
		counters = params.session.counters
		consistentCache = params.session.consistentCache
		params.nonce = params.session.Nonce()
	} else {
		counters = make(map[ner.EntityType]int)
		consistentCache = make(map[string]string)
		params.nonce = newNonce()
	}

	if text == "" {
		return &Result{
			Text:                  text,
			Entities:              nil,
			Mapping:               make(map[string]string),
			OriginalToPlaceholder: make(map[string]string),
		}, nil
	}

	// sourceText conserve l'entrée telle que reçue : la vérification doit
	// pouvoir constater qu'elle contenait déjà un placeholder, y compris quand
	// WithEscapeCollisions vient de le neutraliser.
	sourceText := text

	// Un placeholder déjà présent dans la source corromprait le round-trip :
	// Deanonymize restaurerait du texte qui n'a jamais été anonymisé.
	text, err := checkPlaceholderCollisions(text, params)
	if err != nil {
		return nil, err
	}

	entities, err := a.recognizer.Recognize(text)
	if err != nil {
		return nil, fmt.Errorf("recognition failed: %w", err)
	}

	if a.config.EntityTypes != nil {
		entities = filterByType(entities, a.config.EntityTypes)
	}

	entities = reconcileEntities(entities, params.extraEntities, text)

	result := &Result{
		Text:                  text,
		Entities:              entities,
		Mapping:               make(map[string]string),
		OriginalToPlaceholder: make(map[string]string),
	}

	// Passe 1 : assigner les remplacements dans l'ordre type-priorité DESC puis
	// start ASC, afin que [PERSON_1] corresponde à la première mention dans le texte.
	assignOrder := make([]ner.Entity, len(entities))
	copy(assignOrder, entities)
	sort.Slice(assignOrder, func(i, j int) bool {
		if typePriority(assignOrder[i].Type) != typePriority(assignOrder[j].Type) {
			return typePriority(assignOrder[i].Type) > typePriority(assignOrder[j].Type)
		}
		return assignOrder[i].Start < assignOrder[j].Start
	})

	for _, ent := range assignOrder {
		// Les secrets court-circuitent toute stratégie : ni mapping, ni cache de
		// cohérence, ni pseudonyme stable. Ils sont caviardés et perdus.
		if ner.IsSecretType(ent.Type) {
			continue
		}

		var replacement string
		if a.config.ConsistentMap {
			if cached, ok := consistentCache[normalizeForFuzzy(ent.Text)]; ok {
				replacement = cached
			}
		}
		if replacement == "" {
			counters[ent.Type]++
			replacement, err = a.replace(ent, counters[ent.Type], params)
			if err != nil {
				return nil, err
			}
			if a.config.ConsistentMap {
				// Clé clonée : normalizeForFuzzy peut renvoyer une sous-chaîne du
				// texte source (via TrimSpace), à ne pas retenir dans la session.
				consistentCache[strings.Clone(normalizeForFuzzy(ent.Text))] = replacement
			}
		}
		result.Mapping[replacement] = ent.Text
		result.OriginalToPlaceholder[ent.Text] = replacement
	}

	// Associer chaque entité à son replacement.
	entityReplacements := make([]string, len(entities))
	for i, ent := range entities {
		switch {
		case ner.IsSecretType(ent.Type):
			entityReplacements[i] = params.secretPlaceholder(ent.Type)
		case a.config.ConsistentMap:
			entityReplacements[i] = consistentCache[normalizeForFuzzy(ent.Text)]
		default:
			entityReplacements[i] = result.OriginalToPlaceholder[ent.Text]
		}
	}

	// Passe 2 : remplacer de droite à gauche (start décroissant) sans décalage cumulatif.
	// Chaque remplacement ne modifie que le texte après la position courante,
	// ce qui préserve la validité des offsets des entités précédentes.
	type indexedEntity struct {
		ent         ner.Entity
		replacement string
	}
	ordered := make([]indexedEntity, len(entities))
	for i, ent := range entities {
		ordered[i] = indexedEntity{ent, entityReplacements[i]}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].ent.Start > ordered[j].ent.Start
	})

	for _, ie := range ordered {
		repl := ie.replacement
		if repl == "" {
			continue
		}
		start, end := ie.ent.Start, ie.ent.End
		if start >= 0 && end <= len(result.Text) && result.Text[start:end] == ie.ent.Text {
			result.Text = result.Text[:start] + repl + result.Text[end:]
		}
	}

	for _, pass := range a.config.Passes {
		result.Text = pass(text, result)
	}

	// La forme de surface d'un secret ne doit pas ressortir dans le Result :
	// l'appelant la sérialiserait dans une réponse HTTP ou un log.
	if !params.exposeSecrets {
		result.Entities = redactSecretEntities(result.Entities)
	}

	if params.verify {
		patterns := params.verifyPatterns
		if patterns == nil {
			patterns = DefaultVerifyPatterns()
		}
		report := VerifyWithPatterns(sourceText, result, patterns)
		if params.strictVerify && !report.OK() {
			// Fail-closed : ni Result, ni texte partiellement anonymisé.
			// L'erreur ne porte que des offsets et des types.
			return nil, &VerificationError{Report: report}
		}
		result.Verification = report
	}

	if params.session != nil {
		// Plafond de rétention : projeter la taille finale du mapping avant
		// d'écrire, pour refuser plutôt que de dépasser (croissance bornée).
		if params.session.maxEntities > 0 {
			projected := len(params.session.Mapping)
			for k := range result.Mapping {
				if _, ok := params.session.Mapping[k]; !ok {
					projected++
				}
			}
			if projected > params.session.maxEntities {
				return nil, fmt.Errorf("%w : %d > %d", ErrSessionFull, projected, params.session.maxEntities)
			}
		}
		// strings.Clone coupe le lien avec le tableau d'octets du texte source :
		// une forme de surface est une sous-chaîne du document, la conserver telle
		// quelle retiendrait tout le document en mémoire tant que la session vit.
		for k, v := range result.Mapping {
			params.session.Mapping[k] = strings.Clone(v)
		}
		for k, v := range result.OriginalToPlaceholder {
			params.session.OriginalToPlaceholder[strings.Clone(k)] = v
		}
	}

	return result, nil
}

func (a *Anonymizer) replace(ent ner.Entity, index int, params *anonymizeParams) (string, error) {
	if fn, ok := a.config.CustomReplacers[ent.Type]; ok {
		return fn(ent, index), nil
	}

	switch a.config.Strategy {
	case Redact:
		return strings.Repeat("█", len(ent.Text)), nil
	case Hash:
		return a.hashReplacement(ent, params)
	default: // TagReplace, Consistent
		return params.tagPlaceholder(typeToLabel(ent.Type), index), nil
	}
}

// insecureHashWarning ne prévient qu'une fois par processus : l'avertissement
// doit être visible, pas noyer les logs d'un traitement de masse.
var insecureHashWarning sync.Once

func (a *Anonymizer) hashReplacement(ent ner.Entity, params *anonymizeParams) (string, error) {
	if len(params.hashKey) == 0 {
		if !params.insecureHash {
			return "", ErrHashKeyRequired
		}
		insecureHashWarning.Do(func() {
			log.Printf("anonymizer: AVERTISSEMENT — stratégie Hash sans clé (SHA-256 non salé, " +
				"cassable par dictionnaire). Définir " + HashKeyEnvVar + " en production.")
		})
		return params.hashPlaceholder(ent.Type, insecureHashEntity(ent.Text)), nil
	}
	if err := params.hashKey.Validate(); err != nil {
		return "", err
	}
	return params.hashPlaceholder(ent.Type, hashEntity(params.hashKey, params.hashScope, ent.Type, ent.Text)), nil
}

// redactSecretEntities remplace la forme de surface des entités secrètes par un
// résumé sans contenu. La slice d'origine n'est copiée que si nécessaire.
func redactSecretEntities(entities []ner.Entity) []ner.Entity {
	hasSecret := false
	for _, e := range entities {
		if ner.IsSecretType(e.Type) {
			hasSecret = true
			break
		}
	}
	if !hasSecret {
		return entities
	}

	out := make([]ner.Entity, len(entities))
	copy(out, entities)
	for i := range out {
		if ner.IsSecretType(out[i].Type) {
			out[i].Text = fmt.Sprintf("[redacted %d bytes]", len(out[i].Text))
		}
	}
	return out
}

// ConsistencyPass retourne une AnonymizePass qui remplace dans le texte anonymisé
// les occurrences résiduelles de texte d'entité connue par leur placeholder.
// Utile quand le NER n'a pas détecté toutes les occurrences d'une même entité.
func ConsistencyPass() AnonymizePass {
	return func(original string, result *Result) string {
		if len(result.Entities) == 0 {
			return result.Text
		}

		// Construire le canonical depuis les entités triées par priorité de type.
		canonicalMap := make(map[string]string)
		sortedByType := make([]ner.Entity, len(result.Entities))
		copy(sortedByType, result.Entities)
		sort.Slice(sortedByType, func(i, j int) bool {
			return typePriority(sortedByType[i].Type) > typePriority(sortedByType[j].Type)
		})
		for _, ent := range sortedByType {
			placeholder := result.OriginalToPlaceholder[ent.Text]
			// Une entité sans placeholder (secret caviardé, entité filtrée)
			// produirait une entrée vide, donc un remplacement par chaîne vide.
			if placeholder == "" {
				continue
			}
			cacheKey := normalizeForFuzzy(ent.Text)
			if _, exists := canonicalMap[cacheKey]; !exists {
				canonicalMap[cacheKey] = placeholder
			}
		}

		// Pour les entités PER multi-mots, ajouter chaque token majuscule
		// séparément afin de couvrir les occurrences du prénom ou nom seul.
		// Ne pas ajouter les tokens qui apparaissent dans plusieurs entités PER
		// (évite les conflits sur les noms partagés comme "Dupont").
		perTokenCount := make(map[string]int)
		for _, ent := range result.Entities {
			if ent.Type != ner.TypePER {
				continue
			}
			for _, tok := range strings.Fields(ent.Text) {
				runes := []rune(tok)
				if len(runes) > 0 && unicode.IsUpper(runes[0]) {
					perTokenCount[normalizeForFuzzy(tok)]++
				}
			}
		}
		for _, ent := range sortedByType {
			if ent.Type != ner.TypePER {
				continue
			}
			placeholder := result.OriginalToPlaceholder[ent.Text]
			if placeholder == "" {
				continue
			}
			tokens := strings.Fields(ent.Text)
			if len(tokens) <= 1 {
				continue
			}
			for _, tok := range tokens {
				runes := []rune(tok)
				if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
					continue
				}
				norm := normalizeForFuzzy(tok)
				if perTokenCount[norm] > 1 {
					continue
				}
				if _, exists := canonicalMap[norm]; !exists {
					canonicalMap[norm] = placeholder
				}
			}
		}

		// Convertir en slice triée : matchs longs d'abord (greedy), puis
		// lexicographique pour garantir un ordre déterministe.
		type canonicalEntry struct {
			substr      string
			placeholder string
		}
		canonical := make([]canonicalEntry, 0, len(canonicalMap))
		for substr, placeholder := range canonicalMap {
			canonical = append(canonical, canonicalEntry{substr, placeholder})
		}
		sort.Slice(canonical, func(i, j int) bool {
			if len(canonical[i].substr) != len(canonical[j].substr) {
				return len(canonical[i].substr) > len(canonical[j].substr)
			}
			return canonical[i].substr < canonical[j].substr
		})

		text := result.Text

		covered := make([]bool, len(text))
		for _, ent := range result.Entities {
			placeholder := result.OriginalToPlaceholder[ent.Text]
			if placeholder == "" {
				continue
			}
			pos := 0
			for {
				idx := strings.Index(text[pos:], placeholder)
				if idx < 0 {
					break
				}
				abs := pos + idx
				for i := abs; i < abs+len(placeholder) && i < len(covered); i++ {
					covered[i] = true
				}
				pos = abs + 1
			}
		}

		var repls []textReplacement
		for pos := len(text) - 1; pos >= 0; pos-- {
			if covered[pos] {
				continue
			}

			for _, entry := range canonical {
				end := pos + len(entry.substr)
				if end > len(text) {
					continue
				}
				// Vérifie qu'aucune position du span n'est déjà couverte
				// (évite les chevauchements entre remplacements collectés)
				overlapsCovered := false
				for i := pos + 1; i < end; i++ {
					if covered[i] {
						overlapsCovered = true
						break
					}
				}
				if overlapsCovered {
					continue
				}
				candidate := text[pos:end]
				if normalizeForFuzzy(candidate) != entry.substr {
					continue
				}
				if !isWordBoundary(text, pos, end) {
					continue
				}

				repls = append(repls, textReplacement{pos, end, entry.placeholder})
				for i := pos; i < end; i++ {
					covered[i] = true
				}
				break
			}
		}

		return applyReplacements(text, repls)
	}
}

// SurnameCompletionPass retourne une AnonymizePass qui, pour chaque placeholder
// PER dans le texte anonymisé, vérifie si le token suivant dans le texte original
// est un nom de famille non encore anonymisé, et le remplace par le même placeholder.
func SurnameCompletionPass() AnonymizePass {
	return func(original string, result *Result) string {
		if len(result.Entities) == 0 {
			return result.Text
		}

		text := result.Text

		covered := make([]bool, len(text))
		for _, e := range result.Entities {
			for i := e.Start; i < e.End && i < len(covered); i++ {
				covered[i] = true
			}
		}

		// Indexer les placeholders PER. La recherche se fait par occurrence du
		// placeholder plutôt que par scan des crochets ASCII : le format des
		// placeholders est configurable (cf. WithLegacyPlaceholders).
		placeholderToEntity := make(map[string]ner.Entity)
		for _, e := range result.Entities {
			if e.Type != ner.TypePER {
				continue
			}
			if placeholder := result.OriginalToPlaceholder[e.Text]; placeholder != "" {
				placeholderToEntity[placeholder] = e
			}
		}
		placeholders := make([]string, 0, len(placeholderToEntity))
		for placeholder := range placeholderToEntity {
			placeholders = append(placeholders, placeholder)
		}
		sort.Strings(placeholders) // ordre de parcours déterministe

		var repls []textReplacement
		for _, placeholder := range placeholders {
			ent := placeholderToEntity[placeholder]

			origEnd := ent.End
			for origEnd < len(original) && original[origEnd] == ' ' {
				origEnd++
			}
			if origEnd >= len(original) {
				continue
			}

			candidate := original[origEnd:scanNameToken(original, origEnd)]
			if utf8.RuneCountInString(candidate) < 2 {
				continue
			}

			for pos := 0; ; {
				idx := strings.Index(text[pos:], placeholder)
				if idx < 0 {
					break
				}
				candidateStart := pos + idx + len(placeholder)
				pos = candidateStart

				candidateEnd := candidateStart + len(candidate)
				if candidateEnd > len(text) || text[candidateStart:candidateEnd] != candidate {
					continue
				}

				alreadyCovered := false
				for i := candidateStart; i < candidateEnd && i < len(covered); i++ {
					if covered[i] {
						alreadyCovered = true
						break
					}
				}
				if alreadyCovered {
					continue
				}

				repls = append(repls, textReplacement{candidateStart, candidateEnd, placeholder})
				for i := candidateStart; i < candidateEnd; i++ {
					covered[i] = true
				}
			}
		}

		// applyReplacements attend des remplacements triés par start décroissant.
		sort.Slice(repls, func(i, j int) bool { return repls[i].start > repls[j].start })

		return applyReplacements(text, repls)
	}
}

// textReplacement accumule un remplacement avant application groupée via applyReplacements.
type textReplacement struct {
	start, end  int
	placeholder string
}

// applyReplacements applique une slice de remplacements (ordre décroissant de start)
// en une seule passe strings.Builder — O(S) au lieu de O(S × N).
func applyReplacements(text string, repls []textReplacement) string {
	if len(repls) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for i := len(repls) - 1; i >= 0; i-- {
		r := repls[i]
		b.WriteString(text[prev:r.start])
		b.WriteString(r.placeholder)
		prev = r.end
	}
	b.WriteString(text[prev:])
	return b.String()
}

// isNameRune signale les caractères autorisés à l'intérieur d'un nom propre :
// lettres, apostrophes (ASCII et typographique) et traits d'union
// (ASCII et typographiques) — même ensemble que pkg/ner et le tokenizer.
func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || r == '\'' || r == '’' ||
		r == '-' || r == '‐' || r == '‑'
}

// scanNameToken retourne l'offset de fin (exclusif) du token de type nom
// commençant à start dans text, en décodant rune par rune (UTF-8 safe).
func scanNameToken(text string, start int) int {
	end := start
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isNameRune(r) {
			break
		}
		end += size
	}
	return end
}

// isWordBoundary retourne true si le span [start, end) dans text est délimité
// par des caractères non-lettre/non-chiffre (ou par les bords du texte).
func isWordBoundary(text string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func typeToLabel(t ner.EntityType) string {
	switch t {
	case ner.TypePER:
		return "PERSON"
	case ner.TypeLOC:
		return "LOCATION"
	case ner.TypeORG:
		return "ORGANIZATION"
	case ner.TypeMISC:
		return "MISC"
	default:
		return string(t)
	}
}

func typePriority(t ner.EntityType) int {
	switch t {
	case ner.TypePER:
		return 4
	case ner.TypeLOC:
		return 3
	case ner.TypeORG:
		return 2
	case ner.TypeMISC:
		return 1
	default:
		return 0
	}
}

func normalizeForFuzzy(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func filterByType(entities []ner.Entity, types []ner.EntityType) []ner.Entity {
	typeSet := make(map[ner.EntityType]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	result := make([]ner.Entity, 0)
	for _, e := range entities {
		if typeSet[e.Type] {
			result = append(result, e)
		}
	}
	return result
}
