package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

// This fixture deliberately has no native-operation methods. Browsing needs
// neither a task store nor an authentication-capable service.
type loginCacheFixture struct {
	items       []connections.Connection
	err         error
	lists, gets int
	requestedID string
}

func (f *loginCacheFixture) List(context.Context) ([]connections.Connection, error) {
	f.lists++
	return f.items, f.err
}

func (f *loginCacheFixture) Get(_ context.Context, id string) (connections.Connection, error) {
	f.gets++
	f.requestedID = id
	if len(f.items) == 0 {
		return connections.Connection{}, f.err
	}
	return f.items[0], f.err
}

func TestLoginPresentationKeepsSignInSeparateFromCompatibility(t *testing.T) {
	cases := []struct {
		name, auth, compatibility, status, next string
	}{
		{"signed-in", "authenticated", "", "signed in at last check", "checked again before sending"},
		{"version-review", "authenticated", "unknown", "app compatibility not verified", "unsupported app version"},
		{"signed-out", "signed-out", "", "signed out at last check", "Choose Sign in"},
		{"unknown", "unknown", "", "could not be verified", "new sign-in may not be needed"},
		{"not-checked", "", "", "not checked", "Check the existing account first"},
		{"changed-account", "identity-changed", "unknown", "account changed", "Unlink this connection"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			view := presentConnection(connections.Connection{Provider: "claude", AuthState: tt.auth, Compatibility: tt.compatibility})
			if !strings.Contains(view.status, tt.status) || !strings.Contains(view.next, tt.next) {
				t.Fatalf("status/action lost: %+v", view)
			}
			if strings.Contains(view.status, "ready to send") {
				t.Fatal("cached metadata promised current execution readiness")
			}
		})
	}
}

func TestLoginCachedIDResolvesProviderWithoutReadingNativeState(t *testing.T) {
	cache := &loginCacheFixture{items: []connections.Connection{{ID: "opaque-reference", Label: "Personal", Provider: "claude", AuthState: "authenticated", Compatibility: "unknown"}}}
	var buf bytes.Buffer
	o := newOutput(&buf, &buf, false, func() {})
	input := newLineInput(strings.NewReader("1\n"))
	defer close(input.stop)
	s := &session{out: o, input: input}
	action, err := s.chooseConnectionAction(context.Background(), "opaque-reference", cache)
	if finishErr := o.finish(); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err != nil || action != "check" || cache.gets != 1 || cache.lists != 0 || cache.requestedID != "opaque-reference" {
		t.Fatalf("cached target lost: action=%s error=%v cache=%+v", action, err, cache)
	}
	text := buf.String()
	if !strings.Contains(text, "Claude Code / Personal") || !strings.Contains(text, "app compatibility not verified") || strings.Contains(text, "Native account") || strings.Contains(text, "opaque-reference") {
		t.Fatalf("opaque target lacks friendly context: %s", text)
	}
}

func TestLoginDetailsAndBackPreserveDraftAndChoices(t *testing.T) {
	cache := &loginCacheFixture{items: []connections.Connection{{ID: "opaque-reference", Provider: "codex", AuthState: "unknown", Identity: "PRIVATE_IDENTITY", NativeHome: "PRIVATE_HOME", Executable: "PRIVATE_EXECUTABLE"}}}
	var buf bytes.Buffer
	o := newOutput(&buf, &buf, false, func() {})
	input := newLineInput(strings.NewReader("3\n0\n"))
	defer close(input.stop)
	task := core.Task{ID: "saved-task", Draft: "unsent draft", Revision: 7}
	selection := core.Defaults()
	selection.Model = "codex/example"
	s := &session{out: o, input: input, task: &task, selection: selection}
	action, err := s.chooseConnectionAction(context.Background(), "opaque-reference", cache)
	if finishErr := o.finish(); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err != nil || action != "" || task.Draft != "unsent draft" || task.Revision != 7 || !reflect.DeepEqual(s.selection, selection) || cache.gets != 1 {
		t.Fatalf("details/back changed composition or refreshed: action=%s error=%v", action, err)
	}
	text := buf.String()
	if !strings.Contains(text, "Saved status is not a live check") || !strings.Contains(text, "does not copy credentials") {
		t.Fatal("details omitted the check boundary")
	}
	for _, private := range []string{"PRIVATE_IDENTITY", "PRIVATE_HOME", "PRIVATE_EXECUTABLE"} {
		if strings.Contains(text, private) {
			t.Fatal("details exposed native metadata")
		}
	}
}

func TestLoginAGYSavedIDOffersOnlyCatalogInspection(t *testing.T) {
	cache := &loginCacheFixture{items: []connections.Connection{{ID: "opaque-agy", Provider: "agy", AuthState: "unknown"}}}
	view, err := cachedLoginPresentation(context.Background(), "opaque-agy", cache)
	if err != nil {
		t.Fatal(err)
	}
	rows := connectionActionRows(view.provider)
	if len(rows) != 2 || rows[0].value != "check" || rows[1].value != "details" || !strings.Contains(view.next, "not supported") {
		t.Fatalf("catalog-only connection offered native login or execution: %+v %+v", view, rows)
	}
}

func TestLoginProviderBrowsingIsCachedAndDoesNotSelectSavedIdentity(t *testing.T) {
	cache := &loginCacheFixture{items: []connections.Connection{{ID: "private-a", Provider: "claude", AuthState: "authenticated"}, {ID: "private-b", Provider: "claude", AuthState: "signed-out"}}}
	var buf bytes.Buffer
	o := newOutput(&buf, &buf, false, func() {})
	input := newLineInput(strings.NewReader("2\n"))
	defer close(input.stop)
	s := &session{out: o, input: input}
	provider, err := s.chooseLoginProvider(context.Background(), cache)
	if finishErr := o.finish(); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err != nil || provider != "claude" || cache.lists != 1 || cache.gets != 0 {
		t.Fatalf("provider selection changed binding: %s %v", provider, err)
	}
	if !strings.Contains(buf.String(), "2 saved") || strings.Contains(buf.String(), "private-") || strings.Contains(buf.String(), "signed in") {
		t.Fatal("provider aggregate inferred its default account's identity or current sign-in")
	}
}

func TestLoginCacheFailureDoesNotOfferGuessedProvider(t *testing.T) {
	want := errors.New("synthetic unreadable metadata")
	cache := &loginCacheFixture{err: want}
	s := &session{}
	if _, err := s.chooseConnectionAction(context.Background(), "opaque-reference", cache); !errors.Is(err, want) {
		t.Fatalf("cache failure hidden: %v", err)
	}
}

func TestLoginHumanSummaryAndJSONContract(t *testing.T) {
	item := connections.Connection{ID: "reference", Label: "Personal", Provider: "github", AuthState: "authenticated"}
	var human, actual, expected bytes.Buffer
	for _, run := range []struct {
		buffer *bytes.Buffer
		json   bool
		direct bool
	}{{&human, false, false}, {&actual, true, false}, {&expected, true, true}} {
		o := newOutput(run.buffer, run.buffer, run.json, func() {})
		var err error
		if run.direct {
			err = o.data("connection", item)
		} else {
			err = showConnection(item, o)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := o.finish(); err != nil {
			t.Fatal(err)
		}
	}
	if actual.String() != expected.String() {
		t.Fatal("presentation changed the JSON connection contract")
	}
	if !strings.HasPrefix(human.String(), "GitHub | Personal\nStatus:") || !strings.Contains(human.String(), "no AI models") {
		t.Fatal("human summary omitted provider, status or capability boundary")
	}
}
