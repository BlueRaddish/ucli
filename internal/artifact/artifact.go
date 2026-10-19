// Package artifact acquires explicitly requested, pinned files. It neither
// resolves manifests from projects nor executes or activates downloaded code.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

type Kind string

const (
	Runtime            Kind  = "runtime"
	TranscriptionModel Kind  = "transcription-model"
	MaxArtifactBytes   int64 = 8 << 30
	acquisitionTimeout       = 10 * time.Minute
)

type Platform struct{ OS, Arch string }

// Manifest must come from a shipped/reviewed catalog, never repository settings
// or an untrusted remote manifest. Hashes pin bytes, not publisher trust/safety.
// Model data can use the explicit platform {"any", "any"}; runtimes cannot.
type Manifest struct {
	ID, Version, URL, SHA256 string
	MaxBytes                 int64
	Platform                 Platform
	Kind                     Kind
}

type Options struct{ Offline bool }

// File identifies verified bytes only, not executable admission. Reused includes
// a concurrent writer's verified winner. Warnings report directory-sync limits.
type File struct {
	Path, SHA256 string
	Bytes        int64
	Reused       bool
	Warnings     []string
}

type Code string

const (
	InvalidRequest      Code = "artifact_invalid_request"
	UnsupportedPlatform Code = "artifact_unsupported_platform"
	Missing             Code = "artifact_missing"
	Offline             Code = "artifact_offline"
	AcquisitionFailed   Code = "artifact_acquisition_failed"
	IntegrityFailed     Code = "artifact_integrity_failed"
	CacheUnsafe         Code = "artifact_cache_unsafe"
	CacheIO             Code = "artifact_cache_io"
)

// Error never incorporates response bodies, URLs, query strings or native errors.
// Cancellation is retained through Unwrap for callers' common interruption path.
type Error struct {
	Code    Code
	Message string
	cause   error
}

func (e *Error) Error() string             { return e.Message }
func (e *Error) Unwrap() error             { return e.cause }
func problem(code Code, text string) error { return &Error{Code: code, Message: text} }
func canceled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return &Error{Code: AcquisitionFailed, Message: "Artifact operation was canceled or reached its deadline.", cause: err}
	}
	return nil
}

// Cache stores immutable content-addressed blobs below an explicit UCLI cache
// root. Constructing it does not create directories or perform network requests.
// Use one instance concurrently; its HTTP client is immutable after construction.
type Cache struct {
	root   string
	client *http.Client
}

func New(cacheRoot string) (*Cache, error) {
	root, err := canonicalRoot(cacheRoot)
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, problem(InvalidRequest, "Artifact acquisition requires the standard HTTPS transport.")
	}
	transport := base.Clone()
	transport.DisableCompression = true
	return &Cache{root: root, client: &http.Client{Transport: transport, Timeout: acquisitionTimeout}}, nil
}

func validate(m Manifest) error {
	for _, value := range []string{m.ID, m.Version} {
		if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return problem(InvalidRequest, "Artifact ID and version must be bounded, nonempty text.")
		}
	}
	if m.Kind != Runtime && m.Kind != TranscriptionModel {
		return problem(InvalidRequest, "Artifact kind must be runtime or transcription-model.")
	}
	if m.MaxBytes < 1 || m.MaxBytes > MaxArtifactBytes {
		return problem(InvalidRequest, "Artifact size bound must be positive and at most 8 GiB.")
	}
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != sha256.Size || strings.ToLower(m.SHA256) != m.SHA256 {
		return problem(InvalidRequest, "Artifact SHA-256 must be 64 lowercase hexadecimal characters.")
	}
	if _, err = secureURL(m.URL); err != nil {
		return err
	}
	if m.Platform.OS == "any" && m.Platform.Arch == "any" && m.Kind == TranscriptionModel {
		return nil
	}
	if m.Platform.OS != runtime.GOOS || m.Platform.Arch != runtime.GOARCH {
		return problem(UnsupportedPlatform, "This pinned artifact does not support the current operating system and architecture.")
	}
	return nil
}

