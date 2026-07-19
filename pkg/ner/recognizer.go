package ner

import (
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/lang"
	"github.com/bornholm/go-anon/pkg/model"
	"github.com/bornholm/go-anon/pkg/tokenizer"
)

// DefaultSentenceBoundaries est l'ensemble des tokens non-mots reconnus comme
// fins de phrase par défaut. Seules les vraies fins de phrase y figurent :
// couper aux virgules, deux-points ou parenthèses détruit le contexte des
// appositions (« Jean Dupont, directeur de Renault ») et fait chuter le F1
// (mesuré : −1,1 pt sur WikiNER fr avec l'ancienne liste étendue).
var DefaultSentenceBoundaries = []string{".", "!", "?", "…"}

// Recognizer orchestre le pipeline NER complet :
// tokenisation → extraction de features → décodage Viterbi → entités.
type Recognizer struct {
	tok                tokenizer.Tokenizer
	crf                *model.CRF
	extractor          *features.FeatureExtractor
	langProfile        *lang.LangProfile // profil linguistique (pour FirstNameDetectionPass)
	langCode           string            // code ISO 639-1 configuré via WithLanguage
	sentenceBoundaries map[string]bool   // tokens non-mots qui délimitent les phrases
	includePunctuation bool              // inclure les tokens de ponctuation dans les séquences CRF
	postFilters        []EntityFilter    // filtres appliqués après la reconnaissance
	warnings           []string          // écarts détectés entre la config du modèle et celle de l'inférence
	lastText           string            // texte de la dernière reconnaissance (pour NameCompletionPass)
}

// RecognizerOption configure un Recognizer via le pattern d'option fonctionnel.
type RecognizerOption func(*Recognizer) error

// SupportedLanguages retourne les codes ISO 639-1 des langues gérées par le
// pipeline (cf. WithLanguage). Source de vérité unique pour les appelants qui
// doivent restreindre la détection automatique de langue.
func SupportedLanguages() []string {
	return []string{"fr", "en", "es"}
}

// WithLanguage configure le tokenizer et le LangProfile selon le code ISO 639-1.
// Langues supportées : "fr", "en", "es" (cf. SupportedLanguages).
func WithLanguage(code string) RecognizerOption {
	return func(rec *Recognizer) error {
		switch code {
		case "fr":
			rec.tok = &tokenizer.UnicodeTokenizer{SplitApostrophe: true}
			rec.langProfile = lang.NewFrenchProfile()
			rec.extractor.LangProfile = rec.langProfile
		case "en":
			rec.tok = &tokenizer.UnicodeTokenizer{SplitHyphen: true}
			rec.langProfile = lang.NewEnglishProfile()
			rec.extractor.LangProfile = rec.langProfile
		case "es":
			rec.tok = &tokenizer.UnicodeTokenizer{SplitApostrophe: true}
			rec.langProfile = lang.NewSpanishProfile()
			rec.extractor.LangProfile = rec.langProfile
		default:
			return fmt.Errorf("ner: WithLanguage: unsupported language %q", code)
		}
		rec.langCode = code
		return nil
	}
}

// WithGazetteers attache des gazetteers nommés au FeatureExtractor.
func WithGazetteers(gazetteers map[string]*features.Gazetteer) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.extractor.Gazetteers = gazetteers
		return nil
	}
}

// WithSentenceBoundaries remplace la liste des tokens délimiteurs de phrases.
// Seuls les tokens non-mots (IsWord == false) dont le texte figure dans tokens
// déclenchent une coupure de séquence NER.
// Passer une liste vide désactive le découpage intra-ligne.
// Exemple : ner.WithSentenceBoundaries(".", "!", "?", ";", ":")
func WithSentenceBoundaries(tokens ...string) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.sentenceBoundaries = make(map[string]bool, len(tokens))
		for _, t := range tokens {
			rec.sentenceBoundaries[t] = true
		}
		return nil
	}
}

// WithPunctuationTokens contrôle l'inclusion des tokens de ponctuation dans
// les séquences soumises au CRF (activé par défaut). Les corpus d'entraînement
// (CoNLL, WikiNER) contiennent la ponctuation comme tokens ordinaires
// (étiquetés O) : l'inclure aussi à l'inférence aligne la distribution des
// features de contexte (bigrammes, w[±k], BOS/EOS) sur celle vue à
// l'entraînement (mesuré : +0,6 pt de F1 sur WikiNER fr, +1,7 combiné aux
// frontières de phrase réduites). Passer false restaure l'ancien comportement
// (mots seuls).
// Les offsets des entités restent inchangés (byte-précis dans le texte original).
func WithPunctuationTokens(enabled bool) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.includePunctuation = enabled
		return nil
	}
}

// WithBrownClusters attache des Brown clusters au FeatureExtractor.
func WithBrownClusters(clusters *features.BrownClusters) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.extractor.Clusters = clusters
		return nil
	}
}

