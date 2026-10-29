// Package mascot plays the bundled eight-column artwork in a nine-cell slot.
// It owns no timers, terminal state, filesystem access or operation lifecycles.
package mascot

import (
	_ "embed"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

//go:embed cat-animations.json
var catalogJSON string

const fallback = "(^._.^)  "

type frameFile struct {
	Text string `json:"text"`
	MS   int    `json:"ms"`
}

type animationFile struct {
	ID, Name, Playback, Placement string
	StaticFrame                   int         `json:"static_frame"`
	Frames                        []frameFile `json:"frames"`
}

type catalogFile struct {
	SchemaVersion int             `json:"schema_version"`
	Columns       int             `json:"columns"`
	Mouth         string          `json:"mouth"`
	ASCIIMouth    string          `json:"ascii_mouth"`
	Animations    []animationFile `json:"animations"`
}

type timedFrame struct {
	text, ascii string
	duration    time.Duration
}

type sequence struct {
	frames      []timedFrame
	staticFrame int
	loop        bool
	duration    time.Duration
}

// Lazy decoding keeps import/help startup free of catalog parsing. The private
// map and slices are read-only after publication; callers receive only strings.
var catalog = sync.OnceValue(func() map[string]sequence { return parseCatalog(catalogJSON) })

// Frame returns the frame at elapsed and the delay until its next transition.
// Negative elapsed starts at zero. Static playback and finished one-shots use
// the catalog's static_frame and return zero delay. Unknown IDs or invalid
// bundled data return a static ASCII cat. No animation controls application work.
func Frame(id string, elapsed time.Duration, static, ascii bool) (text string, next time.Duration) {
	return frameAt(catalog()[id], elapsed, static, ascii)
}

func frameAt(s sequence, elapsed time.Duration, static, ascii bool) (string, time.Duration) {
	if len(s.frames) == 0 {
		return fallback, 0
	}
	selected, next := s.frames[s.staticFrame], time.Duration(0)
	if !static && (s.loop || elapsed < s.duration) {
		elapsed = max(0, elapsed)
		if s.loop {
			elapsed %= s.duration
		}
		for _, frame := range s.frames {
			if elapsed < frame.duration {
				selected, next = frame, frame.duration-elapsed
				break
			}
			elapsed -= frame.duration
		}
	}
	if ascii {
		return selected.ascii, next
	}
	return selected.text, next
}

// Validate the compiled playback/glyph boundary once. The broader authoring
// schema (including duplicate JSON keys and labels) remains checked by the
// existing check_cat_animations.py route; this is not an external asset loader.
func parseCatalog(raw string) map[string]sequence {
	if len(raw) > 128*1024 {
		return nil
	}
	var data catalogFile
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF ||
		data.SchemaVersion != 1 || data.Columns != 8 || data.Mouth != "⩊" || data.ASCIIMouth != "_" || len(data.Animations) != 8 {
		return nil
	}
	result := make(map[string]sequence, 8)
	for _, animation := range data.Animations {
		if _, duplicate := result[animation.ID]; duplicate || len(animation.Frames) < 2 || len(animation.Frames) > 64 ||
			animation.StaticFrame < 0 || animation.StaticFrame >= len(animation.Frames) ||
			(animation.Playback != "loop" && animation.Playback != "once") || (animation.ID == "peek" && animation.Playback != "once") {
			return nil
		}
		s := sequence{staticFrame: animation.StaticFrame, loop: animation.Playback == "loop"}
		for index, frame := range animation.Frames {
			if frame.MS < 120 || frame.MS > 2000 || !validFrame(animation.ID, index, frame.Text) {
				return nil
			}
			duration := time.Duration(frame.MS) * time.Millisecond
			// A font's ink can overhang its logical cell. Give the decorative
			// mouth one real blank on its right; this cannot supply missing
			// glyphs, so ASCII remains the portable fallback. Pad other frames
			// to the same nine-cell slot without changing their artwork.
			text := frame.Text + " "
			if strings.ContainsRune(frame.Text, '⩊') {
				text = strings.ReplaceAll(frame.Text, "⩊", "⩊ ")
			}
			ascii := strings.NewReplacer("⩊", "_", "ฅ", ",", "ᨐ", "^").Replace(frame.Text) + " "
			s.frames = append(s.frames, timedFrame{text, ascii, duration})
			s.duration += duration
		}
		result[animation.ID] = s
	}
	return result
}

func validFrame(id string, index int, text string) bool {
	runes := []rune(text)
	if len(runes) != 8 {
		return false
	}
	for _, r := range runes {
		if r >= ' ' && r <= '~' || r == '⩊' && id != "peek" || id == "peek" && (r == 'ฅ' || r == 'ᨐ') {
			continue
		}
		return false
	}
	switch id {
	case "wave", "done", "rest":
		return runes[3] == '⩊' && strings.Count(text, "⩊") == 1
	case "working", "looking", "paw", "listen":
		return runes[3] == '_' && !strings.ContainsRune(text, '⩊')
	case "peek":
		if index == 0 {
			return strings.Count(text, "_") == 5 && strings.Trim(text, "_ ") == ""
		}
		return strings.Count(text, "ᨐ") == 1
	default:
		return false
	}
}
