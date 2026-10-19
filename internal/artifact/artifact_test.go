package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func manifest(address string, data []byte) Manifest {
	sum := sha256.Sum256(data)
	return Manifest{ID: "synthetic-runtime", Version: "1.0.0", URL: address, SHA256: hex.EncodeToString(sum[:]), MaxBytes: int64(len(data)), Platform: Platform{runtime.GOOS, runtime.GOARCH}, Kind: Runtime}
}
func testCache(t *testing.T, server *httptest.Server) *Cache {
	t.Helper()
	c, err := New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if server != nil {
		c.client = server.Client()
	}
	return c
}
func code(t *testing.T, err error, want Code) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("expected %s, got %v", want, err)
	}
}
func noPartial(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".download-") {
			t.Errorf("partial download retained: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLazyVerifiedReuseAndTamperPreservation(t *testing.T) {
	payload := []byte("synthetic executable bytes, never executed")
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(payload) }))
	defer server.Close()
	c := testCache(t, server)
	m := manifest(server.URL, payload)
	if _, err := os.Stat(c.root); !os.IsNotExist(err) {
		t.Fatal("New created a cache")
	}
	_, err := c.Find(context.Background(), m)
	code(t, err, Missing)
	_, err = c.Ensure(context.Background(), m, Options{Offline: true})
	code(t, err, Offline)
	if requests.Load() != 0 {
		t.Fatal("lookup/offline acquisition contacted server")
	}
	if _, err := os.Stat(c.root); !os.IsNotExist(err) {
		t.Fatal("lookup/offline acquisition created directories")
	}
	file, err := c.Ensure(context.Background(), m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if file.Reused || file.Bytes != int64(len(payload)) || file.SHA256 != m.SHA256 {
		t.Fatalf("wrong publication %+v", file)
	}
	for range 2 {
		again, err := c.Ensure(context.Background(), m, Options{Offline: true})
		if err != nil || !again.Reused || again.Path != file.Path {
			t.Fatalf("reuse %+v %v", again, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("verified reuse downloaded again")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(file.Path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("artifact permissions %v", info.Mode())
		}
	}
	foreign := []byte("foreign")
	if err := os.WriteFile(file.Path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.Ensure(context.Background(), m, Options{})
	code(t, err, IntegrityFailed)
	actual, _ := os.ReadFile(file.Path)
	if !bytes.Equal(actual, foreign) || requests.Load() != 1 {
		t.Fatal("damaged existing artifact replaced or downloaded")
	}
	noPartial(t, c.root)
}

func TestDownloadNegativeControls(t *testing.T) {
	good := []byte("pinned payload")
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    Code
	}{
		{"hash", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("wrong payload")) }, IntegrityFailed},
		{"declared-size", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "9999")
			w.WriteHeader(200)
		}, IntegrityFailed},
		{"streamed-size", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			w.Write(bytes.Repeat([]byte("x"), 128))
		}, IntegrityFailed},
		{"partial", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(len(good)))
			w.Write([]byte("pin"))
		}, AcquisitionFailed},
		{"status", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server private details", http.StatusForbidden)
		}, AcquisitionFailed},
		{"encoding", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(good)
		}, AcquisitionFailed},
		{"redirect-loop", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/again?private-signed-token", http.StatusFound)
		}, AcquisitionFailed},
		{"redirect-http", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/private-signed-token", http.StatusFound)
		}, AcquisitionFailed},
		{"redirect-userinfo", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://private-user:private-secret@127.0.0.1:1/", http.StatusFound)
		}, AcquisitionFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tc.handler)
			defer server.Close()
			c := testCache(t, server)
			m := manifest(server.URL+"/?private-original-token", good)
			_, err := c.Ensure(context.Background(), m, Options{})
			code(t, err, tc.want)
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("sensitive URL/body leaked in error", err)
			}
			_, err = c.Find(context.Background(), m)
			code(t, err, Missing)
			noPartial(t, c.root)
		})
	}
}

func TestSecureRedirectAndOrdinaryTLSVerification(t *testing.T) {
	payload := []byte("model-data")
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer destination.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/?signed-query", http.StatusFound)
	}))
	defer origin.Close()
	// httptest TLS servers use the same test-only certificate. Only the private
	// client field is injected; the production constructor trusts system roots.
	c := testCache(t, origin)
	m := manifest(origin.URL, payload)
	m.Kind = TranscriptionModel
	m.Platform = Platform{"any", "any"}
	if _, err := c.Ensure(context.Background(), m, Options{}); err != nil {
		t.Fatal(err)
	}
	untrusted := testCache(t, nil)
	_, err := untrusted.Ensure(context.Background(), manifest(destination.URL, payload), Options{})
	code(t, err, AcquisitionFailed)
	noPartial(t, untrusted.root)
}

