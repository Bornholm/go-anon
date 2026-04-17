package ner

import (
	"github.com/bornholm/go-anon/pkg/corpus"
)

// FixBIOViolations corrige les séquences BIO invalides en sortie.
// Les règles appliquées:
// - I-X après O → convertir en B-X
// - I-X après une entité différente → convertir en B-X
// - I-X au début → convertir en B-X
// - E-X après une entité différente → convertir en B-X
func FixBIOViolations(tags []string) []string {
	if len(tags) == 0 {
		return tags
	}

	fixed := make([]string, len(tags))
	copy(fixed, tags)

	for i, tag := range fixed {
		prefix := corpus.TagPrefix(tag)
		entity := corpus.TagEntity(tag)

		if prefix == "O" || entity == "" {
			continue
		}

		if prefix == "I" {
			if i == 0 {
				fixed[i] = "B-" + entity
				continue
			}
			prevPrefix := corpus.TagPrefix(fixed[i-1])
			prevEntity := corpus.TagEntity(fixed[i-1])

			if prevPrefix == "O" || prevEntity != entity {
				fixed[i] = "B-" + entity
			}
		}

		if prefix == "E" {
			if i == 0 {
				fixed[i] = "B-" + entity
				continue
			}
			prevPrefix := corpus.TagPrefix(fixed[i-1])
			prevEntity := corpus.TagEntity(fixed[i-1])

			if prevPrefix == "O" || prevEntity != entity {
				fixed[i] = "B-" + entity
			}
		}

		if prefix == "S" && i > 0 && i < len(fixed)-1 {
			prevPrefix := corpus.TagPrefix(fixed[i-1])
			prevEntity := corpus.TagEntity(fixed[i-1])
			nextPrefix := corpus.TagPrefix(fixed[i+1])
			nextEntity := corpus.TagEntity(fixed[i+1])

			if prevPrefix != "O" && prevEntity == entity && nextPrefix != "O" && nextEntity == entity {
				fixed[i] = "B-" + entity
			}
		}
	}

	return fixed
}

// FixBIOESViolations corrige les violations spécifiques au schéma BIOES.
// S peut être seul ou suivi de E, jamais de I.
func FixBIOESViolations(tags []string) []string {
	if len(tags) == 0 {
		return tags
	}

	fixed := make([]string, len(tags))
	copy(fixed, tags)

	for i := 1; i < len(fixed); i++ {
		prefix := corpus.TagPrefix(fixed[i])
		entity := corpus.TagEntity(fixed[i])
		prevPrefix := corpus.TagPrefix(fixed[i-1])
		prevEntity := corpus.TagEntity(fixed[i-1])

		if prefix == "I" && (prevPrefix == "O" || prevPrefix == "S" || (prevEntity != entity)) {
			fixed[i] = "B-" + entity
		}
	}

	return fixed
}

// NormalizeTags normalise les tags pour'assurer la cohérence.
// Utilise FixBIOViolations pour corriger les séquences invalides.
func NormalizeTags(tags []string, useBIOES bool) []string {
	if useBIOES {
		return FixBIOESViolations(FixBIOViolations(tags))
	}
	return FixBIOViolations(tags)
}

// TagsToEntities convertit une séquence de tags en entités avec correction BIO.
func TagsToEntities(tags []string, tokens []string) []Entity {
	normalized := FixBIOViolations(tags)
	return tagsToEntitiesImpl(normalized, tokens)
}

func tagsToEntitiesImpl(tags []string, tokens []string) []Entity {
	var entities []Entity
	var current *Entity

	flush := func() {
		if current != nil {
			entities = append(entities, *current)
			current = nil
		}
	}

	for i, tag := range tags {
		prefix := corpus.TagPrefix(tag)
		entity := corpus.TagEntity(tag)

		switch prefix {
		case "B":
			flush()
			start := tokenOffset(tokens, i)
			current = &Entity{
				Text:       tokens[i],
				Type:       EntityType(entity),
				Start:      start,
				End:        start + len(tokens[i]),
				Confidence: 1.0,
			}

		case "I":
			if current != nil && EntityType(entity) == current.Type {
				current.Text += " " + tokens[i]
				start := tokenOffset(tokens, i)
				current.End = start + len(tokens[i])
			} else {
				flush()
			}

		case "E":
			if current != nil && EntityType(entity) == current.Type {
				current.Text += " " + tokens[i]
				start := tokenOffset(tokens, i)
				current.End = start + len(tokens[i])
			}
			flush()

		case "S":
			flush()
			start := tokenOffset(tokens, i)
			entities = append(entities, Entity{
				Text:       tokens[i],
				Type:       EntityType(entity),
				Start:      start,
				End:        start + len(tokens[i]),
				Confidence: 1.0,
			})

		default:
			flush()
		}
	}

	flush()
	return entities
}

func tokenOffset(tokens []string, idx int) int {
	offset := 0
	for i := 0; i < idx && i < len(tokens); i++ {
		offset += len(tokens[i])
		if i < idx-1 {
			offset++ // espace
		}
	}
	return offset
}
