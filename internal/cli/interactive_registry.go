package cli

import (
	"fmt"
	"strings"
)

type slashSpec struct{ Name, Usage, Description string }

// Help and completion share the supported global actions. Finder-local commands
// and explicitly unavailable actions do not belong in this discovery catalog.
var slashCommands = []slashSpec{
	{"/send", "/send", "submit the saved draft"},
	{"/draft", "/draft TEXT", "replace the draft; omit TEXT to inspect it"},
	{"/models", "/models", "search and choose the next model"},
	{"/accounts", "/accounts", "search and choose an authorized account"},
	{"/login", "/login [NAME]", "connect an existing account or start native sign-in"},
	{"/logout", "/logout ID", "unlink a connection; keep native sign-in"},
	{"/effort", "/effort", "choose auto, low, medium, high or max"},
	{"/permission", "/permission", "choose a permission envelope separately from effort"},
	{"/tasks", "/tasks", "search and page saved work without running it"},
	{"/workflow", "/workflow FILE", "inspect an offline plan and step details; omit FILE to choose"},
	{"/voice", "/voice [FILE]", "dictate or transcribe a PCM WAV into the draft; never send it"},
	{"/result", "/result", "inspect the latest five attempts and saved results"},
	{"/older", "/older", "read the next older page shown by /result"},
	{"/new", "/new", "start a fresh task; keep earlier drafts"},
	{"/explain", "/explain", "inspect the next route without submitting"},
	{"/recover", "/recover", "reconcile a stopped demo; never replay it"},
	{"/demo", "/demo", "enable simulation or explicit native echo (no AI)"},
	{"/help", "/help", "show these actions"},
	{"/exit", "/exit", "leave with saved drafts; never submit on EOF"},
}

func interactiveHelp() string {
	var text strings.Builder
	text.WriteString("Actions:\n")
	for _, command := range slashCommands {
		fmt.Fprintf(&text, "  %-14s %s\n", command.Usage, command.Description)
	}
	text.WriteString("Enter adds a line to the draft. /send submits it.\n" +
		"Commands apply only on a line by themselves or with their documented argument.\n" +
		"Use /draft TEXT to enter text beginning with a slash.\n" +
		"This foreground milestone has no live steering or detach.\n")
	return text.String()
}
