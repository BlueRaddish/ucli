package runtimes

// ReviewedNativeVersion identifies an exact reviewed protocol build. It does
// not acquire bytes, prove authentication or replace the adapter's live
// permission/configuration checks. Newer installed files never inherit review.
// Acquisition remains independently pinned by Catalog.
func ReviewedNativeVersion(id, version, digest, goos, arch string) (string, bool) {
	profiles := []struct{ id, version, digest, os, arch string }{
		{"codex", "0.162.1", "dd13bdb162a184fa04039488b234a8258ef9b76f84e926c505d5742b19249a6a", "windows", "amd64"},
		{"codex", "0.163.0", "2692c8c7bb2cf8b0aefe638741fc90236f4b824c59578ce84994f9de58200993", "windows", "amd64"},
		{"claude", "2.1.304", "019e5174d5d954585edb1a946fe280e44a973050b4897034adf0e16306f85fdd", "windows", "amd64"},
	}
	for _, p := range profiles {
		if p.id == id && p.os == goos && p.arch == arch && p.digest == digest && (version == "" || version == p.version) {
			return p.version, true
		}
	}
	// Retain the previously reviewed Claude identities and platform behavior.
	for _, p := range shipped {
		if p.ID == id && p.OS == goos && p.Arch == arch && p.SHA256 == digest && (version == "" || version == p.Version) {
			return p.Version, true
		}
	}
	return "", false
}
