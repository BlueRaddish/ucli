package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowViewCommandStaysOfflineAndHelpIsDiscoverable(t *testing.T) {
	file, err := filepath.Abs(filepath.Join("..", "..", "examples", "review.workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "must-not-exist")
	var out, diagnostic bytes.Buffer
	input := &unreadBackupInput{}
	code := Run(context.Background(), []string{"workflow", "view", file, "--home", home, "--json", "--no-input"}, input, &out, &diagnostic)
	items := jsonLines(t, out.String())
	var data struct {
		Offline    bool `json:"offline"`
		Dispatched bool `json:"dispatched"`
		Spec       struct {
			ID    string            `json:"id"`
			Steps []json.RawMessage `json:"steps"`
		} `json:"spec"`
	}
	if code != 0 || input.reads != 0 || diagnostic.Len() != 0 || len(items) != 1 || items[0].Type != "workflow_view" || json.Unmarshal(items[0].Data, &data) != nil || !data.Offline || data.Dispatched || data.Spec.ID != "review-change" || len(data.Spec.Steps) != 3 {
		t.Fatalf("workflow view reply: %d %s %s reads%d", code, out.String(), diagnostic.String(), input.reads)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline view opened task home", err)
	}
	for _, topic := range []string{"workflow", "workflow view"} {
		if text := help(topic, false); !strings.Contains(text, "view") {
			t.Fatalf("view missing from %s help", topic)
		}
	}
	code, text, progress := call(t, home, "", "workflow", "view", file, "--model", "auto")
	if code != 2 || !strings.Contains(progress, "applies to run") || text != "" {
		t.Fatalf("view accepted execution flags: %d %s %s", code, text, progress)
	}
}
