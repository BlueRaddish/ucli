package mascot

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rivo/uniseg"
)

func TestFramePlaybackBoundaries(t *testing.T) {
	tests := []struct {
		name, id string
		elapsed  time.Duration
		static   bool
		ascii    bool
		text     string
		next     time.Duration
	}{
		{"negative", "wave", -time.Second, false, false, "(^.⩊ .^) ", 280 * time.Millisecond},
		{"before boundary", "wave", 280*time.Millisecond - 1, false, false, "(^.⩊ .^) ", 1},
		{"at boundary", "wave", 280 * time.Millisecond, false, false, "(^.⩊ .^)/", 180 * time.Millisecond},
		{"friendly ASCII", "wave", 280 * time.Millisecond, false, true, "(^._.^)/ ", 180 * time.Millisecond},
		{"loop wrap", "working", 2600 * time.Millisecond, false, false, "(^._.^)  ", 900 * time.Millisecond},
		{"second loop", "working", 3500 * time.Millisecond, false, false, "(^-_-^)  ", 180 * time.Millisecond},
		{"done starts", "done", 0, false, false, "(^>⩊ <^)/", 260 * time.Millisecond},
		{"done static", "done", 0, true, false, "(^~⩊ ~^) ", 0},
		{"done settled", "done", 1640 * time.Millisecond, false, false, "(^~⩊ ~^) ", 0},
		{"rest starts", "rest", 0, false, false, "(^-⩊ -^) ", 400 * time.Millisecond},
		{"rest small z", "rest", 400 * time.Millisecond, false, false, "(^-⩊ -^)z", 400 * time.Millisecond},
		{"rest big Z", "rest", 800 * time.Millisecond, false, false, "(^-⩊ -^)Z", 800 * time.Millisecond},
		{"rest settled", "rest", 2600 * time.Millisecond, false, false, "(^-⩊ -^) ", 0},
		{"rest static ASCII", "rest", time.Hour, true, true, "(^-_-^)  ", 0},
		{"peek hidden", "peek", 0, false, false, "_____    ", 350 * time.Millisecond},
		{"peek visible ASCII", "peek", 850 * time.Millisecond, false, true, "_,^,_    ", 450 * time.Millisecond},
		{"peek settled", "peek", time.Hour, false, false, "_ฅᨐฅ_    ", 0},
		{"static loop", "looking", time.Hour, true, false, "(^._.^)  ", 0},
		{"unknown", "absent", time.Hour, false, false, fallback, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, next := Frame(test.id, test.elapsed, test.static, test.ascii)
			if text != test.text || next != test.next {
				t.Fatalf("Frame = %q, %v; want %q, %v", text, next, test.text, test.next)
			}
		})
	}
}

func TestBundledFramesAndFallbacksHaveNineCells(t *testing.T) {
	data := parseCatalog(catalogJSON)
	if len(data) != 8 {
		t.Fatal("bundled catalog failed validation")
	}
	count := 0
	for id, sequence := range data {
		elapsed := time.Duration(0)
		for _, frame := range sequence.frames {
			count++
			for _, ascii := range []bool{false, true} {
				text, next := Frame(id, elapsed, false, ascii)
				want := frame.text
				if ascii {
					want = frame.ascii
					for _, r := range text {
						if r < ' ' || r > '~' {
							t.Fatalf("non-ASCII fallback %q", text)
						}
					}
				}
				if text != want || next != frame.duration || uniseg.StringWidth(text) != 9 {
					t.Fatalf("%s at %v: %q next %v, want %q next %v and nine cells", id, elapsed, text, next, want, frame.duration)
				}
			}
			elapsed += frame.duration
		}
		text, next := Frame(id, elapsed, false, false)
		want, delay := sequence.frames[sequence.staticFrame].text, time.Duration(0)
		if sequence.loop {
			want, delay = sequence.frames[0].text, sequence.frames[0].duration
		}
		if text != want || next != delay {
			t.Fatalf("%s did not loop/settle: %q %v", id, text, next)
		}
	}
	if count != 38 || uniseg.StringWidth(fallback) != 9 {
		t.Fatalf("unexpected catalog frame count %d or fallback width", count)
	}
}

