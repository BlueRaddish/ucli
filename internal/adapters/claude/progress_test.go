package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/process"
)

// Only the synthetic child calls this from TestMain. Its private marker lets a
// consuming test prove launch and cleanup without examining unrelated processes.
func recordClientProgressFixturePID() {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if _, err := os.Stat(filepath.Join(home, "observe-client-start")); home != "" && err == nil {
		_ = os.WriteFile(filepath.Join(home, "client-start-pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	}
}

func TestClientStartedProgressPrecedesClaudeInputAndCleansFailure(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "persist", true: "refuse"}[refuse], func(t *testing.T) {
			req := fixtureRequest(t, "complete", "read-only")
			if err := os.WriteFile(filepath.Join(req.StateHome, "observe-client-start"), []byte("synthetic only"), 0600); err != nil {
				t.Fatal(err)
			}
			calls, pid := 0, 0
			req.OnClientStarted = func() error {
				calls++
				deadline := time.Now().Add(3 * time.Second)
				for pid == 0 && time.Now().Before(deadline) {
					if raw, err := os.ReadFile(filepath.Join(req.StateHome, "client-start-pid")); err == nil {
						pid, _ = strconv.Atoi(string(raw))
					}
					if pid == 0 {
						time.Sleep(5 * time.Millisecond)
					}
				}
				if alive, err := process.Alive(pid); err != nil || !alive {
					t.Errorf("client-start callback lacks live owned fixture: %v", err)
				}
				if _, err := os.Stat(filepath.Join(req.StateHome, "context-receipt.dispatched")); !errors.Is(err, os.ErrNotExist) {
					t.Error("task input preceded the client-start callback")
				}
				if refuse {
					return errors.New("PRIVATE_PROGRESS_CALLBACK_DETAIL")
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var output strings.Builder
			out, err := fixtureExecute(ctx, req, func(text string) error {
				if calls != 1 {
					t.Error("output preceded the single client-start callback")
				}
				output.WriteString(text)
				return nil
			})
			if calls != 1 {
				t.Fatalf("client-start calls=%d", calls)
			}
			if alive, probeErr := process.Alive(pid); probeErr != nil || alive {
				t.Fatalf("owned fixture was not reaped: alive=%v err=%v", alive, probeErr)
			}
			if refuse {
				_, markerErr := os.Stat(filepath.Join(req.StateHome, "context-receipt.dispatched"))
				if err == nil || out.State != "failed" || output.Len() != 0 || !errors.Is(markerErr, os.ErrNotExist) || strings.Contains(err.Error()+out.Message, "PRIVATE") {
					t.Fatalf("progress failure sent/leaked input: %+v %v marker=%v", out, err, markerErr)
				}
			} else if err != nil || out.State != "complete" || output.String() != "fixture reply" {
				t.Fatalf("progress changed successful execution: %+v %v", out, err)
			}
		})
	}
}

func TestClientStartedProgressAbsentWhenClaudeLaunchFails(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	req.Binding.Executable = filepath.Join(req.StateHome, "absent-native-client")
	called := false
	req.OnClientStarted = func() error { called = true; return nil }
	out, err := fixtureExecute(context.Background(), req, func(string) error { return nil })
	if err == nil || out.State != "failed" || called {
		t.Fatalf("failed launch reported client-start: %+v %v", out, err)
	}
}

func TestClientStartedProgressCannotBypassClaudeAdmission(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	called := false
	req.OnClientStarted = func() error { called = true; return nil }
	out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
	if err == nil || out.State != "failed" || called {
		t.Fatalf("unreviewed fixture binary reported admitted launch: %+v %v", out, err)
	}
}
