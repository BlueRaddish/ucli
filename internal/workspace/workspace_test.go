package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalAliasesSubdirectoryAndLinkedWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("actual Git unavailable for worktree setup")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo space ü")
	other := filepath.Join(base, "other worktree")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		hideTest(cmd)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(e), "GIT_") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	run("init", "--quiet", repo)
	run("-C", repo, "-c", "user.name=UCLI Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "commit", "--quiet", "--allow-empty", "-m", "fixture")
	run("-C", repo, "worktree", "add", "--quiet", "--detach", other, "HEAD")
	sub := filepath.Join(repo, "src")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias path")
	if err := makeAlias(alias, repo); err != nil {
		t.Fatal(err)
	}
	a, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(sub)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Resolve(other)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || a.ID != c.ID || a.ID == d.ID {
		t.Fatalf("physical IDs: %+v %+v %+v %+v", a, b, c, d)
	}
	if b.Root != a.Root || b.Dir != sub || c.Dir != alias {
		t.Fatalf("execution directories not preserved: %+v %+v", b, c)
	}
}

func TestPlainDirectoryAndInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	w, err := Resolve(dir)
	if err != nil || w.Dir != dir || w.Root != dir || w.ID == "" {
		t.Fatalf("plain directory: %+v %v", w, err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", file, filepath.Join(dir, "missing")} {
		if _, err := Resolve(path); err == nil {
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
}
