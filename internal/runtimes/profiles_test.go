package runtimes

import (
	"strings"
	"testing"
)

func TestReviewedNativeProfilesAreExactAndDoNotChangeAcquisition(t *testing.T) {
	profiles := []struct{ id, version, digest string }{
		{"codex", "0.162.1", "dd13bdb162a184fa04039488b234a8258ef9b76f84e926c505d5742b19249a6a"},
		{"codex", "0.163.0", "2692c8c7bb2cf8b0aefe638741fc90236f4b824c59578ce84994f9de58200993"},
		{"claude", "2.1.299", "295b49ec092f31a7de9c6ff286d9d8f120a4f029b9ad9a5ac0b12c35cbf4df2b"},
		{"claude", "2.1.304", "019e5174d5d954585edb1a946fe280e44a973050b4897034adf0e16306f85fdd"},
	}
	for _, p := range profiles {
		t.Run(p.id+"/"+p.version, func(t *testing.T) {
			for _, version := range []string{p.version, ""} {
				got, ok := ReviewedNativeVersion(p.id, version, p.digest, "windows", "amd64")
				if !ok || got != p.version {
					t.Fatalf("exact profile lost: %q %v", got, ok)
				}
			}
			for _, v := range []struct{ id, version, digest, os, arch string }{
				{p.id, "unreviewed", p.digest, "windows", "amd64"},
				{p.id, p.version, strings.Repeat("0", 64), "windows", "amd64"},
				{p.id, p.version, p.digest, "linux", "amd64"},
				{p.id, p.version, p.digest, "windows", "arm64"},
				{"foreign", p.version, p.digest, "windows", "amd64"},
			} {
				if _, ok := ReviewedNativeVersion(v.id, v.version, v.digest, v.os, v.arch); ok {
					t.Fatal("unreviewed combination accepted", v)
				}
			}
		})
	}
	for _, p := range shipped {
		if got, ok := ReviewedNativeVersion(p.ID, p.Version, p.SHA256, p.OS, p.Arch); !ok || got != p.Version {
			t.Fatal("previous profile removed")
		}
	}
	for _, p := range Catalog() {
		if p.ID != "claude" || p.Version != "2.1.299" {
			t.Fatal("protocol review changed acquisition", p)
		}
	}
}
