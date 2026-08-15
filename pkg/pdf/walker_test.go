package pdf

import "testing"

func TestEncodePDFString_CaractereHorsWinAnsi(t *testing.T) {
	// Une chaîne PDF littérale est relue octet par octet. Écrire les octets
	// UTF-8 de « █ » (E2 96 88) affichait « â–ˆ » dans les lecteurs, ce qui
	// rendait illisibles les pages caviardées.
	got := string(encodePDFString("██", false))
	if got != "(##)" {
		t.Errorf("encodePDFString(\"██\") = %q, want %q", got, "(##)")
	}
}

func TestEncodePDFString_UnOctetParRune(t *testing.T) {
	// Le masque doit garder la largeur du texte remplacé : une rune non
	// représentable ne doit pas produire plusieurs octets.
	for _, s := range []string{"█", "字", "→"} {
		if n := len(encodePDFString(s, false)) - 2; n != 1 {
			t.Errorf("encodePDFString(%q) : %d octets entre parenthèses, want 1", s, n)
		}
	}
}

func TestEncodePDFString_PreserveWinAnsi(t *testing.T) {
	// Les accents français restent représentables et ne doivent pas être
	// remplacés.
	got := string(encodePDFString("Éric", false))
	if got != "(\xc9ric)" {
		t.Errorf("encodePDFString(\"Éric\") = %q", got)
	}
}

func TestDecodeTJArray_EspacesImplicites(t *testing.T) {
	// TeX encode l'espace inter-mots par un déplacement, sans émettre le
	// caractère. Les ignorer collait les mots et rendait la ligne
	// insegmentable pour le modèle.
	got, _ := decodeTJArray([]byte("[(Free) -300 (Forfait) -280 (Mobile)]"), nil)
	if got != "Free Forfait Mobile" {
		t.Errorf("decodeTJArray = %q, want %q", got, "Free Forfait Mobile")
	}
}

func TestDecodeTJArray_CrenageNeSeparePas(t *testing.T) {
	// Un crénage de paire serrée ne doit pas produire d'espace.
	got, _ := decodeTJArray([]byte("[(A) -80 (V) 20 (a)]"), nil)
	if got != "AVa" {
		t.Errorf("decodeTJArray = %q, want %q", got, "AVa")
	}
}
