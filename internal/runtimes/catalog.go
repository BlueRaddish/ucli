// Package runtimes describes reviewed native artifacts. Discovery is local;
// Prepare is called only for an explicit first-use/setup request. Acquiring bytes
// does not admit an AI adapter, start a login, or execute a native program.
package runtimes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"ucli.local/ucli/internal/artifact"
)

type Entry struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	URL          string `json:"url"`
	Requirements string `json:"requirements,omitempty"`
}

type Prepared struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Path      string   `json:"path"`
	SHA256    string   `json:"sha256"`
	Source    string   `json:"source"`
	Bytes     int64    `json:"bytes"`
	Executed  bool     `json:"executed"`
	ManagedAI bool     `json:"managed_ai"`
	Warnings  []string `json:"warnings,omitempty"`
}

// These hashes come from the official 2.1.299 manifest, verified against
// Anthropic's documented GPG fingerprint. The Windows bytes also match the
// locally audited executable. Other entries identify downloads, not host support.
var shipped = []Entry{
	{"claude", "2.1.299", "windows", "amd64", 269231264, "295b49ec092f31a7de9c6ff286d9d8f120a4f029b9ad9a5ac0b12c35cbf4df2b", "https://downloads.claude.ai/claude-code-releases/2.1.299/win32-x64/claude.exe", ""},
	{"claude", "2.1.299", "windows", "arm64", 255381152, "a34fe98210ae38e55543e094036fea698db93bb4bba458c63d560b19ed2a56a2", "https://downloads.claude.ai/claude-code-releases/2.1.299/win32-arm64/claude.exe", ""},
	{"claude", "2.1.299", "darwin", "arm64", 247931696, "ba5ab7b8c195fa03ab8d5bf3915c76153b576a2c40e79a5c46e265e7ed9744b3", "https://downloads.claude.ai/claude-code-releases/2.1.299/darwin-arm64/claude", "macOS 13+; native execution unverified"},
	{"claude", "2.1.299", "darwin", "amd64", 257765072, "099dcc53dabbbefabed8926dab25b0318775c175a7c87bb6ba6c54fde9f8ece2", "https://downloads.claude.ai/claude-code-releases/2.1.299/darwin-x64/claude", "macOS 13+; native execution unverified"},
	{"claude", "2.1.299", "linux", "amd64", 265342136, "2d894a4b1bfaaa9aab72aeff7803e3c73bc2510f279a171f6b8f59f72d17d077", "https://downloads.claude.ai/claude-code-releases/2.1.299/linux-x64/claude", "glibc distribution; native execution unverified"},
	{"claude", "2.1.299", "linux", "arm64", 264626168, "4cd5ab006b49dc12a247fed4dbc42a925ad1aea1e711d9aa24587d285ee87c2f", "https://downloads.claude.ai/claude-code-releases/2.1.299/linux-arm64/claude", "glibc distribution; native execution unverified"},
}

// Catalog never checks PATH, starts a child, opens a cache, or uses the network.
func Catalog() []Entry { return append([]Entry(nil), shipped...) }

type cache interface {
	Ensure(context.Context, artifact.Manifest, artifact.Options) (artifact.File, error)
}

// Prepare prefers explicitly discovered existing bytes with the exact reviewed
// hash, then uses the private artifact cache. A different existing version stays
// untouched. No shell installer, global PATH update, chmod or launch occurs.
func Prepare(ctx context.Context, cacheRoot, id, existing string, offline bool) (Prepared, error) {
	var selected *Entry
	for _, entry := range shipped {
		if entry.ID == id && entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
			copy := entry
			selected = &copy
			break
		}
	}
	if selected == nil {
		return Prepared{}, fmt.Errorf("no reviewed native download for %q on %s/%s", id, runtime.GOOS, runtime.GOARCH)
	}
	c, err := artifact.New(cacheRoot)
	if err != nil {
		return Prepared{}, err
	}
	return prepare(ctx, c, *selected, existing, offline)
}

func prepare(ctx context.Context, c cache, entry Entry, existing string, offline bool) (Prepared, error) {
	if ctx == nil {
		return Prepared{}, errors.New("runtime preparation needs a context")
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	result := Prepared{ID: entry.ID, Version: entry.Version, SHA256: entry.SHA256, Bytes: entry.Bytes}
	if existing != "" {
		if !filepath.IsAbs(existing) {
			return Prepared{}, errors.New("existing native runtime path must be absolute")
		}
		ok, err := matches(ctx, existing, entry)
		if err != nil {
			return Prepared{}, err
		}
		if ok {
			result.Path = existing
			result.Source = "existing"
			return result, nil
		}
		result.Warnings = append(result.Warnings, "Existing native bytes differ from the reviewed version and were left unchanged.")
	}
	file, err := c.Ensure(ctx, artifact.Manifest{ID: entry.ID, Version: entry.Version, URL: entry.URL, SHA256: entry.SHA256, MaxBytes: entry.Bytes,
		Platform: artifact.Platform{OS: entry.OS, Arch: entry.Arch}, Kind: artifact.Runtime}, artifact.Options{Offline: offline})
	if err != nil {
		return Prepared{}, err
	}
	if file.Bytes != entry.Bytes || file.SHA256 != entry.SHA256 {
		return Prepared{}, errors.New("prepared native artifact differs from its reviewed identity")
	}
	result.Path = file.Path
	result.Source = "downloaded"
	if file.Reused {
		result.Source = "cache"
	}
	result.Warnings = append(result.Warnings, file.Warnings...)
	return result, nil
}

func matches(ctx context.Context, path string, entry Entry) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Bytes {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	digest := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err := f.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)) == entry.SHA256, nil
}
