package modelstore

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateURL(t *testing.T) {
	if err := validateURL("https://example.com/model.crf.gz"); err != nil {
		t.Errorf("expected valid HTTPS URL: %v", err)
	}

	if err := validateURL("http://example.com/model.crf.gz"); err == nil {
		t.Error("expected error for HTTP URL")
	}

	if err := validateURL("ftp://example.com/model.crf.gz"); err == nil {
		t.Error("expected error for FTP URL")
	}
}

func testHTTPSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func TestDownloadModelSuccess(t *testing.T) {
	content := []byte("fake model content")
	expectedSHA := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expectedSHA[:])

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()

	path, err := downloadModel(context.Background(), testHTTPSClient(), server.URL, expectedHex, dir, "fr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(dir, "fr.crf.gz")
	if path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch")
	}
}

func TestDownloadModelChecksumMismatch(t *testing.T) {
	content := []byte("fake model content")

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()

	_, err := downloadModel(context.Background(), testHTTPSClient(), server.URL, "wrongsha", dir, "fr", nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestDownloadModel404(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	dir := t.TempDir()

	_, err := downloadModel(context.Background(), testHTTPSClient(), server.URL, "abc123", dir, "fr", nil)
	if err == nil {
		t.Fatal("expected 404 error")
	}
}

func TestDownloadModelHTTPRejected(t *testing.T) {
	dir := t.TempDir()

	_, err := downloadModel(context.Background(), http.DefaultClient, "http://example.com/model.crf.gz", "abc123", dir, "fr", nil)
	if err == nil {
		t.Fatal("expected error for HTTP URL")
	}
}

func TestDownloadModelProgressCallback(t *testing.T) {
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	expectedSHA := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expectedSHA[:])

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000")
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()

	var calls int
	var lastDone, lastTotal int64
	progressFn := func(lang string, done, total int64) {
		calls++
		lastDone = done
		lastTotal = total
	}

	_, err := downloadModel(context.Background(), testHTTPSClient(), server.URL, expectedHex, dir, "fr", progressFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calls == 0 {
		t.Fatal("expected progress callbacks")
	}
	if lastTotal != 10000 {
		t.Errorf("expected total 10000, got %d", lastTotal)
	}
	if lastDone != 10000 {
		t.Errorf("expected done 10000, got %d", lastDone)
	}
}