// WithFirstNameReclassify ajoute un filtre qui reclasse en PER les entités LOC
// d'un seul token figurant dans le gazetteer firstNames.
// À placer avant WithMergePass pour que les prénoms reclassés soient fusionnés
// correctement avec les noms de famille adjacents.
func WithFirstNameReclassify(firstNames *features.Gazetteer) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = append(rec.postFilters, FirstNameReclassifyFilter(firstNames))
		return nil
	}
}

// WithFirstNameDetectionPass ajoute un filtre qui détecte les tokens majuscules
// correspondant à des prénoms du gazetteer qui ne sont pas déjà couverts par des
// entités existantes, et les ajoute comme entités PER.
// Les stop words proviennent du LangProfile du Recognizer.
// À placer après les autres filtres pour ne pas interférer avec la détection
// initiale du NER.
func WithFirstNameDetectionPass(firstNames *features.Gazetteer) RecognizerOption {
	return func(rec *Recognizer) error {
		var stopWords map[string]bool
		if rec.langProfile != nil {
			stopWords = make(map[string]bool)
			for w := range rec.langProfile.StopWords {
				stopWords[w] = true
			}
		}
		rec.postFilters = append(rec.postFilters, FirstNameDetectionFilter(func() string {
			return rec.lastText
		}, firstNames, stopWords))
		return nil
	}
}

// WithNameCompletionPass ajoute un filtre de complétion des noms de personnes
// après la reconnaissance NER. Ce filtre détecte les entités PER incomplètes
// (prénom seul) et les complète avec le token adjacent qui ressemble à un nom
// de famille (commençant par une majuscule, non couvert par une entité existante).
// firstNames est un gazetteer de prénoms connus ; nil rend la passe inopérante.
// À placer après WithFirstNameDetectionPass pour voir les entités détectées.
func WithNameCompletionPass(firstNames *features.Gazetteer) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = append(rec.postFilters, NameCompletionPass(func() string {
			return rec.lastText
		}, firstNames))
		return nil
	}
}

// WithMergePass ajoute un filtre de fusion des entités fragmentées après la
// reconnaissance NER. Ce filtre fusionne les entités adjacentes de même type
// (PER+PER) et corrige les faux positifs LOC sur les noms de famille (PER+LOC).
func WithMergePass() RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = append(rec.postFilters, MergePass(func() string {
			return rec.lastText
		}))
		return nil
	}
}

// New construit un Recognizer avec le modèle m et les options fournies.
// Le modèle est obligatoire ; utiliser LoadModel pour l'obtenir depuis un io.Reader.
func New(m *Model, opts ...RecognizerOption) (*Recognizer, error) {
	boundaries := make(map[string]bool, len(DefaultSentenceBoundaries))
	for _, t := range DefaultSentenceBoundaries {
		boundaries[t] = true
	}

	rec := &Recognizer{
		tok: &tokenizer.UnicodeTokenizer{SplitHyphen: true}, // défaut : "en"
		extractor: &features.FeatureExtractor{
			WindowSize: 2,
		},
		sentenceBoundaries: boundaries,
		includePunctuation: true, // aligné sur l'entraînement (cf. WithPunctuationTokens)
		crf:                m.crf,
	}

	for _, opt := range opts {
		if err := opt(rec); err != nil {
			return nil, err
		}
	}

	// Synchroniser la taille de fenêtre du modèle avec l'extracteur.
	// Le modèle sauvegarde la fenêtre utilisée à l'entraînement dans FeatureCfg.WindowSize ;
	// sans cette synchronisation, l'inférence utiliserait la valeur par défaut (2)
	// même si le modèle a été entraîné avec window=3, causant une absence de features critiques.
	if w := m.crf.FeatureCfg.WindowSize; w > 0 {
		rec.extractor.WindowSize = w
	}

	rec.warnings = configWarnings(m.crf.FeatureCfg, rec)

	return rec, nil
}

// Warnings retourne les écarts détectés entre la configuration d'entraînement
// du modèle (FeatureConfig sérialisée) et la configuration d'inférence courante.
// Chaque écart dégrade silencieusement le F1 : les features correspondantes,
// apprises à l'entraînement, ne sont jamais générées à l'inférence (ou l'inverse).
// La slice est vide si les configurations concordent.
func (r *Recognizer) Warnings() []string {
	return r.warnings
}

