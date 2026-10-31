package cli

import (
	"context"
	"testing"
)

func TestComposerActionSearchOpensEffortDirectly(t *testing.T) {
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyText, text: "effort"}, {kind: keyEnter}}}
	value, err := pickTerminal(context.Background(), terminal, "Actions", composerActions(), "")
	if err != nil || value != "/effort" {
		t.Fatalf("effort search opened another action: %q, %v", value, err)
	}
}
