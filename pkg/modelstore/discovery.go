package modelstore

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type Discoverer interface {
	Discover(ctx context.Context) (*Manifest, string, error)
}

type StaticDiscoverer struct {
	URL        string
	HTTPClient *http.Client

	// TrustedKey, si non nil et SkipVerify == false, impose la vérification de
	// la signature minisign du manifest (fichier <URL>.minisig) AVANT de faire
	// confiance à son contenu — donc avant de faire confiance aux SHA-256 qu'il
	// porte. La chaîne : signature → manifest → hash → modèle.
	TrustedKey *PublicKey
	// SkipVerify désactive explicitement la vérification (manifests custom non
	// signés) ; à n'utiliser qu'en connaissance de cause.
	SkipVerify bool
	// SignatureURL surcharge l'emplacement de la signature (défaut : URL+".minisig").
	SignatureURL string
}

func NewStaticDiscoverer(url string) *StaticDiscoverer {
	return &StaticDiscoverer{URL: url}
}

func (d *StaticDiscoverer) Discover(ctx context.Context) (*Manifest, string, error) {
	client := d.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("static discoverer: create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("static discoverer: fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("static discoverer: GET %s: %s", d.URL, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("static discoverer: read body: %w", err)
	}

	// Vérification de la signature AVANT tout parsing/confiance dans le contenu.
	if err := d.verifySignature(ctx, client, data); err != nil {
		return nil, "", err
	}

	m, err := ParseManifest(data)
	if err != nil {
		return nil, "", fmt.Errorf("static discoverer: %w", err)
	}

	return m, d.URL, nil
}

// verifySignature applique la politique de signature du discoverer sur le
// manifest brut. Trois cas :
//   - SkipVerify : aucune vérification (choix explicite de l'appelant) ;
//   - TrustedKey == nil : aucune clé de confiance disponible → avertissement
//     bruyant et poursuite (rétrocompatibilité tant que les releases ne sont pas
//     signées, cf. keys.go) ;
//   - TrustedKey != nil : fail-closed — la signature doit être présente et valide.
func (d *StaticDiscoverer) verifySignature(ctx context.Context, client *http.Client, manifest []byte) error {
	if d.SkipVerify {
		log.Printf("modelstore: AVERTISSEMENT: vérification de signature désactivée (-insecure-skip-verify)")
		return nil
	}
	if d.TrustedKey == nil {
		log.Printf("modelstore: AVERTISSEMENT: manifest non vérifié (aucune clé de confiance embarquée) — l'authenticité de la source n'est pas garantie")
		return nil
	}

	sigURL := d.SignatureURL
	if sigURL == "" {
		sigURL = d.URL + ".minisig"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sigURL, nil)
	if err != nil {
		return fmt.Errorf("static discoverer: create signature request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("static discoverer: fetch signature %s: %w", sigURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("static discoverer: GET %s: %s", sigURL, resp.Status)
	}

	sigData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("static discoverer: read signature: %w", err)
	}

	if err := d.TrustedKey.Verify(manifest, sigData); err != nil {
		return fmt.Errorf("static discoverer: %w", err)
	}
	return nil
}

type ChainDiscoverer struct {
	Discoverers []Discoverer
}

func NewChainDiscoverer(discoverers ...Discoverer) *ChainDiscoverer {
	return &ChainDiscoverer{Discoverers: discoverers}
}

func (d *ChainDiscoverer) Discover(ctx context.Context) (*Manifest, string, error) {
	var errs []error
	for _, disc := range d.Discoverers {
		m, source, err := disc.Discover(ctx)
		if err == nil {
			return m, source, nil
		}
		errs = append(errs, err)
	}

	var msg strings.Builder
	msg.WriteString("chain discoverer: all sources failed:")
	for i, e := range errs {
		msg.WriteString(fmt.Sprintf("\n  [%d] %v", i+1, e))
	}
	return nil, "", fmt.Errorf("%s", msg.String())
}