func TestFriendlyMouthReservesRightBreathingRoom(t *testing.T) {
	friendly := 0
	for id, sequence := range parseCatalog(catalogJSON) {
		elapsed := time.Duration(0)
		for _, frame := range sequence.frames {
			text, _ := Frame(id, elapsed, false, false)
			if strings.ContainsRune(text, '⩊') {
				friendly++
				if !strings.Contains(text, "⩊ ") {
					t.Fatalf("%s at %v crowds the mouth with the next glyph: %q", id, elapsed, text)
				}
			}
			elapsed += frame.duration
		}
	}
	if friendly != 15 {
		t.Fatalf("checked %d friendly frames, want all 15", friendly)
	}
}

func TestInvalidCatalogFallsBackWithoutPlayback(t *testing.T) {
	mutations := map[string]func(*catalogFile){
		"schema":              func(c *catalogFile) { c.SchemaVersion++ },
		"columns":             func(c *catalogFile) { c.Columns++ },
		"missing animation":   func(c *catalogFile) { c.Animations = c.Animations[:7] },
		"duplicate ID":        func(c *catalogFile) { c.Animations[1].ID = c.Animations[0].ID },
		"unknown ID":          func(c *catalogFile) { c.Animations[0].ID = "unknown" },
		"playback":            func(c *catalogFile) { c.Animations[0].Playback = "forever" },
		"static index":        func(c *catalogFile) { c.Animations[0].StaticFrame = 99 },
		"negative static":     func(c *catalogFile) { c.Animations[0].StaticFrame = -1 },
		"empty frames":        func(c *catalogFile) { c.Animations[0].Frames = nil },
		"zero duration":       func(c *catalogFile) { c.Animations[0].Frames[0].MS = 0 },
		"large duration":      func(c *catalogFile) { c.Animations[0].Frames[0].MS = 2001 },
		"control":             func(c *catalogFile) { c.Animations[0].Frames[0].Text = "\x1b^.⩊.^) " },
		"directional control": func(c *catalogFile) { c.Animations[0].Frames[0].Text = "\u202e^.⩊.^) " },
		"legacy mouth":        func(c *catalogFile) { c.Animations[0].Frames[0].Text = "(^.ﻌ.^) " },
		"wrong mood":          func(c *catalogFile) { c.Animations[0].Frames[0].Text = "(^._.^) " },
		"rest wrong mood":     func(c *catalogFile) { c.Animations[6].Frames[0].Text = "(^-_-^) " },
		"wide glyph":          func(c *catalogFile) { c.Animations[0].Frames[0].Text = "中^.⩊.^) " },
		"peek loop":           func(c *catalogFile) { c.Animations[7].Playback = "loop" },
		"peek missing hidden": func(c *catalogFile) { c.Animations[7].Frames[0].Text = "__ᨐ__   " },
		"peek missing face":   func(c *catalogFile) { c.Animations[7].Frames[1].Text = "_____   " },
	}
	assertFallback := func(t *testing.T, raw string) {
		t.Helper()
		data := parseCatalog(raw)
		if data != nil {
			t.Fatal("invalid catalog accepted")
		}
		for _, ascii := range []bool{false, true} {
			text, next := frameAt(data["wave"], 0, false, ascii)
			if text != fallback || next != 0 {
				t.Fatalf("bad catalog did not settle safely: %q, %v", text, next)
			}
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var data catalogFile
			if err := json.Unmarshal([]byte(catalogJSON), &data); err != nil {
				t.Fatal(err)
			}
			mutate(&data)
			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			assertFallback(t, string(raw))
		})
	}
	for _, raw := range []string{"{", "null", catalogJSON + "{}", strings.Repeat(" ", 128*1024+1)} {
		assertFallback(t, raw)
	}
}

func TestFrameConcurrentReadsAndLargeElapsed(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 20 {
				text, next := Frame("working", time.Duration(1<<63-1), false, false)
				if uniseg.StringWidth(text) != 9 || next <= 0 || next > 2*time.Second {
					t.Errorf("unbounded/invalid playback: %q %v", text, next)
				}
			}
		})
	}
	wg.Wait()
}