func secureURL(raw string) (*url.URL, error) {
	if len(raw) > 8192 || !utf8.ValidString(raw) {
		return nil, problem(InvalidRequest, "Artifact URL must be a bounded HTTPS URL.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, problem(InvalidRequest, "Artifact URL must use HTTPS without embedded credentials or a fragment.")
	}
	return u, nil
}

func (c *Cache) path(ctx context.Context, m Manifest) (string, error) {
	if ctx == nil || c == nil || c.root == "" || c.client == nil {
		return "", problem(InvalidRequest, "Artifact operations require a cache and context.")
	}
	if err := canceled(ctx); err != nil {
		return "", err
	}
	if err := validate(m); err != nil {
		return "", err
	}
	path := filepath.Join(c.root, "artifacts-v1", "sha256", m.SHA256, "blob")
	if err := checkChain(path); err != nil {
		return "", err
	}
	return path, nil
}

// Find verifies every byte on every call. No network or filesystem mutation is
// performed, including when the file is missing or damaged.
func (c *Cache) Find(ctx context.Context, m Manifest) (File, error) {
	path, err := c.path(ctx, m)
	if err != nil {
		return File{}, err
	}
	return verify(ctx, path, m)
}

func verify(ctx context.Context, path string, m Manifest) (File, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return File{}, problem(Missing, "The pinned artifact is not cached. Explicit first-use acquisition is required.")
	}
	if err != nil {
		return File{}, problem(CacheIO, "The artifact cache could not be inspected.")
	}
	if !info.Mode().IsRegular() {
		return File{}, problem(CacheUnsafe, "An artifact cache entry is not an ordinary file; it was left untouched.")
	}
	if info.Size() > m.MaxBytes {
		return File{}, problem(IntegrityFailed, "The cached artifact exceeds its pinned size bound; it was left untouched.")
	}
	file, err := os.Open(path)
	if err != nil {
		return File{}, problem(CacheIO, "The cached artifact could not be opened.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return File{}, problem(CacheUnsafe, "The artifact cache changed while it was being opened.")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(contextReader{ctx, file}, m.MaxBytes+1))
	if err != nil {
		if e := canceled(ctx); e != nil {
			return File{}, e
		}
		return File{}, problem(CacheIO, "The cached artifact could not be fully read.")
	}
	if err = canceled(ctx); err != nil {
		return File{}, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return File{}, problem(CacheUnsafe, "The artifact cache changed while it was being verified.")
	}
	if n > m.MaxBytes || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return File{}, problem(IntegrityFailed, "The cached artifact does not match its pinned hash or size bound; it was left untouched.")
	}
	return File{Path: path, SHA256: m.SHA256, Bytes: n, Reused: true}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Ensure is the only acquisition entrypoint and must be invoked deliberately at
// first use. Offline permits verified reuse only. Concurrent requests may perform
// duplicate downloads; no-clobber publication and winner verification cooperate
// across processes without a stale lock or implicit retry. No executable is run.
func (c *Cache) Ensure(ctx context.Context, m Manifest, options Options) (result File, err error) {
	path, err := c.path(ctx, m)
	if err != nil {
		return File{}, err
	}
	result, err = verify(ctx, path, m)
	if err == nil {
		return result, nil
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != Missing {
		return File{}, err
	}
	if options.Offline {
		return File{}, problem(Offline, "The pinned artifact is missing and offline acquisition is disabled.")
	}
	ctx, cancel := context.WithTimeout(ctx, acquisitionTimeout)
	defer cancel()
	if err = makePrivateDirs(filepath.Dir(path)); err != nil {
		return File{}, err
	}
	stage, err := os.CreateTemp(filepath.Dir(path), ".download-")
	if err != nil {
		return File{}, problem(CacheIO, "A private artifact download file could not be created.")
	}
	temporary := stage.Name()
	defer func() {
		_ = stage.Close()
		if removeErr := os.Remove(temporary); removeErr != nil && !os.IsNotExist(removeErr) {
			cleanup := problem(CacheIO, "The private partial download could not be removed; inspect the artifact cache before retrying.")
			err = errors.Join(err, cleanup)
		}
	}()
	if err = secureCreatedPath(temporary, false); err != nil {
		return File{}, problem(CacheIO, "The private artifact download file could not be secured.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return File{}, problem(InvalidRequest, "Artifact URL could not be used for a request.")
	}
	req.Header.Set("Accept-Encoding", "identity")
	client := *c.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return problem(AcquisitionFailed, "Artifact download exceeded its redirect bound.")
		}
		_, err := secureURL(req.URL.String())
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		if e := canceled(ctx); e != nil {
			return File{}, e
		}
		return File{}, problem(AcquisitionFailed, "Artifact download failed; no artifact was published.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return File{}, problem(AcquisitionFailed, "Artifact server returned an unsupported response; no artifact was published.")
	}
	if response.ContentLength > m.MaxBytes {
		return File{}, problem(IntegrityFailed, "Artifact download exceeds its pinned size bound; no artifact was published.")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(stage, hash), io.LimitReader(contextReader{ctx, response.Body}, m.MaxBytes+1))
	if err != nil {
		if e := canceled(ctx); e != nil {
			return File{}, e
		}
		return File{}, problem(AcquisitionFailed, "Artifact download could not be completed; no artifact was published.")
	}
	if err = canceled(ctx); err != nil {
		return File{}, err
	}
	if n > m.MaxBytes || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return File{}, problem(IntegrityFailed, "Artifact download does not match its pinned hash or size bound; no artifact was published.")
	}
	if err = stage.Sync(); err != nil {
		return File{}, problem(CacheIO, "The verified artifact could not be synced; no artifact was published.")
	}
	if err = stage.Close(); err != nil {
		return File{}, problem(CacheIO, "The verified artifact could not be closed; no artifact was published.")
	}
	if err = canceled(ctx); err != nil {
		return File{}, err
	}
	if err = checkChain(path); err != nil {
		return File{}, err
	}
	if err = os.Link(temporary, path); err != nil {
		// An existing destination is never replaced, chmodded, or deleted. A
		// matching concurrent winner is reusable only after a fresh full hash.
		winner, checkErr := verify(ctx, path, m)
		if checkErr == nil {
			return winner, nil
		}
		var typed *Error
		if errors.As(checkErr, &typed) && typed.Code == Missing {
			return File{}, problem(CacheIO, "Atomic non-overwriting publication is unavailable on this cache filesystem.")
		}
		return File{}, checkErr
	}
	result = File{Path: path, SHA256: m.SHA256, Bytes: n}
	directory, openErr := os.Open(filepath.Dir(path))
	if openErr != nil {
		result.Warnings = append(result.Warnings, "Artifact directory could not be synced; power-loss durability is unverified.")
	} else {
		syncErr := directory.Sync()
		closeErr := directory.Close()
		if syncErr != nil || closeErr != nil {
			result.Warnings = append(result.Warnings, "Artifact directory could not be synced; power-loss durability is unverified.")
		}
	}
	return result, nil
}
