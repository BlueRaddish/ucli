// Package assets lists reviewed model data and prepares it only on explicit use.
// It never starts a runtime, discovers a remote catalog, or downloads at startup.
package assets

import "ucli.local/ucli/internal/artifact"

type ModelFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Model struct {
	ID       string      `json:"id"`
	Revision string      `json:"revision"`
	Engine   string      `json:"engine"`
	Files    []ModelFile `json:"files"`
}

const tinyRevision = "0d3d19a32d3338f10357c0889762bd8d64bbdeba"

// Catalog returns independent metadata copies without filesystem or network IO.
// File identities were reviewed against the official pinned Hub revision, not a
// moving branch. Runtime acquisition/activation is a separate catalog policy.
func Catalog() []Model {
	base := "https://huggingface.co/Systran/faster-whisper-tiny.en/resolve/" + tinyRevision + "/"
	return []Model{{ID: "whisper/tiny.en", Revision: tinyRevision, Engine: "faster-whisper", Files: []ModelFile{
		{Name: "config.json", URL: base + "config.json", SHA256: "14b1b421a90349bc551b881461426b561a874049cb9e4c4864f2ca384f6a7cc5", Bytes: 2317},
		{Name: "model.bin", URL: base + "model.bin", SHA256: "1a5afae06a4db91c975c9a9d78be5cc110ee4ea022ad57d55492e4550e936b2a", Bytes: 75537502},
		{Name: "tokenizer.json", URL: base + "tokenizer.json", SHA256: "929c5252409436dce1b38a75d1abbcb5e132d170d8e324e4e04ed915fa2d22df", Bytes: 2128466},
		{Name: "vocabulary.txt", URL: base + "vocabulary.txt", SHA256: "ff77588746d3a2595d32ab5b69ffd7b95ce2441ac57533cb66fc3eb575a115cf", Bytes: 422309},
	}}}
}

func (m Model) artifact(file ModelFile) artifact.Manifest {
	return artifact.Manifest{ID: m.ID + "/" + file.Name, Version: m.Revision,
		URL: file.URL, SHA256: file.SHA256, MaxBytes: file.Bytes,
		Platform: artifact.Platform{OS: "any", Arch: "any"}, Kind: artifact.TranscriptionModel}
}
