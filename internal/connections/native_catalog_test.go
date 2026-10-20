package connections

import (
	"context"
	"strings"
	"testing"
)

func TestAGYCatalogStrictMetadataAndOfflineReuse(t *testing.T) {
	raw := []byte("example-medium\tExample (Medium)\r\nother-low\tOther (Low)\r\n")
	models, err := parseAGYModels(raw)
	if err != nil || len(models) != 2 || models[0].ID != "example-medium" || models[0].Name != "Example (Medium)" || len(models[0].Efforts) != 0 || models[0].Default {
		t.Fatalf("catalog inferred admission or lost labels: %+v %v", models, err)
	}
	s, options, calls := fixture(t)
	options.Provider = "agy"
	s.inspect = func(_ context.Context, c Connection) (Connection, error) {
		*calls++
		c.AuthState, c.AuthMode, c.Identity = "unknown", "native-keyring", ""
		c.Models = models
		return c, nil
	}
	item, err := s.Connect(context.Background(), options)
	if err != nil || item.AuthState != "unknown" || item.Identity != "" || item.ManagedAI {
		t.Fatalf("catalog became an authenticated route: %+v %v", item, err)
	}
	reopened, err := New(s.home)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.List(context.Background())
	if err != nil || len(items) != 1 || len(items[0].Models) != 2 || *calls != 1 {
		t.Fatalf("cached model browse needed a native process: %+v %v", items, err)
	}
}

func TestAGYCatalogRejectsDriftAndUnsafeText(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty": nil, "blank": []byte("\n"), "header": []byte("ID Name\n"),
		"duplicate": []byte("same\tFirst\nsame\tSecond\n"), "extra": []byte("id\tname\textra"),
		"ansi": []byte("id\t\x1b[31mname"), "bidi": []byte("id\tname\u202e"),
		"url": []byte("https://private\tName"), "space-id": []byte(" bad\tName"),
		"blank-label": []byte("id\t "), "utf8": []byte{'a', '\t', 0xff},
		"too-many":  []byte(strings.Repeat("id\tName\n", 257)),
		"too-large": []byte(strings.Repeat("a", 65537)),
	} {
		t.Run(name, func(t *testing.T) {
			models, err := parseAGYModels(raw)
			if err == nil || len(models) != 0 || strings.Contains(err.Error(), string(raw)) && len(raw) > 4 {
				t.Fatalf("bad catalog accepted or reflected: models=%d err=%v", len(models), err)
			}
		})
	}
}
