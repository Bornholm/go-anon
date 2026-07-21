// Package goanon fournit une API de haut niveau pour la reconnaissance d'entités
// nommées (NER) et l'anonymisation de texte en français, anglais et espagnol.
//
// Usage minimal :
//
//	f, _ := os.Open("model.crf.gz")
//	m, err := goanon.LoadModel(f)
//	r, err := goanon.NewRecognizer(m, goanon.WithLanguage("fr"))
//	entities, err := r.Recognize("Jean Dupont habite à Paris.")
//
// Le Recognizer applique par défaut la configuration d'inférence validée sur
// WikiNER : ponctuation conservée dans les séquences CRF et découpage aux
// seules fins de phrase (cf. WithPunctuationTokens, WithSentenceBoundaries).
// NewRecognizer propage automatiquement le schéma de features et la fenêtre de
// contexte enregistrés dans le modèle ; Recognizer.Warnings() signale tout
// écart entre la configuration du modèle et celle de l'inférence (gazetteers
// ou Brown clusters manquants, langue différente).
//
// Anonymisation :
//
//	anon := goanon.NewAnonymizer(r, goanon.Config{Strategy: goanon.TagReplace})
//	result, err := anon.Anonymize("Jean Dupont habite à Paris.")
//	// result.Text == "[PERSON_1] habite à [LOCATION_1]."
package goanon

import (
	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/langdetect"
	"github.com/bornholm/go-anon/pkg/ner"
)

// — Types NER —

type Model = ner.Model
type Entity = ner.Entity
type EntityType = ner.EntityType
type EntityFilter = ner.EntityFilter
type RecognizerOption = ner.RecognizerOption

const (
	TypePER  = ner.TypePER
	TypeLOC  = ner.TypeLOC
	TypeORG  = ner.TypeORG
	TypeMISC = ner.TypeMISC

	TypeEMAIL = ner.TypeEMAIL
	TypeIPV4  = ner.TypeIPV4
	TypeIPV6  = ner.TypeIPV6
	TypeIBAN  = ner.TypeIBAN
	TypeSIRET = ner.TypeSIRET
	TypeSIREN = ner.TypeSIREN
	TypePHONE = ner.TypePHONE

	TypeAPIKey = ner.TypeAPIKey
	TypeJWT    = ner.TypeJWT
	TypeSecret = ner.TypeSecret
)

var DefaultSentenceBoundaries = ner.DefaultSentenceBoundaries

// SupportedLanguages retourne les codes ISO 639-1 gérés par le pipeline (fr/en/es).
var SupportedLanguages = ner.SupportedLanguages

// — Détection de langue —

type LanguageDetector = langdetect.Detector
type LanguageResult = langdetect.Result

// NewWhatlangDetector construit un détecteur de langue basé sur whatlanggo,
// restreint aux codes ISO 639-1 fournis.
var NewWhatlangDetector = langdetect.NewWhatlangDetector

// — Types Anonymizer —

type Strategy = anonymizer.Strategy
type Config = anonymizer.Config
type ReplacerFunc = anonymizer.ReplacerFunc
type Result = anonymizer.Result
type Recognizer = anonymizer.Recognizer
type AnonymizePass = anonymizer.AnonymizePass

const (
	TagReplace = anonymizer.TagReplace
	Redact     = anonymizer.Redact
	Hash       = anonymizer.Hash
	Consistent = anonymizer.Consistent
)

// — Constructeurs —

// LoadModel charge un modèle CRF sérialisé depuis r (format gob+gzip).
var LoadModel = ner.LoadModel

// NewRecognizer construit un Recognizer NER avec le modèle m et les options fournies.
// Voir WithLanguage, WithGazetteers, WithBrownClusters, WithPostFilters.
var NewRecognizer = ner.New

// NewAnonymizer crée un Anonymizer qui s'appuie sur le Recognizer donné.
var NewAnonymizer = anonymizer.New

// — Passes de post-traitement de l'anonymiseur —

var ConsistencyPass = anonymizer.ConsistencyPass
var SurnameCompletionPass = anonymizer.SurnameCompletionPass

// — Options du Recognizer —

var WithLanguage = ner.WithLanguage
var WithGazetteers = ner.WithGazetteers
var WithSentenceBoundaries = ner.WithSentenceBoundaries
var WithPunctuationTokens = ner.WithPunctuationTokens
var WithConfidenceScores = ner.WithConfidenceScores
var WithBrownClusters = ner.WithBrownClusters
var WithPostFilters = ner.WithPostFilters
var WithFirstNameReclassify = ner.WithFirstNameReclassify
var WithFirstNameDetectionPass = ner.WithFirstNameDetectionPass
var WithMergePass = ner.WithMergePass
var WithNameCompletionPass = ner.WithNameCompletionPass

// — Filtres post-NER —

var FirstNameReclassifyFilter = ner.FirstNameReclassifyFilter
var FirstNameDetectionFilter = ner.FirstNameDetectionFilter
var MinConfidenceFilter = ner.MinConfidenceFilter
var MaxTokensFilter = ner.MaxTokensFilter
var BlocklistFilter = ner.BlocklistFilter
var MergePass = ner.MergePass
var NameCompletionPass = ner.NameCompletionPass
var RegexEntityFilter = ner.RegexEntityFilter
var WithRegexPatterns = ner.WithRegexPatterns
var WithBuiltinRegexPatterns = ner.WithBuiltinRegexPatterns
var BuiltinRegexPatterns = ner.BuiltinRegexPatterns
var WithBuiltinSecretPatterns = ner.WithBuiltinSecretPatterns
var SecretPatterns = ner.SecretPatterns

// RegexPattern associe une expression régulière à un type d'entité.
type RegexPattern = ner.RegexPattern

// — Features (réexports pour gazetteers / clusters) —

type Gazetteer = features.Gazetteer
type BrownClusters = features.BrownClusters

var LoadGazetteer = features.LoadGazetteer
var LoadBrownClusters = features.LoadBrownClusters
