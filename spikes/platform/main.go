// P01 is a disposable synthetic platform spike, not the UCLI product.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Request struct {
	ID        string `json:"id"`
	Scenario  string `json:"scenario"`
	TimeoutMS int    `json:"timeout_ms"`
	DelayMS   int    `json:"delay_ms"`
}

type Event struct {
	Type     string `json:"type"`
	Sequence int    `json:"sequence,omitempty"`
	Text     string `json:"text,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

func main() {
	if err := entry(os.Args[1:]); err != nil {
		json.NewEncoder(os.Stderr).Encode(map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}

func entry(args []string) error {
	if len(args) == 0 || args[0] == "--help" {
		fmt.Println("P01 synthetic platform spike: run | host | child | identity | probe | inspect | lease | version")
		return nil
	}
	if args[0] == "version" {
		fmt.Println("p01-spike-v0")
		return nil
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := fs.String("root", "", "isolated fixture directory")
	scenario := fs.String("scenario", "quick", "synthetic scenario")
	id := fs.String("id", "fixture", "synthetic request ID")
	timeout := fs.Int("timeout-ms", 3000, "bounded execution deadline")
	delay := fs.Int("delay-ms", 0, "injected delay")
	pid := fs.Int("pid", 0, "owned fixture PID to inspect")
	workspace := fs.String("workspace", "", "fixture workspace for lease probe")
	mode := fs.String("mode", "read", "read or write fixture lease")
	hold := fs.Int("hold-ms", 250, "bounded fixture lease duration")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "probe" {
		alive, err := processAlive(*pid)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"pid": *pid, "alive": alive})
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	if args[0] == "identity" {
		identity, err := workspaceIdentity(*root)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(identity)
	}
	if args[0] == "leaf" {
		return leaf(*root)
	}
	if args[0] == "child" {
		return fixtureChild(*root, *scenario, *delay)
	}
	if args[0] != "run" && args[0] != "host" && args[0] != "inspect" && args[0] != "lease" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	service, err := openService(*root)
	if err != nil {
		return err
	}
	defer service.Close()
	enc := json.NewEncoder(os.Stdout)
	emit := func(e Event) error { return enc.Encode(e) }
	if args[0] == "inspect" {
		var integrity string
		var count int
		if err := service.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
			return err
		}
		if err := service.db.QueryRow("SELECT count(*) FROM attempts").Scan(&count); err != nil {
			return err
		}
		rows, err := service.db.Query("SELECT id,status FROM attempts ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		states := map[string]string{}
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err != nil {
				return err
			}
			states[id] = status
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return enc.Encode(map[string]any{"sqlite": service.version, "integrity": integrity, "attempt_count": count, "states": states})
	}
	if args[0] == "lease" {
		return service.Lease(*workspace, *mode, *hold, enc)
	}
	if args[0] == "run" {
		result := service.Execute(context.Background(), Request{*id, *scenario, *timeout, *delay}, "foreground", emit)
		if err := enc.Encode(result); err != nil {
			return err
		}
		if result.Error != "" {
			return errors.New(result.Error)
		}
		return nil
	}
	// A pipe-driven fixture host reuses the same service. It is deliberately not
	// a production supervisor, authenticated IPC implementation or detach feature.
	if err := enc.Encode(map[string]any{"type": "host_ready", "sqlite": service.version}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	for scanner.Scan() {
		var req Request
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			return err
		}
		if err := enc.Encode(service.Execute(context.Background(), req, "fixture-supervisor", emit)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func fixtureChild(root, scenario string, delayMS int) error {
	if delayMS > 0 {
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
	}
	if scenario == "malformed" {
		fmt.Println("{broken")
		return nil
	}
	if scenario == "oversized" {
		fmt.Println(strings.Repeat("x", maxFrame+1))
		return nil
	}
	if scenario == "fail" {
		return errors.New("injected child failure")
	}
	enc := json.NewEncoder(os.Stdout)
	if scenario == "descendant" || scenario == "ignore-cancel" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, "leaf", "--root", root)
		configureHidden(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		// Child belongs to the enclosing runner's OS process group/job. The
		// leaf's stop file allows cleanup of the deliberately broken parent-only control.
		if err := os.WriteFile(filepath.Join(root, "leaf.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
			return err
		}
		if err := enc.Encode(Event{Type: "descendant", PID: cmd.Process.Pid}); err != nil {
			return err
		}
	}
	if scenario == "stderr-flood" {
		block := []byte(strings.Repeat("e", 8192))
		for i := 0; i < 512; i++ {
			if _, err := os.Stderr.Write(block); err != nil {
				return err
			}
		}
	}
	if scenario == "split" {
		frame := []byte("{\"type\":\"message_delta\",\"sequence\":1,\"text\":\"한글 ü 👋\"}\n")
		for _, b := range frame {
			if _, err := os.Stdout.Write([]byte{b}); err != nil {
				return err
			}
		}
	} else if err := enc.Encode(Event{Type: "message_delta", Sequence: 1, Text: "fixture marker"}); err != nil {
		return err
	}
	if scenario == "descendant" || scenario == "ignore-cancel" || scenario == "approval" || scenario == "stall" {
		if scenario == "approval" {
			enc.Encode(Event{Type: "input_required", Sequence: 2})
		}
		stop := make(chan struct{})
		if scenario != "ignore-cancel" && scenario != "stall" {
			go func() { bufio.NewReader(os.Stdin).ReadString('\n'); close(stop) }()
		}
		select {
		case <-stop:
		case <-time.After(30 * time.Second):
		}
	}
	if err := enc.Encode(Event{Type: "terminal_result", Sequence: 3, Text: "complete"}); err != nil {
		return err
	}
	if scenario == "trailing" {
		_, err := os.Stdout.Write([]byte("unterminated"))
		return err
	}
	return nil
}

func leaf(root string) error {
	for i := 0; i < 1500; i++ {
		if _, err := os.Stat(filepath.Join(root, "stop-leaf")); err == nil {
			return nil
		}
		if err := os.WriteFile(filepath.Join(root, "heartbeat"), []byte(strconv.Itoa(i)), 0600); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

type Identity struct {
	Root string `json:"root"`
	CWD  string `json:"cwd"`
	ID   string `json:"id"`
}

func workspaceIdentity(cwd string) (Identity, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Identity{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Identity{}, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return Identity{}, err
	}
	if !info.IsDir() {
		return Identity{}, errors.New("workspace is not a directory")
	}
	root := real
	for dir := real; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			root = dir
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	key, err := physicalIdentity(root)
	return Identity{root, real, key}, err
}

// drainCount retains no diagnostic payload; production diagnostics would add
// a redacted bounded tail. This spike verifies a pipe cannot block on stderr.
type drainCount struct{ n int64 }

func (w *drainCount) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }

var _ io.Writer = (*drainCount)(nil)
