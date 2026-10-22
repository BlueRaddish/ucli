package cli

import (
	"context"
	"testing"

	"ucli.local/ucli/internal/connections"
)

func TestExistingConnectionLoginReusesExactNativeProfile(t *testing.T) {
	item := connections.Connection{ID: "connection-original", Provider: "claude", Label: "personal", Executable: "native-cli", NativeHome: "native-profile", HomeMode: "existing", AuthState: "authenticated"}
	f := &fakeConnections{item: item}
	got, err := linkNativeConnection(context.Background(), f, invocation{command: "login", id: item.ID, noInput: true}, func(connections.LoginEvent) error { return nil })
	want := connections.ConnectOptions{Provider: item.Provider, Label: item.Label, Executable: item.Executable, NativeHome: item.NativeHome, HomeMode: item.HomeMode}
	if err != nil || got.ID != item.ID || f.options != want || f.loginCalls != 0 {
		t.Fatalf("reuse bypassed reviewed update path or changed profile: %+v %v", f.options, err)
	}
}
