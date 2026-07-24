package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"runtime"
	"time"
)

// maxBytesExceeded rapporte si err provient d'un http.MaxBytesReader ayant
// dépassé la limite de corps (à mapper en 413 plutôt qu'en 400).
func maxBytesExceeded(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// contextWithRequestID attache l'identifiant de corrélation au contexte.
func contextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// La politique de sécurité du serveur tient en une phrase : rien de ce qui
// transite (corps de requête, texte source, forme de surface d'entité) ne doit
// apparaître dans un log ou une réponse d'erreur. Les middlewares ci-dessous
// l'appliquent mécaniquement, indépendamment des handlers.

type ctxKey string

const requestIDKey ctxKey = "requestID"

// newRequestID tire un identifiant de corrélation court. Il relie une réponse
// d'erreur générique renvoyée au client à la ligne de log correspondante côté
// serveur, sans jamais exposer de contenu.
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// statusRecorder capture le code de statut et le volume écrit pour le log
// d'accès, sans toucher au corps.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// requestIDFrom retourne l'identifiant de corrélation associé à la requête.
func requestIDFrom(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey).(string); ok {
		return v
	}
	return "-"
}

// withRequestID attribue un identifiant de corrélation et le renvoie dans
// l'en-tête X-Request-Id.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		ctx := contextWithRequestID(r.Context(), id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// noStore interdit toute mise en cache intermédiaire : une réponse peut contenir
// du texte pseudonymisé (ou, sur un chemin d'erreur, rien du tout), et aucun
// proxy ne doit la conserver.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// recoverPanic intercepte les panics. Le stack trace est logué SANS les
// arguments de la requête : on n'imprime ni r, ni les buffers, ni la valeur de
// panic (qui pourrait porter du contenu utilisateur). Le client reçoit une
// erreur générique avec l'identifiant de corrélation.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				id := requestIDFrom(r)
				// Stack seul, jamais la valeur de panic ni la requête.
				var stack [4096]byte
				n := runtime.Stack(stack[:], false)
				log.Printf("panic req=%s : récupérée (%d octets de stack)\n%s", id, n, stack[:n])
				http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// accessLog journalise des métadonnées seulement : méthode, chemin, statut,
// durée, taille de réponse, identifiant de corrélation. Jamais de corps, jamais
// de query string (elle pourrait porter du contenu), jamais d'en-têtes clients.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.Printf("req=%s %s %s -> %d (%d o, %s)",
			requestIDFrom(r), r.Method, r.URL.Path, rec.status, rec.bytes, time.Since(start))
	})
}

// limitConcurrency borne le nombre d'anonymisations simultanées. L'inférence CRF
// est CPU-bound : sans limite, une rafale de requêtes met en mémoire autant de
// documents clients qu'il y a de connexions, jusqu'à l'OOM. Au-delà du plafond,
// la requête attend un créneau pendant une durée bornée, puis reçoit 429.
func limitConcurrency(sem chan struct{}, acquireTimeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		case <-time.After(acquireTimeout):
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy, retry later", http.StatusTooManyRequests)
		case <-r.Context().Done():
			// Client parti avant d'obtenir un créneau : ne rien faire.
		}
	})
}

// chain compose des middlewares dans l'ordre : chain(h, a, b, c) == a(b(c(h))).
func chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
