package core

import (
	"strings"
	"testing"
)

func TestIntentFingerprintIgnoresExecutionButNotUserChanges(t *testing.T) {
	a := RunCommand{TaskID: "task", ExpectedRevision: 2, Input: "check tests", Requested: Defaults(), Workspace: Workspace{ID: "workspace", Root: "root", Dir: "root/sub"}}
	b := a
	b.Owner.ID = "another-owner"
	b.Effective.Model = "later-model"
	if a.IntentHash() != b.IntentHash() {
		t.Fatal("ownership/route resolution changed command intent")
	}
	b.Input = "change tests"
	if a.IntentHash() == b.IntentHash() {
		t.Fatal("changed input had same intent fingerprint")
	}
	b = a
	b.ExpectedRevision++
	if a.IntentHash() == b.IntentHash() {
		t.Fatal("stale revision was not part of intent")
	}
}
func TestTrustBoundaryValidation(t *testing.T) {
	if err := ValidateInput("Review 東京 café"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"  ", "x\x00y", string([]byte{255}), strings.Repeat("a", MaxInputBytes+1)} {
		if ValidateInput(s) == nil {
			t.Fatal("invalid input accepted")
		}
	}
	for _, s := range []Selection{{Effort: "extreme"}, {Permission: "auto"}, {Model: "x\ny"}} {
		if ValidateSelection(s) == nil {
			t.Fatal("invalid setting accepted", s)
		}
	}
	if err := ValidateSelection(Selection{}); err != nil {
		t.Fatal(err)
	}
	if Defaults().Permission != "read-only" || ValidateSelection(Selection{Permission: "sandbox"}) != nil {
		t.Fatal("explicit sandbox permission or read-only default changed")
	}
}
