//go:build darwin

package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinLaunchFailsBeforeExecutingChild(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-exist")
	child, err := Start(context.Background(), Spec{Executable: "/usr/bin/touch", Args: []string{marker}, Env: []string{}, Dir: root})
	if child != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("child=%v err=%v", child, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported platform executed a child")
	}
}
