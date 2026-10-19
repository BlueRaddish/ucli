package state

import (
	"testing"
	"ucli.local/ucli/internal/core"
)

func TestNativeAccountLabelBecomesStableTaskPinWithoutChangingReceipt(t *testing.T) {
	for _, tc := range []struct{ name, mode, requested, want string }{{"native-label", "native", "work", "connection-original"}, {"native-auto", "native", "auto", "auto"}, {"demo-label", "demo", "work", "work"}} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			s := openTest(t, home)
			cmd := command("command-pin", "workspace-pin", "read-only")
			cmd.Requested.Account = tc.requested
			cmd.Effective.Mode = tc.mode
			cmd.Effective.Account = "connection-original"
			if tc.mode == "native" {
				cmd.Effective.Runtime = "codex"
				cmd.Effective.Provider = "openai"
				cmd.Effective.Model = "codex/synthetic"
			}
			intent := cmd.IntentHash()
			accepted := accept(t, s, cmd)
			task, err := s.GetTask(background, accepted.TaskID)
			if err != nil || task.Selection.Account != tc.want {
				t.Fatalf("saved account=%q want=%q err=%v", task.Selection.Account, tc.want, err)
			}
			if accepted.Requested.Account != tc.requested || accepted.Effective.Account != "connection-original" {
				t.Fatal("acceptance receipt lost literal request or effective account")
			}
			stored, hash, err := s.GetCommand(background, cmd.ID)
			if err != nil || hash != intent || stored.Requested.Account != tc.requested {
				t.Fatal("command intent changed", err)
			}
			replay := cmd
			replay.Owner = core.Owner{}
			replay.Effective = core.Route{Account: "replacement"}
			again, fresh, err := s.Accept(background, replay)
			if err != nil || fresh || again.ID != accepted.ID || again.Effective.Account != "connection-original" || again.Requested.Account != tc.requested {
				t.Fatal("replay was rebound", err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, home)
			task, err = s.GetTask(background, accepted.TaskID)
			if err != nil || task.Selection.Account != tc.want {
				t.Fatal("task pin did not survive reopen", err)
			}
		})
	}
}