// configWarnings compare la FeatureConfig du modèle à la configuration effective
// du Recognizer et retourne un message par écart détecté.
func configWarnings(cfg model.FeatureConfig, rec *Recognizer) []string {
	var warnings []string

	if cfg.LangCode != "" && rec.langCode != "" && cfg.LangCode != rec.langCode {
		warnings = append(warnings, fmt.Sprintf(
			"modèle entraîné pour la langue %q mais inférence configurée en %q", cfg.LangCode, rec.langCode))
	}

	for _, name := range cfg.GazetteerNames {
		if _, ok := rec.extractor.Gazetteers[name]; !ok {
			warnings = append(warnings, fmt.Sprintf(
				"modèle entraîné avec le gazetteer %q mais celui-ci n'est pas chargé à l'inférence (WithGazetteers)", name))
		}
	}

	if cfg.HasClusters && rec.extractor.Clusters == nil {
		warnings = append(warnings,
			"modèle entraîné avec des Brown clusters mais aucun cluster chargé à l'inférence (WithBrownClusters)")
	}
	if cfg.HasEmbeddings && rec.extractor.Embeddings == nil {
		warnings = append(warnings,
			"modèle entraîné avec des word embeddings mais aucun embedding chargé à l'inférence")
	}

	return warnings
}

// Recognize détecte les entités nommées dans text.
// Le texte est découpé ligne par ligne : chaque ligne non vide est traitée
// comme une séquence NER indépendante (comme à l'entraînement), ce qui évite
// que le modèle étende des spans d'entités au-delà des frontières naturelles.
// Les offsets Start/End des entités retournées sont des positions byte dans
// le texte original (non modifié).
func (r *Recognizer) Recognize(text string) ([]Entity, error) {
	if text == "" {
		return []Entity{}, nil
	}

	r.lastText = text

	var allEntities []Entity
	_, labelIndex := r.crf.LabelsWithIndex()

	offset := 0
	for _, line := range strings.Split(text, "\n") {
		lineLen := len(line)
		entities := r.recognizeLine(line, offset, labelIndex)
		allEntities = append(allEntities, entities...)
		offset += lineLen + 1 // +1 pour le '\n' consommé
	}

	for _, f := range r.postFilters {
		allEntities = f(allEntities)
	}

	return allEntities, nil
}

// recognizeLine effectue la reconnaissance NER sur une seule ligne de texte.
// La ligne est elle-même découpée en phrases aux frontières syntaxiques
// (`.`, `!`, `?`, `;`) détectées dans le flux de tokens — chaque phrase
// est traitée indépendamment par le CRF.
// byteOffset est l'offset de début de la ligne dans le texte original complet ;
// il est ajouté aux offsets Start/End de chaque entité détectée.
func (r *Recognizer) recognizeLine(line string, byteOffset int, labelIndex map[string]int) []Entity {
	if strings.TrimSpace(line) == "" {
		return nil
	}

	allTokens := r.tok.Tokenize(line)

	var entities []Entity
	var segment []tokenizer.Token

	flushSegment := func() {
		wordTokens := extractWordTokens(segment)
		// Séquence soumise au CRF : mots seuls, ou tous les tokens (ponctuation
		// comprise) si WithPunctuationTokens est actif — comme à l'entraînement.
		seqTokens := wordTokens
		if r.includePunctuation {
			seqTokens = append([]tokenizer.Token(nil), segment...)
		}
		segment = segment[:0]
		if len(wordTokens) == 0 {
			return
		}
		words := tokenTexts(seqTokens)
		lowerWords := make([]string, len(words))
		for i, w := range words {
			lowerWords[i] = strings.ToLower(w)
		}
		feats := make([]map[string]float64, len(seqTokens))
		for i := range seqTokens {
			feats[i] = r.extractor.FeaturesEx(words, lowerWords, i)
		}
		labels := r.crf.Predict(feats)
		marginals := r.crf.PredictMarginals(feats)
		fixedLabels := FixBIOViolations(labels)
		segEntities := decodeEntitiesWithScores(seqTokens, fixedLabels, marginals, labelIndex)
		for i := range segEntities {
			segEntities[i].Start += byteOffset
			segEntities[i].End += byteOffset
		}
		entities = append(entities, segEntities...)
	}

	for _, tok := range allTokens {
		segment = append(segment, tok)
		if r.isSentenceBoundary(tok) {
			flushSegment()
		}
	}
	flushSegment()

	return entities
}

// isSentenceBoundary retourne true si le token marque une fin de phrase
// selon la configuration du Recognizer.
func (r *Recognizer) isSentenceBoundary(tok tokenizer.Token) bool {
	return !tok.IsWord && r.sentenceBoundaries[tok.Text]
}

// extractWordTokens retourne uniquement les tokens dont IsWord == true.
func extractWordTokens(tokens []tokenizer.Token) []tokenizer.Token {
	result := make([]tokenizer.Token, 0, len(tokens))
	for _, t := range tokens {
		if t.IsWord {
			result = append(result, t)
		}
	}
	return result
}

// tokenTexts retourne les formes de surface des tokens.
func tokenTexts(tokens []tokenizer.Token) []string {
	texts := make([]string, len(tokens))
	for i, t := range tokens {
		texts[i] = t.Text
	}
	return texts
}
