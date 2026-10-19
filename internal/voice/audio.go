package voice

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ValidateAudio checks local WAV metadata before setup can acquire dependencies
// or model bytes. It reads bounded headers, never imports an audio decoder.
func ValidateAudio(path string, duration time.Duration) error {
	if duration == 0 {
		duration = MaxDuration
	}
	if duration < time.Second || duration > MaxDuration {
		return problem("voice_config", "Recording duration must be between 1 and 120 seconds.")
	}
	invalid := func() error {
		return problem("voice_audio", "Use a regular 16 kHz mono PCM16 WAV within the recording duration limit and 8 MiB size bound.")
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return invalid()
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 44 || info.Size() > 8<<20 {
		return invalid()
	}
	file, err := os.Open(path)
	if err != nil {
		return invalid()
	}
	defer file.Close()
	var header [12]byte
	if _, err = file.ReadAt(header[:], 0); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" || int64(binary.LittleEndian.Uint32(header[4:8]))+8 != info.Size() {
		return invalid()
	}
	foundFormat, foundData := false, false
	for offset, chunks := int64(12), 0; offset < info.Size(); chunks++ {
		if chunks >= 1024 { // Bound metadata scanning independently of file size.
			return invalid()
		}
		var chunk [8]byte
		if _, err = file.ReadAt(chunk[:], offset); err != nil {
			return invalid()
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		start, end := offset+8, offset+8+size
		if end+(size&1) > info.Size() {
			return invalid()
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if foundFormat || foundData || size < 16 || size > 64 {
				return invalid()
			}
			var format [16]byte
			if _, err = file.ReadAt(format[:], start); err != nil || binary.LittleEndian.Uint16(format[:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint32(format[4:8]) != 16000 || binary.LittleEndian.Uint32(format[8:12]) != 32000 || binary.LittleEndian.Uint16(format[12:14]) != 2 || binary.LittleEndian.Uint16(format[14:]) != 16 {
				return invalid()
			}
			foundFormat = true
		case "data":
			if !foundFormat || foundData || size == 0 || size%2 != 0 || float64(size)/32000 > duration.Seconds() {
				return invalid()
			}
			foundData = true
		}
		offset = end + (size & 1)
	}
	if !foundFormat || !foundData {
		return invalid()
	}
	return nil
}
