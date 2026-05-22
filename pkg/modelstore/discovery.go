package modelstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Discoverer interface {
	Discover(ctx context.Context) (*Manifest, string, error)
}

type StaticDiscoverer struct {
	URL        string
	HTTPClient *http.Client
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

	m, err := ParseManifest(data)
	if err != nil {
		return nil, "", fmt.Errorf("static discoverer: %w", err)
	}

	return m, d.URL, nil
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
