package modelstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultDownloadTimeout = 15 * time.Minute
	maxRetries             = 1
)

func validateURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("%w: got %s", ErrInvalidURL, url)
	}
	return nil
}

func downloadModel(ctx context.Context, client *http.Client, url, expectedSHA, destDir, lang string, progressFn func(string, int64, int64)) (string, error) {
	if err := validateURL(url); err != nil {
		return "", err
	}

	isTTY := isTerminal()

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(1 * time.Second)
		}

		path, err := downloadOnce(ctx, client, url, expectedSHA, destDir, lang, progressFn, isTTY)
		if err == nil {
			return path, nil
		}

		lastErr = err
		if err == ErrChecksumMismatch {
			tmpPath := filepath.Join(destDir, ".tmp-"+lang+"-*")
			matches, _ := filepath.Glob(tmpPath)
			for _, m := range matches {
				os.Remove(m)
			}
			continue
		}
		break
	}

	return "", lastErr
}

func downloadOnce(ctx context.Context, client *http.Client, url, expectedSHA, destDir, lang string, progressFn func(string, int64, int64), isTTY bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("modelstore: create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("modelstore: download %s: %w", lang, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("modelstore: download %s: %s", lang, resp.Status)
	}

	tmpFile, err := os.CreateTemp(destDir, ".tmp-"+lang+"-")
	if err != nil {
		return "", fmt.Errorf("modelstore: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	cleanup := func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}

	h := sha256.New()
	var writer io.Writer = io.MultiWriter(tmpFile, h)

	if progressFn != nil || isTTY {
		pb := NewProgressBar(lang, resp.ContentLength, progressFn, isTTY)
		writer = io.MultiWriter(tmpFile, h, &progressWriter{pw: pb})
		defer pb.Finish()
	}

	if _, err := io.Copy(writer, resp.Body); err != nil {
		cleanup()
		return "", fmt.Errorf("modelstore: write model %s: %w", lang, err)
	}

	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("modelstore: close temp file: %w", err)
	}

	gotSHA := hex.EncodeToString(h.Sum(nil))
	if gotSHA != expectedSHA {
		cleanup()
		return "", fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedSHA, gotSHA)
	}

	destPath := modelCachePath(destDir, lang)
	if err := os.Rename(tmpPath, destPath); err != nil {
		cleanup()
		return "", fmt.Errorf("modelstore: rename to final path: %w", err)
	}

	return destPath, nil
}

func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
