// Package goanon fournit une API de haut niveau pour la reconnaissance d'entités
// nommées (NER) et l'anonymisation de texte en français et en anglais.
//
// Usage minimal :
//
//	f, _ := os.Open("model.crf.gz")
//	m, err := goanon.LoadModel(f)
//	r, err := goanon.NewRecognizer(m, goanon.WithLanguage("fr"))
//	entities, err := r.Recognize("Jean Dupont habite à Paris.")
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
)

var DefaultSentenceBoundaries = ner.DefaultSentenceBoundaries

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
var WithBrownClusters = ner.WithBrownClusters
var WithPostFilters = ner.WithPostFilters
var WithMergePass = ner.WithMergePass
var WithNameCompletionPass = ner.WithNameCompletionPass

// — Filtres post-NER —

var MinConfidenceFilter = ner.MinConfidenceFilter
var MaxTokensFilter = ner.MaxTokensFilter
var BlocklistFilter = ner.BlocklistFilter
var MergePass = ner.MergePass
var NameCompletionPass = ner.NameCompletionPass

// — Features (réexports pour gazetteers / clusters) —

type Gazetteer = features.Gazetteer
type BrownClusters = features.BrownClusters

var LoadGazetteer = features.LoadGazetteer
var LoadBrownClusters = features.LoadBrownClusters
