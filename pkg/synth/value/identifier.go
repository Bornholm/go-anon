package value

import (
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/checksum"
)

// luhnCheckDigit retourne le chiffre à ajouter à body pour que le tout
// satisfasse la formule de Luhn.
func luhnCheckDigit(body string) int {
	sum := 0
	double := true // le chiffre ajouté sera en position 0 depuis la droite
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return (10 - sum%10) % 10
}

// newSIREN produit un SIREN valide au sens de Luhn.
func (g *Generator) newSIREN() string {
	body := fmt.Sprintf("%08d", g.rng.Intn(100000000))
	s := body + fmt.Sprint(luhnCheckDigit(body))
	if !checksum.SIREN(s) {
		// Filet : une génération invalide indiquerait un bug de calcul de clé,
		// pas un aléa. Mieux vaut échouer bruyamment qu'empoisonner le corpus.
		panic("SIREN généré invalide : " + s)
	}
	return s
}

// siretFor produit un SIRET valide partageant le SIREN fourni.
func (g *Generator) siretFor(siren string) string {
	for attempt := 0; attempt < 100; attempt++ {
		nic := fmt.Sprintf("%04d", g.rng.Intn(10000))
		body := siren + nic
		s := body + fmt.Sprint(luhnCheckDigit(body))
		if checksum.SIRET(s) {
			return s
		}
	}
	panic("impossible de produire un SIRET valide pour " + siren)
}

// newIBAN produit un IBAN français valide : BBAN aléatoire, clé mod-97-10
// calculée. Le même code de clé sert aux négatifs de § 7, ce qui garantit
// qu'un « invalide » l'est réellement.
func (g *Generator) newIBAN() string {
	bban := fmt.Sprintf("%05d%05d%011d%02d",
		g.rng.Intn(100000), g.rng.Intn(100000),
		g.rng.Int63n(100000000000), g.rng.Intn(100))
	key := ibanKey("FR", bban)
	iban := fmt.Sprintf("FR%02d%s", key, bban)
	if !checksum.IBAN(iban) {
		panic("IBAN généré invalide : " + iban)
	}
	return groupBy4(iban)
}

// ibanKey calcule la clé de contrôle d'un IBAN à partir du pays et du BBAN.
func ibanKey(country, bban string) int {
	rem := mod97(bban + letterToDigits(country) + "00")
	return 98 - rem
}

func letterToDigits(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteString(fmt.Sprint(int(s[i]-'A') + 10))
	}
	return b.String()
}

func mod97(digits string) int {
	rem := 0
	for i := 0; i < len(digits); i++ {
		rem = (rem*10 + int(digits[i]-'0')) % 97
	}
	return rem
}

// groupBy4 restitue la présentation par groupes de quatre, majoritaire sur les
// documents réels.
func groupBy4(s string) string {
	var parts []string
	for i := 0; i < len(s); i += 4 {
		end := i + 4
		if end > len(s) {
			end = len(s)
		}
		parts = append(parts, s[i:end])
	}
	return strings.Join(parts, " ")
}

// --- Négatifs difficiles (DATASET.md § 7) ---

// renderDecoy produit un contre-exemple non annoté. Le corpus en a besoin pour
// que le modèle n'apprenne pas « majuscules = entité » ni « quatorze chiffres =
// SIRET ».
func (g *Generator) renderDecoy(kind string) string {
	switch kind {
	case "voie_nom":
		p := g.NewPerson()
		return g.pick("voie_types") + " " + p.First + " " + p.Last
	case "id_secteur":
		return g.idSecteur()
	case "ref_longue":
		return g.refLongue()
	case "titre_caps":
		return g.pick("titres_caps")
	case "mention":
		return g.pick("mentions")
	case "designation":
		return g.pick("designations")
	case "analyse":
		return g.pick("analyses")
	case "domaine":
		// Nom de domaine dérivé d'une activité : évite de recycler un libellé
		// de prestation là où un document réel porte une adresse web.
		return strings.ToLower(stripAccents(strings.ReplaceAll(g.pick("activites"), " ", "-")))
	case "age":
		// Âge entre parenthèses, tel qu'imprimé après une date de naissance sur
		// les comptes-rendus médicaux.
		if g.rng.Intn(3) == 0 {
			return fmt.Sprintf("%d mois %d jours", g.rng.Intn(24), 1+g.rng.Intn(28))
		}
		return fmt.Sprintf("%d ans", 1+g.rng.Intn(95))
	case "code_court":
		// Code APE, BIC, numéro d'accréditation : gabarit court mêlant lettres
		// et chiffres, qu'aucune clé de contrôle ne valide.
		return g.pick("codes_courts")
	default:
		return g.pick("designations")
	}
}

// idSecteur imite les identifiants métier relevés sur les documents réels : numéro client,
// FINESS, point de livraison, identifiant abonné, numéro court.
func (g *Generator) idSecteur() string {
	switch g.rng.Intn(6) {
	case 0:
		return fmt.Sprintf("%d %03d %03d %03d", g.rng.Intn(10), g.rng.Intn(1000), g.rng.Intn(1000), g.rng.Intn(1000))
	case 1:
		return fmt.Sprintf("%08d", g.rng.Intn(100000000))
	case 2:
		return fmt.Sprintf("%02d %03d %03d %03d %03d", g.rng.Intn(100), g.rng.Intn(1000),
			g.rng.Intn(1000), g.rng.Intn(1000), g.rng.Intn(1000))
	case 3:
		return fmt.Sprintf("%04d", 3000+g.rng.Intn(1000))
	case 4:
		return fmt.Sprintf("CL%05d", g.rng.Intn(100000))
	default:
		// Neuf chiffres nus : le cas FINESS, qui satisfait Luhn une fois sur
		// dix et que seul le contexte distingue d'un SIREN.
		return fmt.Sprintf("%09d", g.rng.Intn(1000000000))
	}
}

// refLongue produit une référence au gabarit d'un identifiant à clé, mais dont
// la clé est fausse. Elle est vérifiée invalide, sinon elle deviendrait un vrai
// identifiant annoté par erreur en aval.
func (g *Generator) refLongue() string {
	for attempt := 0; attempt < 100; attempt++ {
		var s string
		if g.rng.Intn(2) == 0 {
			s = fmt.Sprintf("%014d", g.rng.Int63n(100000000000000))
			if !checksum.SIRET(s) {
				return s
			}
		} else {
			s = fmt.Sprintf("FR%02d%011d", g.rng.Intn(100), g.rng.Int63n(100000000000))
			if !checksum.IBAN(s) {
				return s
			}
		}
	}
	// Improbable : 100 tirages consécutifs valides. Retourner une forme
	// structurellement impossible plutôt que de boucler.
	return "REF-INVALIDE-00000"
}
