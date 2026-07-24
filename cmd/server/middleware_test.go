package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGuarantee_LogsAndErrorsCarryNoProbe est le test « logs propres » du
// chantier S7 (7.T3). Un marqueur unique injecté dans l'entrée ne doit
// apparaître ni dans ce que le serveur journalise, ni dans la réponse renvoyée
// au client — y compris sur les chemins d'erreur (handler en panique, erreur
// interne forcée).
func TestGuarantee_LogsAndErrorsCarryNoProbe(t *testing.T) {
	const probe = "XZLEAKPROBE"

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	cases := []struct {
		name    string
		handler http.Handler
	}{
		{
			// Le handler panique avec le marqueur comme valeur de panic :
			// recoverPanic ne doit logger que le stack, jamais la valeur.
			name: "panic",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := make([]byte, r.ContentLength)
				_, _ = r.Body.Read(body)
				panic(probe + " secret=" + string(body))
			}),
		},
		{
			// Erreur interne « classique » : le handler a lu un contenu portant
			// le marqueur mais répond de façon générique.
			name: "internal-error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := requestIDFrom(r)
				http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
				// Log métadonnées seulement, jamais le corps.
				log.Printf("op req=%s: échec", id)
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := chain(tc.handler, withRequestID, recoverPanic, accessLog, noStore)
			req := httptest.NewRequest(http.MethodPost, "/op", strings.NewReader(probe+" dans le corps"))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("statut attendu 500, got %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), probe) {
				t.Errorf("le marqueur a fuité dans la réponse : %q", rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control: no-store manquant")
			}
		})
	}

	if strings.Contains(logBuf.String(), probe) {
		t.Errorf("le marqueur a fuité dans les logs :\n%s", logBuf.String())
	}
}

// TestMaxBytesRejectsOversizedBody vérifie qu'un corps dépassant la limite est
// rejeté en 413 avant tout traitement (7.T2).
func TestMaxBytesRejectsOversizedBody(t *testing.T) {
	s := &Server{maxBody: 16}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.limitBody(w, r)
		if _, err := new(bytes.Buffer).ReadFrom(r.Body); err != nil {
			if maxBytesExceeded(err) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("A", 1024)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("statut attendu 413, got %d", rec.Code)
	}
}

// TestLimitConcurrencyRejectsBurst vérifie qu'au-delà du plafond, une requête
// supplémentaire reçoit 429 après une attente bornée (7.T2).
func TestLimitConcurrencyRejectsBurst(t *testing.T) {
	sem := make(chan struct{}, 1)
	release := make(chan struct{})

	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := limitConcurrency(sem, 100*time.Millisecond, blocking)

	// Première requête : occupe le seul créneau.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	}()

	// Laisse la première acquérir le sémaphore.
	time.Sleep(20 * time.Millisecond)

	// Seconde requête : aucun créneau libre → 429 après le timeout d'attente.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("statut attendu 429, got %d", rec.Code)
	}

	close(release)
	wg.Wait()
}
