package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// A separate opt-in keeps functional suites out of the timing window. Samples
// measure bytes arriving from real ConPTY, not a physical monitor or user study.
func TestPackagedConPTYRichTiming(t *testing.T) {
	if os.Getenv("UCLI_CONPTY_TIMING") != "1" {
		t.Skip("set UCLI_CONPTY_TIMING=1 for 30 isolated packaged UI trials")
	}
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Fatal("set UCLI_CONPTY_BINARY to a verified packaged executable")
	}
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	original, err := a.Draft(context.Background(), "", "Small synthetic timing draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	samples := map[string][]float64{"first_frame": {}, "open_models": {}, "filter_models": {}, "cancel_to_composer": {}}
	for index := range 30 {
		if !t.Run(fmt.Sprintf("trial_%02d", index+1), func(t *testing.T) {
			p := startConPTYOptions(t, binary, home, cwd, false)
			measure := func(mark int, needle, kind string) {
				p.await(t, mark, needle)
				last := p.observations[len(p.observations)-1]
				samples[kind] = append(samples[kind], last["elapsed_ms"].(float64))
			}
			measure(0, "? shortcuts", "first_frame")
			mark := p.send(t, "\x15/models\r")
			measure(mark, "Choose a CLI", "open_models")
			// Enter the demo model layer before measuring its local filter.
			mark = p.send(t, "Local demo\r")
			p.await(t, mark, "Choose a model")
			mark = p.send(t, "deep")
			// Differential redraw can retain the Search label. Time the new
			// filtered candidate, which proves the list has responded to input.
			measure(mark, "demo/deep | available in cached catalog", "filter_models")
			mark = p.send(t, "\x1b")
			p.await(t, mark, "Choose a CLI")
			mark = p.send(t, "\x1b")
			measure(mark, original.Draft, "cancel_to_composer")
			p.send(t, "\x15/exit\r")
			if code := p.wait(t); code != 0 {
				t.Fatalf("timing child exit%d", code)
			}
		}) {
			t.Fatal("timing trial failed; incomplete samples are not a pass")
		}
	}
	results := map[string]map[string]any{}
	for name, values := range samples {
		ordered := append([]float64(nil), values...)
		sort.Float64s(ordered)
		results[name] = map[string]any{"n": len(ordered), "p50_ms": ordered[(50*len(ordered)+99)/100-1], "p95_ms": ordered[(95*len(ordered)+99)/100-1], "max_ms": ordered[len(ordered)-1]}
	}
	a, err = app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	task, attempts, err := a.Task(context.Background(), original.ID)
	if err != nil || task.Revision != original.Revision || task.Draft != original.Draft || len(attempts) != 0 {
		t.Fatalf("timing changed task: %+v %+v %v", task, attempts, err)
	}
	receipt := map[string]any{"scope": "30 cold UCLI processes; primed filesystem; small synthetic task and actual local catalog; Windows ConPTY output bytes; no inference, credentials, warm supervisor, physical paint or native parity claim", "results": results, "samples": samples, "percentiles": "nearest rank", "exit_code": 0}
	if evidence := os.Getenv("UCLI_CONPTY_EVIDENCE"); evidence != "" {
		data, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidence, "rich-timing.json"), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(results)
	t.Log(string(data))
}