func TestCancellationCleansPartialAndPreservesContext(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := testCache(t, server)
	m := manifest(server.URL, []byte("partial with a much longer verified suffix"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := c.Ensure(ctx, m, Options{}); finished <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled acquisition hung")
	}
	_, err := c.Find(context.Background(), m)
	code(t, err, Missing)
	noPartial(t, c.root)
	_, err = c.Ensure(ctx, m, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("already canceled context lost", err)
	}
}

func TestConcurrentCachesVerifyWinnerAndDoNotClobberForeignFile(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint("foreign-", foreign), func(t *testing.T) {
			payload := []byte("cooperating requests share exactly these pinned bytes")
			var requests atomic.Int32
			release := make(chan struct{})
			allStarted := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 2 {
					close(allStarted)
				}
				select {
				case <-release:
					w.Write(payload)
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			first := testCache(t, server)
			second, err := New(first.root)
			if err != nil {
				t.Fatal(err)
			}
			second.client = server.Client()
			m := manifest(server.URL, payload)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var files [2]File
			var errs [2]error
			var wg sync.WaitGroup
			for i, c := range []*Cache{first, second} {
				wg.Add(1)
				go func() { defer wg.Done(); files[i], errs[i] = c.Ensure(ctx, m, Options{}) }()
			}
			select {
			case <-allStarted:
			case <-ctx.Done():
				close(release)
				wg.Wait()
				t.Fatalf("both downloads did not start: %v", errs)
			}
			path := filepath.Join(first.root, "artifacts-v1", "sha256", m.SHA256, "blob")
			if foreign {
				if err = os.WriteFile(path, []byte("foreign sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			wg.Wait()
			if foreign {
				for _, err := range errs {
					code(t, err, IntegrityFailed)
				}
				actual, _ := os.ReadFile(path)
				if string(actual) != "foreign sentinel" {
					t.Fatal("foreign winner overwritten")
				}
			} else {
				if errs[0] != nil || errs[1] != nil || files[0].Path != files[1].Path || files[0].Reused == files[1].Reused {
					t.Fatalf("publication race %+v %v", files, errs)
				}
				actual, _ := os.ReadFile(path)
				if !bytes.Equal(actual, payload) {
					t.Fatal("incomplete winner")
				}
			}
			noPartial(t, first.root)
		})
	}
}

func TestManifestAndPathGuardsBeforeAcquisition(t *testing.T) {
	c := testCache(t, nil)
	m := manifest("https://example.invalid/pinned", []byte("data"))
	for name, mutate := range map[string]func(*Manifest){
		"hash": func(m *Manifest) { m.SHA256 = "../escape" }, "uppercase-hash": func(m *Manifest) { m.SHA256 = strings.ToUpper(m.SHA256) },
		"empty-id": func(m *Manifest) { m.ID = "" }, "version": func(m *Manifest) { m.Version = "x\ny" },
		"http": func(m *Manifest) { m.URL = "http://127.0.0.1:1/file" }, "credentials": func(m *Manifest) { m.URL = "https://secret:token@example.invalid/file" },
		"fragment": func(m *Manifest) { m.URL += "#ignored" }, "size": func(m *Manifest) { m.MaxBytes = 0 }, "huge-size": func(m *Manifest) { m.MaxBytes = MaxArtifactBytes + 1 },
		"kind": func(m *Manifest) { m.Kind = "package-manager" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := m
			mutate(&invalid)
			_, err := c.Ensure(context.Background(), invalid, Options{})
			code(t, err, InvalidRequest)
		})
	}
	m.Platform = Platform{"any", "any"}
	_, err := c.Find(context.Background(), m)
	code(t, err, UnsupportedPlatform)
	if _, err = os.Stat(c.root); !os.IsNotExist(err) {
		t.Fatal("invalid manifest created cache")
	}
	for _, path := range []string{"", "relative", `\\server\share`, "https://example.invalid/cache"} {
		_, err := New(path)
		code(t, err, InvalidRequest)
	}
	_, err = c.Ensure(nil, m, Options{})
	code(t, err, InvalidRequest)
}

func TestForeignPathsAndLinkedEntriesStayUntouched(t *testing.T) {
	c := testCache(t, nil)
	m := manifest("https://example.invalid/pinned", []byte("data"))
	if err := os.MkdirAll(c.root, 0700); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(c.root, "artifacts-v1")
	if err := os.WriteFile(occupied, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := c.Ensure(context.Background(), m, Options{})
	code(t, err, CacheUnsafe)
	actual, _ := os.ReadFile(occupied)
	if string(actual) != "foreign" {
		t.Fatal("foreign parent changed")
	}
	t.Run("linked-entry", func(t *testing.T) {
		c = testCache(t, nil)
		target := filepath.Join(t.TempDir(), "target")
		if err = os.WriteFile(target, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(c.root, "artifacts-v1", "sha256", m.SHA256, "blob")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(target, path); err != nil {
			t.Skipf("Host cannot create symlink negative control: %v", err)
		}
		_, err = c.Ensure(context.Background(), m, Options{})
		code(t, err, CacheUnsafe)
		actual, _ = os.ReadFile(target)
		if string(actual) != "data" {
			t.Fatal("linked target changed")
		}
	})
}
