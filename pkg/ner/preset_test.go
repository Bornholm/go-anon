package ner

import (
	"math"
	"testing"
)

// TestFBeta vérifie F1 et F2 sur un cas connu : P=0,5 R=1,0.
// F1 = 2PR/(P+R) = 0,667 ; F2 = 5PR/(4P+R) = 0,833 (F2 récompense le rappel).
func TestFBeta(t *testing.T) {
	const p, r = 0.5, 1.0

	f1 := fBeta(p, r, 1)
	if math.Abs(f1-2.0/3.0) > 1e-9 {
		t.Errorf("F1 = %.4f, attendu %.4f", f1, 2.0/3.0)
	}

	f2 := fBeta(p, r, 2)
	if math.Abs(f2-5.0/6.0) > 1e-9 {
		t.Errorf("F2 = %.4f, attendu %.4f", f2, 5.0/6.0)
	}

	// F2 > F1 quand le rappel dépasse la précision : c'est tout l'intérêt RGPD.
	if f2 <= f1 {
		t.Errorf("F2 (%.4f) devrait dépasser F1 (%.4f) quand R > P", f2, f1)
	}

	// Précision et rappel nuls → 0, pas de division par zéro.
	if got := fBeta(0, 0, 2); got != 0 {
		t.Errorf("fBeta(0,0,2) = %v, attendu 0", got)
	}
}

// TestHighRecallSupersetsBalanced : HighRecall ajoute exactement une passe
// (FirstNameDetectionPass, le levier de rappel) au-dessus de Balanced.
func TestHighRecallSupersetsBalanced(t *testing.T) {
	balanced := Balanced(nil)
	highRecall := HighRecall(nil)

	if len(highRecall) != len(balanced)+1 {
		t.Errorf("HighRecall a %d options, attendu %d (Balanced + 1)",
			len(highRecall), len(balanced)+1)
	}

	// PresetOptions doit router correctement.
	if got := len(PresetOptions(PresetHighRecall, nil)); got != len(highRecall) {
		t.Errorf("PresetOptions(high-recall) = %d options, attendu %d", got, len(highRecall))
	}
	if got := len(PresetOptions(PresetBalanced, nil)); got != len(balanced) {
		t.Errorf("PresetOptions(balanced) = %d options, attendu %d", got, len(balanced))
	}
	// Preset inconnu → Balanced (dégradation sûre).
	if got := len(PresetOptions("inconnu", nil)); got != len(balanced) {
		t.Errorf("PresetOptions(inconnu) = %d options, attendu Balanced (%d)", got, len(balanced))
	}
}
