package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecutableDriftKeepsIdentityAndSeparatesCatalogEvidence(t *testing.T) {
	s, o, _ := fixture(t)
	o.Provider = "claude"
	c, err := s.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Executable, []byte("updated executable fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	newSHA, err := executableHash(context.Background(), o.Executable)
	if err != nil {
		t.Fatal(err)
	}
	s.inspect = func(context.Context, Connection) (Connection, error) {
		t.Fatal("drift ran auth/execution inspection")
		return Connection{}, nil
	}
	s.discover = func(_ context.Context, candidate Connection) ([]Model, error) {
		if candidate.ID != c.ID || candidate.SHA256 != newSHA || candidate.Version != "" || candidate.Identity != c.Identity {
			t.Fatal("wrong discovery binding")
		}
		return []Model{{ID: "fresh-model", Name: "Fresh model", ResolvedID: "claude-fresh", EffortState: "supported", Efforts: []string{"low"}}}, nil
	}
	updated, err := s.Status(context.Background(), c.ID)
	requireCode(t, err, "connection_changed")
	if updated.ID != c.ID || updated.Identity != c.Identity || updated.SHA256 != c.SHA256 || updated.Version != c.Version || updated.AuthState != c.AuthState || updated.Compatibility != "unknown" || updated.CatalogSHA256 != newSHA || updated.Models[0].ID != "fresh-model" {
		t.Fatalf("pin or metadata lost: %+v", updated)
	}
	s.discover = func(context.Context, Connection) ([]Model, error) {
		return nil, problem("connection_catalog", "Synthetic invalid catalog.")
	}
	failed, err := s.Status(context.Background(), c.ID)
	requireCode(t, err, "connection_catalog")
	if !reflect.DeepEqual(failed.Models, updated.Models) || failed.CatalogSHA256 != newSHA || failed.Identity != c.Identity || !failed.ObservedAt.Equal(updated.ObservedAt) {
		t.Fatal("failed refresh destroyed last good metadata")
	}
	reopened, err := New(s.home)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := reopened.Get(context.Background(), c.ID)
	if err != nil || !reflect.DeepEqual(cached, failed) {
		t.Fatal("offline reopen lost compatibility state", err)
	}
	if err = verifyExecutable(context.Background(), cached); err == nil {
		t.Fatal("discovery silently replaced execution pin")
	}
}

func TestCatalogFailureCanPersistAuthWithoutLosingLastGoodModels(t *testing.T) {
	s, o, _ := fixture(t)
	c, err := s.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		c.Compatibility = "unknown"
		return c, problem("connection_catalog", "Synthetic metadata failure.")
	}
	updated, err := s.Status(context.Background(), c.ID)
	requireCode(t, err, "connection_catalog")
	if updated.AuthState != "authenticated" || updated.Identity != c.Identity || !reflect.DeepEqual(updated.Models, c.Models) {
		t.Fatal("catalog failure became logout")
	}
	cached, err := s.Get(context.Background(), c.ID)
	if err != nil || cached.Compatibility != "unknown" {
		t.Fatal("failure was not durably visible", err)
	}
	if _, err = s.Login(context.Background(), c.ID, func(LoginEvent) error { t.Fatal("login started for catalog failure"); return nil }); err == nil {
		t.Fatal("unknown compatibility allowed login")
	}
}

func TestConnectionFormatOneReadAndFormatTwoAppend(t *testing.T) {
	s, o, _ := fixture(t)
	sha, err := executableHash(context.Background(), o.Executable)
	if err != nil {
		t.Fatal(err)
	}
	c := Connection{ID: "connection_" + strings.Repeat("a", 32), Label: "legacy", Provider: o.Provider, Executable: o.Executable, SHA256: sha, NativeHome: o.NativeHome, HomeMode: "existing", AuthState: "authenticated", AuthMode: "chatgpt", Identity: strings.Repeat("b", 64), Isolation: "unverified", ObservedAt: time.Now().UTC()}
	r := record{Format: 1, Revision: 1, Connection: c}
	if !validRecord(r) {
		t.Fatal("legacy fixture invalid")
	}
	if err = privateDirs(s.directory); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(r)
	sum := sha256.Sum256(body)
	raw, _ := json.Marshal(envelope{r, hex.EncodeToString(sum[:])})
	path := join(s.directory, recordName(r))
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(context.Background(), c.ID); err != nil {
		t.Fatal("legacy format refused", err)
	}
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		c.Compatibility = "reviewed"
		c.CatalogSHA256 = c.SHA256
		c.Models = []Model{{ID: "new", Name: "New", ResolvedID: "claude-new", EffortState: "supported", Efforts: []string{"low"}}}
		return c, nil
	}
	if _, err = s.Status(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path)
	if err != nil || string(old) != string(raw) {
		t.Fatal("legacy history rewritten")
	}
	latest, err := s.records(context.Background())
	if err != nil || len(latest) != 1 || latest[0].Format != 2 || latest[0].Revision != 2 {
		t.Fatal("extended append missing", err)
	}
	// The previous reader's explicit format == 1 guard must refuse new metadata.
	if latest[0].Format == 1 {
		t.Fatal("older reader could ignore extended evidence")
	}
	bad := latest[0]
	bad.Format = 1
	if validRecord(bad) {
		t.Fatal("extended fields smuggled into format1")
	}
	for _, mutate := range []func(*record){func(r *record) { r.Connection.Compatibility = "yes" }, func(r *record) { r.Connection.CatalogSHA256 = "bad" }, func(r *record) { r.Connection.Models[0].EffortState = "unsupported" }, func(r *record) { r.Connection.Models[0].ResolvedID = "bad\nname" }} {
		bad = latest[0]
		bad.Connection.Models = append([]Model{}, bad.Connection.Models...)
		mutate(&bad)
		if validRecord(bad) {
			t.Fatal("invalid extended metadata accepted")
		}
	}
}

func TestAGYUpdateUsesNewFingerprintForMetadataOnly(t *testing.T) {
	s, o, _ := fixture(t)
	o.Provider = "agy"
	c, err := s.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Executable, []byte("updated AGY fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	sha, err := executableHash(context.Background(), o.Executable)
	if err != nil {
		t.Fatal(err)
	}
	s.discover = func(_ context.Context, candidate Connection) ([]Model, error) {
		if candidate.SHA256 != sha || candidate.SHA256 == c.SHA256 {
			t.Fatal("wrong metadata pin")
		}
		return []Model{{ID: "future-agy", Name: "New native AGY model"}}, nil
	}
	updated, err := s.Status(context.Background(), c.ID)
	requireCode(t, err, "connection_changed")
	if updated.ID != c.ID || updated.SHA256 != c.SHA256 || updated.CatalogSHA256 != sha || updated.Models[0].ID != "future-agy" || updated.Compatibility != "unknown" {
		t.Fatal("AGY metadata replaced execution pin")
	}
}
