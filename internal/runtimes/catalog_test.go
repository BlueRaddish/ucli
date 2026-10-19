package runtimes

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ucli.local/ucli/internal/artifact"
)

type fakeCache struct {
	calls    int
	offline  bool
	manifest artifact.Manifest
	file     artifact.File
	err      error
}

func (c *fakeCache) Ensure(_ context.Context, m artifact.Manifest, o artifact.Options) (artifact.File, error) {
	c.calls++
	c.manifest = m
	c.offline = o.Offline
	return c.file, c.err
}

func TestCatalogIsMetadataOnlyAndIndependent(t *testing.T) {
	entries := Catalog()
	if len(entries) != 6 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if entry.ID != "claude" || entry.Version != "2.1.299" || len(entry.SHA256) != 64 || entry.Bytes < 1 || entry.URL == "" {
			t.Fatal(entry)
		}
	}
	entries[0].SHA256 = "changed"
	if Catalog()[0].SHA256 == "changed" {
		t.Fatal("caller mutated the shipped catalog")
	}
}

func TestPrepareReusesExactExistingBytesWithoutAcquisition(t *testing.T) {
	content := []byte("synthetic native bytes, never executed")
	entry := Entry{ID: "fixture", Version: "1", SHA256: fmt.Sprintf("%x", sha256.Sum256(content)), Bytes: int64(len(content))}
	path := filepath.Join(t.TempDir(), "existing.exe")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	c := &fakeCache{}
	prepared, err := prepare(context.Background(), c, entry, path, true)
	if err != nil || c.calls != 0 || prepared.Path != path || prepared.Source != "existing" || prepared.Executed || prepared.ManagedAI {
		t.Fatalf("%+v %v calls%d", prepared, err, c.calls)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(content) {
		t.Fatal("existing runtime changed")
	}
}

func TestPrepareUsesOnlyReviewedManifestAndPreservesForeignBytes(t *testing.T) {
	entry := Catalog()[0]
	path := filepath.Join(t.TempDir(), "foreign.exe")
	if err := os.WriteFile(path, []byte("foreign executable"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &fakeCache{file: artifact.File{Path: "verified-fixture", SHA256: entry.SHA256, Bytes: entry.Bytes, Reused: true}}
	prepared, err := prepare(context.Background(), c, entry, path, true)
	if err != nil || c.calls != 1 || !c.offline || c.manifest.URL != entry.URL || c.manifest.SHA256 != entry.SHA256 || c.manifest.Kind != artifact.Runtime || c.manifest.MaxBytes != entry.Bytes || prepared.Source != "cache" || len(prepared.Warnings) != 1 {
		t.Fatalf("%+v %+v %v", prepared, c, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "foreign executable" {
		t.Fatal("foreign runtime changed")
	}
	c.file.Bytes--
	if _, err := prepare(context.Background(), c, entry, "", false); err == nil {
		t.Fatal("wrong cached size accepted")
	}
}

func TestPrepareCancellationAndPathsHaveNoSideEffects(t *testing.T) {
	c := &fakeCache{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepare(ctx, c, Catalog()[0], "", false); err != context.Canceled || c.calls != 0 {
		t.Fatalf("%v %d", err, c.calls)
	}
	if _, err := prepare(context.Background(), c, Catalog()[0], "relative.exe", false); err == nil || c.calls != 0 {
		t.Fatalf("%v %d", err, c.calls)
	}
}
