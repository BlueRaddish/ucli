package cli

import (
	"context"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"ucli.local/ucli/internal/app"
)

// Test-only child entrypoint: call the actual CLI service twice in one console,
// then inspect its borrowed OS handles. Production CLI has no testing switches.
func TestMain(m *testing.M) {
	if os.Getenv("UCLI_CONSOLE_MODE_HELPER") == "1" {
		input, output := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
		var beforeIn, beforeOut uint32
		if windows.GetConsoleMode(input, &beforeIn) != nil || windows.GetConsoleMode(output, &beforeOut) != nil {
			os.Exit(90)
		}
		for index := 1; index <= 2; index++ {
			code := Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
			var afterIn, afterOut uint32
			if code != 0 || windows.GetConsoleMode(input, &afterIn) != nil || windows.GetConsoleMode(output, &afterOut) != nil || beforeIn != afterIn || beforeOut != afterOut {
				fmt.Println("Console mode/handle mismatch")
				os.Exit(91)
			}
			fmt.Printf("Console mode roundtrip %d passed; borrowed handles open\n", index)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestConPTYConsoleModeRestoration(t *testing.T) {
	if os.Getenv("UCLI_CONPTY_BINARY") == "" {
		t.Skip("explicit packaged terminal verification opt-in is required")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("UCLI_CONSOLE_MODE_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, cwd := t.TempDir(), t.TempDir()
	p := startConPTYOptions(t, binary, home, cwd, false)
	p.await(t, 0, "Tab actions")
	mark := p.send(t, "/exit\r")
	p.await(t, mark, "Console mode roundtrip 1 passed")
	p.await(t, mark, "Tab actions")
	mark = p.send(t, "Second run preserved every key\r")
	p.await(t, mark, "Ready for your next prompt")
	mark = p.send(t, "/exit\r")
	p.await(t, mark, "Console mode roundtrip 2 passed")
	if code := p.wait(t); code != 0 {
		t.Fatalf("console lifecycle helper exit%d", code)
	}
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tasks, err := a.Tasks(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Draft != "" {
		t.Fatalf("reader survived shutdown or ate next-session input: %+v %v", tasks, err)
	}
	_, attempts, err := a.Task(context.Background(), tasks[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Input != "Second run preserved every key" || attempts[0].State != "complete" {
		t.Fatalf("second entrypoint did not submit exactly once: %+v %v", attempts, err)
	}
}
