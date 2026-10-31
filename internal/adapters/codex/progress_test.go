package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientStartedProgressPrecedesCodexProtocolDispatch(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "persist", true: "refuse"}[refuse], func(t *testing.T) {
			req := fixtureRequest(t)
			child := fixtureChild(t, req, "normal")
			marker := filepath.Join(req.Attempt.Workspace.Dir, "dispatched")
			calls := 0
			req.OnClientStarted = func() error {
				calls++
				if child.Settled() {
					t.Error("fixture was not a running owned child")
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Error("turn input preceded client-start notification")
				}
				if refuse {
					return errors.New("PRIVATE_PROGRESS_CALLBACK_DETAIL")
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output strings.Builder
			out, err := executeProtocol(ctx, req, child, func(text string) error {
				if calls != 1 {
					t.Error("output preceded the single client-start notification")
				}
				output.WriteString(text)
				return nil
			})
			if calls != 1 || !child.Settled() {
				t.Fatalf("start/cleanup contract: calls=%d settled=%v", calls, child.Settled())
			}
			if refuse {
				_, markerErr := os.Stat(marker)
				if err == nil || out.State != "failed" || output.Len() != 0 || !errors.Is(markerErr, os.ErrNotExist) || strings.Contains(err.Error()+out.Message, "PRIVATE") {
					t.Fatalf("progress failure sent/leaked input: %+v %v marker=%v", out, err, markerErr)
				}
			} else if err != nil || out.State != "complete" || output.Len() == 0 {
				t.Fatalf("progress changed successful execution: %+v %v", out, err)
			}
		})
	}
}

func TestClientStartedProgressCannotBypassCodexAdmission(t *testing.T) {
	req := fixtureRequest(t)
	called := false
	req.OnClientStarted = func() error { called = true; return nil }
	out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
	if err == nil || out.State != "failed" || called {
		t.Fatalf("refused native binding reported a child launch: %+v %v", out, err)
	}
	// Private protocol experiments cannot report a launch without a real child.
	out, err = executeProtocol(context.Background(), req, nil, func(string) error { return nil })
	if err == nil || out.State != "failed" || called {
		t.Fatalf("missing child reported a launch: %+v %v", out, err)
	}
}
