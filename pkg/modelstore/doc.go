// Package modelstore provides automatic discovery, download, verification,
// and caching of NER models published on GitHub Releases.
//
// # Overview
//
// The Store type is the main entry point. It manages:
//   - Discovery of the model manifest (JSON catalog of available models)
//   - Download of model files with SHA-256 verification
//   - Disk caching to avoid redundant downloads
//   - Inter-process locking to prevent duplicate downloads
//
// # Manifest Schema
//
// The manifest.json file follows this schema:
//
//	{
//	  "schema_version": 1,
//	  "version": "models-v1",
//	  "published_at": "2025-01-15T10:00:00Z",
//	  "models": {
//	    "fr": {
//	      "url": "https://github.com/.../models-v1/fr.crf.gz",
//	      "sha256": "abc123...",
//	      "size_bytes": 262144000,
//	      "metadata": { "f1": 0.847, "corpus": "WikiNER-fr" }
//	    }
//	  }
//	}
//
// # Default Configuration
//
// By default, Store fetches the manifest from:
//
//	https://bornholm.github.io/go-anon-resources/manifest.json
//
// Models are cached in os.UserCacheDir()/go-anon/models.
// The manifest TTL is 6 hours. Download timeout is 15 minutes.
//
// # Thread Safety
//
// All Store methods are safe for concurrent use. Inter-process
// locking is handled via flock (Unix) or LockFileEx (Windows).
//
// # Example
//
//	store, err := modelstore.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	path, err := store.Get(ctx, "fr")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println("model path:", path)
package modelstore
