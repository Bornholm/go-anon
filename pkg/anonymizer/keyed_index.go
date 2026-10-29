package anonymizer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/bornholm/go-anon/pkg/ner"
)

// maxKeyedIndex borne les index dérivés par WithIndexKey : six chiffres au
// plus, assez pour que deux entités d'un même texte ne se disputent presque
// jamais un index, assez peu pour que le modèle les recopie sans erreur.
const maxKeyedIndex = 999_999

// ErrIndexSpaceFull signale qu'aucun index n'est resté libre pour une entité :
// inatteignable en pratique, l'appel et sa session devraient réunir près d'un
// million d'entités du même type, au-delà de la limite d'une session
// (SetMaxEntities).
var ErrIndexSpaceFull = errors.New("anonymizer: plus aucun index de placeholder libre")

// keyedIndexes indique si l'index du placeholder de ent est dérivé de l'entité
// (WithIndexKey) : seulement pour les stratégies dont le placeholder porte un
// index, et hors remplaçant personnalisé, libre d'ignorer l'index.
func (a *Anonymizer) keyedIndexes(ent ner.Entity, params *anonymizeParams) bool {
	if !params.indexKeySet {
		return false
	}
	if _, custom := a.config.CustomReplacers[ent.Type]; custom {
		return false
	}
	return a.config.Strategy == TagReplace || a.config.Strategy == Consistent
}

// keyedReplacement construit le placeholder de ent à l'index dérivé de
// l'entité.
//
// Deux entités distinctes d'un même type peuvent tomber sur le même index :
// la seconde prend alors le suivant libre. Son placeholder dépend dans ce cas
// des entités présentes, comme avec la numérotation séquentielle, mais pour
// elle seule et rarement : avec six chiffres, quelques dizaines d'entités d'un
// type ne se rencontrent qu'une fois sur quelques milliers de textes.
//
// La clé a été validée par Anonymize.
func (a *Anonymizer) keyedReplacement(ent ner.Entity, params *anonymizeParams, mapping map[string]string) (string, error) {
	normalized := normalizeForFuzzy(ent.Text)
	index := keyedIndex(params.indexKey, ent.Type, normalized)
	for range maxKeyedIndex {
		replacement, err := a.replace(ent, index, params)
		if err != nil {
			return "", err
		}
		if !placeholderTaken(replacement, normalized, params, mapping) {
			return replacement, nil
		}
		index = index%maxKeyedIndex + 1
	}
	return "", ErrIndexSpaceFull
}

// sequentialReplacement construit le placeholder de ent au prochain index de
// son type, dans l'ordre d'apparition.
//
// Les compteurs d'une session ne voient pas les index dérivés : quand des
// appels avec et sans WithIndexKey partagent une session, un index séquentiel
// peut être déjà pris par un placeholder dérivé, et il est alors sauté.
func (a *Anonymizer) sequentialReplacement(ent ner.Entity, counters map[ner.EntityType]int, params *anonymizeParams, mapping map[string]string) (string, error) {
	normalized := normalizeForFuzzy(ent.Text)
	for {
		counters[ent.Type]++
		replacement, err := a.replace(ent, counters[ent.Type], params)
		if err != nil {
			return "", err
		}
		if !placeholderTaken(replacement, normalized, params, mapping) {
			return replacement, nil
		}
	}
}

// placeholderTaken indique si replacement désigne déjà, dans cet appel ou dans
// la session, une autre entité que celle de forme normalisée normalized.
func placeholderTaken(replacement, normalized string, params *anonymizeParams, mapping map[string]string) bool {
	owner, taken := mapping[replacement]
	if !taken && params.session != nil {
		owner, taken = params.session.Mapping[replacement]
	}
	return taken && normalizeForFuzzy(owner) != normalized
}

// keyedIndex dérive de la clé, du type et de la forme normalisée d'une entité
// un index entre 1 et maxKeyedIndex. Le type est séparé de la forme par un
// octet nul, comme dans hashEntity.
func keyedIndex(key HashKey, t ner.EntityType, normalized string) int {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(t))
	mac.Write([]byte{0x00})
	mac.Write([]byte(normalized))
	return int(binary.BigEndian.Uint64(mac.Sum(nil))%maxKeyedIndex) + 1
}
