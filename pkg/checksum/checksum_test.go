package checksum

import "testing"

// Les identifiants utilisés comme vecteurs sont **synthétiques** : construits
// pour satisfaire (ou violer) la clé de contrôle, sans correspondance avec une
// entreprise, un compte ou une personne existants. Un dépôt de code n'a pas à
// transporter d'identifiants réels, fussent-ils publics.
//
// Seuls deux vecteurs font exception, et sont des constantes du domaine :
// le SIREN de La Poste, exception à Luhn inscrite dans la spécification et déjà
// présente dans le code, et les IBAN DE/GB de la norme ISO 13616, publiés comme
// exemples de la norme elle-même.
func TestSIRET(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"sans séparateur", "32145678200002", true},
		{"groupé par espaces", "407 123 454 00008", true},
		{"groupé par espaces", "513 987 651 00009", true},
		{"séparateurs mixtes", "321.456.782.00002", true},
		{"espace insécable", "321 456 782 00002", true},
		{"dernier chiffre altéré", "32145678200003", false},
		{"deux chiffres transposés", "32145678200020", false},
		{"trop court", "3214567820000", false},
		{"trop long", "321456782000020", false},
		{"vide", "", false},
		{"non numérique", "3214567820000A", false},
		{"La Poste (exception Luhn)", "35600000000015", true},
		{"La Poste, somme non multiple de 5", "35600000000014", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SIRET(tt.input); got != tt.want {
				t.Errorf("SIRET(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSIREN(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"clé valide", "321456782", true},
		{"clé valide", "407123454", true},
		{"clé valide", "513987651", true},
		{"La Poste", "356000000", true},
		{"clé fausse", "321456783", false},
		{"trop court", "32145678", false},
		{"longueur SIRET", "32145678200002", false},
		{"vide", "", false},
		// Un identifiant sectoriel à 9 chiffres (FINESS, RPPS…) satisfait Luhn
		// une fois sur dix par pure coïncidence. La clé de contrôle ne peut pas
		// le distinguer d'un SIREN ; seul le contexte le peut. Documenté ici
		// pour que le comportement ne soit pas pris pour un bug.
		{"identifiant sectoriel satisfaisant Luhn", "602453193", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SIREN(tt.input); got != tt.want {
				t.Errorf("SIREN(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIBAN(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"FR groupé", "FR76 3000 1007 9412 3456 7890 185", true},
		{"FR groupé", "FR75 1273 9000 5012 3456 7890 143", true},
		{"FR compact", "FR7512739000501234567890143", true},
		{"minuscules", "fr7512739000501234567890143", true},
		{"FR avec lettre dans le BBAN", "FR1420041010050500013M02606", true},
		{"DE valide (exemple ISO 13616)", "DE89370400440532013000", true},
		{"GB valide (exemple ISO 13616)", "GB82WEST12345698765432", true},
		{"clé altérée", "FR77 3000 1007 9412 3456 7890 185", false},
		{"chiffre altéré", "FR76 3000 1007 9412 3456 7890 184", false},
		// Formes que la regex IBAN de pkg/ner capture et que la clé mod-97
		// écarte : un numéro de TVA intracommunautaire français a le même
		// préfixe « FR » suivi de chiffres, mais pas la bonne longueur.
		{"TVA intracommunautaire FR", "FR12321456782", false},
		{"TVA intracommunautaire FR", "FR34407123454", false},
		{"référence de dossier", "CS4185039772", false},
		{"longueur FR incorrecte", "FR761100621001521497842333", false},
		{"pays inconnu, mod-97 faux", "ZZ0012345678901234", false},
		{"trop court", "FR7611", false},
		{"caractère interdit", "FR76 3000 1007 9412 3456 7890 18!", false},
		{"vide", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IBAN(tt.input); got != tt.want {
				t.Errorf("IBAN(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNIR(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"homme, avril 1980", "180047511630581", true},
		{"groupé par espaces", "1 80 04 75 116 305 81", true},
		{"séparateurs mixtes", "1.80.04.75.116.305.81", true},
		{"clé fausse", "180047511630582", false},
		{"femme", "255087612345659", true},
		{"corse 2A", "180042A12345668", true},
		{"corse 2B", "180042B22345605", true},
		{"corse 2A, clé calculée sans substitution", "180042A12345641", false},
		{"mois 20 (mois inconnu)", "180207511630546", true},
		{"né à l'étranger, mois 99", "180049912345642", true},
		{"sexe invalide", "580047511630581", false},
		{"mois invalide", "180137511630581", false},
		{"département 00", "180040011630581", false},
		{"trop court", "18004751163058", false},
		{"trop long", "1800475116305811", false},
		{"vide", "", false},
		{"lettre hors 2A/2B", "1800475116305Z1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NIR(tt.input); got != tt.want {
				t.Errorf("NIR(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLuhn(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"79927398713", true}, // vecteur canonique de la spécification Luhn
		{"79927398710", false},
		{"0", true},
		{"", false},
		{"12a4", false},
	}
	for _, tt := range tests {
		if got := Luhn(tt.input); got != tt.want {
			t.Errorf("Luhn(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
