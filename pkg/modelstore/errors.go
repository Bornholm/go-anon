package modelstore

import "errors"

var (
	ErrOffline          = errors.New("offline mode: model not in cache")
	ErrLanguageNotFound = errors.New("language not found in manifest")
	ErrChecksumMismatch = errors.New("checksum verification failed")
	ErrManifestSchema   = errors.New("unsupported manifest schema version")
	ErrInvalidURL       = errors.New("model URL must use HTTPS")
	ErrDownloadTimeout  = errors.New("model download timed out")
	ErrManifestExpired  = errors.New("manifest cache expired and offline mode enabled")
	ErrLockTimeout      = errors.New("timed out waiting for file lock")
	ErrInvalidLang      = errors.New("language code must match ^[a-z]{2}$")
)
