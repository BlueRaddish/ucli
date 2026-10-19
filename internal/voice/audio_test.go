package voice

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudioPreflightBeforeRuntimeOrModelSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.wav")
	writeWave(t, path, 16000, 1600)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateAudio(path, time.Second); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"mp3":            func(data []byte) []byte { copy(data, "ID3"); return data },
		"stereo":         func(data []byte) []byte { binary.LittleEndian.PutUint16(data[22:], 2); return data },
		"rate":           func(data []byte) []byte { binary.LittleEndian.PutUint32(data[24:], 44100); return data },
		"float":          func(data []byte) []byte { binary.LittleEndian.PutUint16(data[20:], 3); return data },
		"truncated":      func(data []byte) []byte { return data[:len(data)-1] },
		"chunk-overflow": func(data []byte) []byte { binary.LittleEndian.PutUint32(data[40:], ^uint32(0)); return data },
		"missing-format": func(data []byte) []byte { copy(data[12:], "JUNK"); return data },
		"odd-frames":     func(data []byte) []byte { binary.LittleEndian.PutUint32(data[40:], 3199); return data },
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, mutate(append([]byte(nil), original...)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateAudio(path, time.Second); err == nil {
				t.Fatal("unsupported audio accepted before setup")
			}
		})
	}
	writeWave(t, path, 16000, 16001)
	if err := ValidateAudio(path, time.Second); err == nil {
		t.Fatal("over-duration audio accepted")
	}
	if err := ValidateAudio(path+".missing", 0); err == nil {
		t.Fatal("missing audio accepted")
	}
	// RIFF metadata chunks are legal and do not change the audio duration.
	withMetadata := append(append(append([]byte{}, original[:36]...), []byte("JUNK\x02\x00\x00\x00ab")...), original[36:]...)
	binary.LittleEndian.PutUint32(withMetadata[4:], uint32(len(withMetadata)-8))
	if err := os.WriteFile(path, withMetadata, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAudio(path, time.Second); err != nil {
		t.Fatalf("bounded metadata chunk: %v", err)
	}
}
