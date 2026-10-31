package cli

import (
	"fmt"
	"strconv"
	"strings"

	"ucli.local/ucli/internal/core"
)

// Version can be set by the release build's -ldflags -X option.
var Version = "0.1.0-dev"

type flagSpec struct{ name, short, argument, description string }

// Parsing and help use this single vocabulary. No provider-specific flags leak in.
var flags = []flagSpec{
	{"help", "h", "", "show this reference and exit"},
	{"version", "V", "", "show the build version and exit"},
	{"home", "", "DIR", "use local UCLI state in DIR"},
	{"json", "", "", "write versioned JSON lines to stdout"},
	{"no-input", "", "", "disable interactive prompts"},
	{"plain", "", "", "use static text without color"},
	{"no-color", "", "", "disable color; keep terminal keyboard navigation"},
	{"no-motion", "", "", "keep terminal cats static; retain keyboard navigation"},
	{"no-mouse", "", "", "disable terminal clicks; F2 toggles while open"},
	{"ascii", "", "", "use ASCII cat artwork in the rich terminal"},
	{"model", "", "MODEL", "choose a model (new task default: auto)"},
	{"effort", "", "EFFORT", "choose auto|low|medium|high|max (new task: auto)"},
	{"account", "", "ACCOUNT", "choose an authorized account (new task: auto)"},
	{"permission", "", "MODE", "choose read-only|workspace-write|sandbox|unrestricted"},
	{"cwd", "", "DIR", "execute in DIR (default: current directory)"},
	{"demo", "", "", "enable local simulation or explicit native echo (no AI)"},
	{"command-id", "", "ID", "reuse an unchanged submission's durable receipt"},
	{"limit", "", "N", "show 1-100 items per page (list: 20; history: 5)"},
	{"before-revision", "", "R", "show attempts older than revision R (0: latest)"},
	{"search", "", "TEXT", "find task titles or IDs across the saved archive"},
	{"cursor", "", "TOKEN", "continue a task list using its returned cursor"},
	{"offline", "", "", "reuse prepared runtime/model bytes without downloading"},
	{"python", "", "FILE", "use this Python interpreter for voice"},
	{"python-site", "", "DIR", "reuse this explicit installed Python package directory"},
	{"model-dir", "", "DIR", "use an existing local transcription model without downloading"},
	{"language", "", "CODE", "choose the transcription language (default: en)"},
	{"duration", "", "SECONDS", "bound voice recording/audio to 1-120 seconds (default: 30)"},
	{"label", "", "NAME", "name this local connection"},
	{"native-home", "", "DIR", "connect this native account/configuration directory"},
	{"executable", "", "FILE", "connect this exact native executable"},
	{"separate", "", "", "create a separate native sign-in home when supported"},
}

type commandSpec struct {
	name, usage, description string
	examples                 []example
}
type example struct{ caption, command string }

var commands = []commandSpec{
	{"run", "run [OPTIONS] PROMPT", "start a fresh task", []example{{"start a local simulated task", "ucli run --demo \"Summarize this project\""}, {"test a pinned native echo runtime (no AI or login)", "ucli run --demo --model muse/echo \"Check native echo\""}, {"pass an option-like prompt literally", "ucli run --demo -- -literal-input"}}},
	{"models", "models [OPTIONS]", "list local model capabilities and availability", []example{{"inspect local model choices", "ucli models"}, {"read the catalog as JSON lines", "ucli models --json"}}},
	{"accounts", "accounts [OPTIONS]", "list local accounts and identity verification", []example{{"inspect local account choices", "ucli accounts"}, {"read accounts as JSON lines", "ucli accounts --json"}}},
	{"accounts list", "accounts list [OPTIONS]", "list cached local connection metadata", []example{{"list linked native connections", "ucli accounts list --json"}}},
	{"accounts discover", "accounts discover [OPTIONS]", "find native clients without running them or reading credentials", []example{{"find installed sign-in providers", "ucli accounts discover"}}},
	{"accounts add", "accounts add PROVIDER [OPTIONS]", "link and check a native account without signing it out", []example{{"reuse an existing Codex sign-in", "ucli accounts add codex"}, {"name another native account home", "ucli accounts add codex --label work --native-home PATH"}}},
	{"accounts status", "accounts status [ID] [OPTIONS]", "explicitly refresh native sign-in status without inference", []example{{"check linked account authentication", "ucli accounts status"}}},
	{"accounts remove", "accounts remove ID [OPTIONS]", "unlink UCLI metadata while retaining native sign-in", []example{{"remove one UCLI connection", "ucli accounts remove CONNECTION_ID"}}},
	{"login", "login [PROVIDER|ID] [OPTIONS]", "reuse an existing native sign-in or start its supported login flow", []example{{"connect Claude's existing sign-in", "ucli login claude"}, {"sign into a separate Codex home", "ucli login codex --separate --label work"}, {"link the GitHub CLI identity", "ucli login github"}}},
	{"logout", "logout ID [OPTIONS]", "unlink one UCLI connection without revoking native credentials", []example{{"unlink a UCLI connection", "ucli logout CONNECTION_ID"}}},
	{"tasks", "tasks [list|show|followup|recover] [OPTIONS]", "find saved work and recovery paths", []example{{"find saved tasks", "ucli tasks"}, {"read task summaries as JSON lines", "ucli tasks list --json"}}},
	{"tasks list", "tasks list [OPTIONS]", "search and page saved task summaries without starting work", []example{{"find saved tasks", "ucli tasks list"}, {"search the whole archive", "ucli tasks list --search \"cache review\" --json"}, {"continue using a returned cursor and the same search", "ucli tasks list --cursor TOKEN --limit 20"}}},
	{"tasks show", "tasks show ID [OPTIONS]", "inspect drafts and recent results; page into older work", []example{{"inspect the latest five attempts", "ucli tasks show TASK_ID"}, {"read an older page using its returned cursor", "ucli tasks show TASK_ID --before-revision 12 --limit 5 --json"}}},
	{"tasks followup", "tasks followup ID [OPTIONS] PROMPT", "add work to an existing task thread", []example{{"continue an ID from ucli tasks", "ucli tasks followup TASK_ID --demo \"Explain the result\""}, {"supply the next input through redirected stdin", "ucli tasks followup TASK_ID --demo -"}}},
	{"tasks recover", "tasks recover ID [OPTIONS]", "reconcile a stopped demo without replaying work", []example{{"reconcile a stopped demo ID from ucli tasks", "ucli tasks recover TASK_ID"}, {"inspect the reconciled outcome", "ucli tasks show TASK_ID"}}},
	{"workflow", "workflow check FILE [OPTIONS]", "check a workflow offline without starting tasks", []example{{"check a workflow file", "ucli workflow check workflow.json"}, {"read an offline plan as JSON lines", "ucli workflow check workflow.json --json"}}},
	{"workflow check", "workflow check FILE [OPTIONS]", "validate a workflow and show initial ready steps", []example{{"check a workflow file without opening task storage", "ucli workflow check workflow.json"}, {"read a workflow from redirected stdin", "ucli workflow check - --json"}}},
	{"workflow view", "workflow view FILE [OPTIONS]", "inspect an offline plan and its steps without running it", []example{{"browse a validated plan", "ucli workflow view examples/review.workflow.json"}, {"read the validated plan and requested settings as JSON", "ucli workflow view workflow.json --json"}}},
	{"explain", "explain [OPTIONS] [PROMPT]", "inspect routing choices without running a task", []example{{"preview the default simulated route", "ucli explain --demo"}, {"preview a pinned model and effort", "ucli explain --demo --model demo/deep --effort high"}}},
	{"doctor", "doctor [OPTIONS]", "check full state integrity and local readiness", []example{{"check local state integrity and readiness", "ucli doctor"}, {"read the completed diagnostic as JSON lines", "ucli doctor --json"}}},
	{"backup", "backup FILE [OPTIONS]", "save a standalone copy of local task data", []example{{"save to a new file in an existing directory", "ucli backup ucli-backup.sqlite"}, {"save an explicit home and read its receipt", "ucli backup ucli-backup.sqlite --home .ucli --json"}}},
	{"migrate", "migrate BACKUP_FILE [OPTIONS]", "explicitly enable faster local task search with a verified backup", []example{{"upgrade a quiescent home after saving its original state", "ucli migrate before-schema2.sqlite"}, {"upgrade an explicit home and read the outcome", "ucli migrate before-schema2.sqlite --home .ucli --json"}}},
	{"runtime", "runtime [list|prepare] [OPTIONS]", "inspect and prepare native bytes only when requested", []example{{"inspect reviewed optional downloads", "ucli runtime list"}, {"reuse or download only Claude's reviewed native bytes", "ucli runtime prepare claude"}}},
	{"runtime list", "runtime list [OPTIONS]", "list reviewed native downloads without inspecting or downloading them", []example{{"inspect the optional runtime catalog", "ucli runtime list --json"}}},
	{"runtime prepare", "runtime prepare ID [OPTIONS]", "reuse verified local native bytes or download the requested runtime", []example{{"prepare only the requested runtime", "ucli runtime prepare claude"}, {"reuse available bytes without downloading", "ucli runtime prepare claude --offline"}}},
	{"voice", "voice [record|transcribe|models|setup] [OPTIONS]", "dictate or transcribe without sending a task", []example{{"dictate with visible recording controls", "ucli voice"}, {"transcribe a local PCM WAV", "ucli voice transcribe note.wav"}}},
	{"voice record", "voice record [OPTIONS]", "record bounded speech and return transcript text", []example{{"record up to thirty seconds", "ucli voice record"}, {"record a bounded scripted take", "ucli voice record --duration 10 --no-input --json"}}},
	{"voice transcribe", "voice transcribe FILE [OPTIONS]", "transcribe local 16 kHz mono PCM16 WAV without sending it", []example{{"read a transcript as JSON lines", "ucli voice transcribe note.wav --json"}, {"use existing model files without downloads", "ucli voice transcribe note.wav --model-dir LOCAL_MODEL --offline"}}},
	{"voice models", "voice models [OPTIONS]", "list lightweight transcription model metadata without loading models", []example{{"inspect download sizes", "ucli voice models"}}},
	{"voice setup", "voice setup [OPTIONS]", "prepare only the requested voice runtime and model", []example{{"prepare voice on demand", "ucli voice setup"}, {"reuse an existing Python interpreter and model", "ucli voice setup --python PYTHON --model-dir LOCAL_MODEL --offline"}}},
}

type invocation struct {
	command, id, prompt, home, cwd, commandID       string
	file, search, cursor                            string
	selection                                       core.Selection
	json, noInput, demo, help, version              bool
	pageLimit, beforeRevision                       int
	positionals                                     []string
	set                                             map[string]bool
	offline                                         bool
	voicePython, voiceSite, voiceModelDir, language string
	voiceDuration                                   int
	connectionLabel, nativeHome, executable         string
	separate                                        bool
}

func parse(args []string) (invocation, error) {
	p := invocation{set: map[string]bool{}, pageLimit: 5, voiceDuration: 30}
	// Explicit help takes precedence over malformed flags and needs no state.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			p.help = true
		}
		if arg == "--json" {
			p.json = true
		}
	}
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !literal && arg == "--" {
			literal = true
			continue
		}
		if literal || arg == "-" || !strings.HasPrefix(arg, "-") {
			p.positionals = append(p.positionals, arg)
			continue
		}
		name, value, equal := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		var spec *flagSpec
		for j := range flags {
			f := &flags[j]
			if arg == "-"+f.short && f.short != "" || strings.HasPrefix(arg, "--") && name == f.name {
				spec = f
				break
			}
		}
		if spec == nil {
			if p.help {
				continue
			}
			return p, usageError("unknown option " + arg + "; run ucli --help")
		}
		name = spec.name
		if spec.argument != "" {
			if !equal {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					if p.help {
						continue
					}
					return p, usageError("--" + name + " needs " + spec.argument)
				}
				i++
				value = args[i]
			}
			if value == "" && !p.help && name != "search" && name != "cursor" {
				return p, usageError("--" + name + " must not be empty")
			}
		} else if equal && !p.help {
			return p, usageError("--" + name + " does not take a value")
		}
		p.set[name] = true
		switch name {
		case "help":
			p.help = true
		case "version":
			p.version = true
		case "home":
			p.home = value
		case "json":
			p.json = true
		case "no-input":
			p.noInput = true
		case "demo":
			p.demo = true
		case "model":
			p.selection.Model = value
		case "account":
			p.selection.Account = value
		case "effort":
			p.selection.Effort = value
		case "permission":
			p.selection.Permission = value
		case "cwd":
			p.cwd = value
		case "command-id":
			p.commandID = value
		case "search":
			p.search = value
		case "cursor":
			p.cursor = value
		case "offline":
			p.offline = true
		case "python":
			p.voicePython = value
		case "python-site":
			p.voiceSite = value
		case "model-dir":
			p.voiceModelDir = value
		case "language":
			p.language = value
		case "duration":
			number, err := strconv.Atoi(value)
			if err != nil && !p.help {
				return p, usageError("--duration needs a whole number of seconds")
			}
			p.voiceDuration = number
		case "label":
			p.connectionLabel = value
		case "native-home":
			p.nativeHome = value
		case "executable":
			p.executable = value
		case "separate":
			p.separate = true
		case "limit", "before-revision":
			number, err := strconv.Atoi(value)
			if err != nil && !p.help {
				return p, usageError("--" + name + " needs a whole number")
			}
			if name == "limit" {
				p.pageLimit = number
			} else {
				p.beforeRevision = number
			}
		}
	}
	pos := p.positionals
	if len(pos) > 0 && pos[0] == "help" {
		p.help = true
		pos = pos[1:]
	}
	if len(pos) > 0 {
		p.command = pos[0]
		pos = pos[1:]
		if (p.command == "tasks" || p.command == "workflow" || p.command == "voice" || p.command == "runtime" || p.command == "accounts") && len(pos) > 0 {
			p.command += " " + pos[0]
			pos = pos[1:]
		}
	}
	if p.help || p.version {
		return p, nil
	}
	if p.pageLimit < 1 || p.pageLimit > 100 {
		return p, usageError("--limit must be between 1 and 100")
	}
	if p.beforeRevision < 0 {
		return p, usageError("--before-revision must be 0 (latest) or a positive revision")
	}
	if p.command == "tasks" {
		p.command = "tasks list"
	}
	if p.command == "runtime" {
		p.command = "runtime list"
	}
	if p.command == "voice" {
		p.command = "voice record"
	}
	if p.command == "tasks list" && !p.set["limit"] {
		p.pageLimit = 20
	}
	if p.command != "" && command(p.command) == nil {
		if p.command == "native" {
			return p, usageError(p.command + " is not implemented in this milestone; use ucli models to inspect local choices or ucli run --demo PROMPT for the simulated task loop")
		}
		return p, usageError("unknown command " + p.command + "; run ucli --help for available commands")
	}
	switch p.command {
	case "login", "accounts status":
		if len(pos) > 1 {
			return p, usageError("use ucli " + p.command + " [PROVIDER|ID]")
		}
		if len(pos) == 1 {
			p.id = pos[0]
		}
	case "accounts add", "accounts remove", "logout":
		if len(pos) != 1 {
			return p, usageError("use ucli " + p.command + " ID")
		}
		p.id = pos[0]
	case "run":
		if len(pos) == 0 {
			return p, usageError("a prompt is required; use ucli run [OPTIONS] PROMPT or ucli run -")
		}
		p.prompt = strings.Join(pos, " ")
	case "tasks followup":
		if len(pos) < 2 {
			return p, usageError("use ucli tasks followup ID [OPTIONS] PROMPT")
		}
		p.id, p.prompt = pos[0], strings.Join(pos[1:], " ")
	case "tasks show", "tasks recover":
		if len(pos) != 1 {
			return p, usageError("use ucli " + p.command + " ID")
		}
		p.id = pos[0]
	case "runtime prepare":
		if len(pos) != 1 {
			return p, usageError("use ucli runtime prepare ID")
		}
		p.id = pos[0]
	case "voice transcribe":
		if len(pos) != 1 || pos[0] == "-" {
			return p, usageError("use ucli voice transcribe FILE with a local PCM WAV file")
		}
		p.file = pos[0]
	case "workflow check", "workflow view":
		if len(pos) != 1 {
			return p, usageError("use ucli " + p.command + " FILE, or - for redirected stdin")
		}
		p.file = pos[0]
	case "backup", "migrate":
		if len(pos) != 1 || pos[0] == "" || pos[0] == "-" {
			return p, usageError("use ucli " + p.command + " FILE with a new local file path; stdin/stdout backups are not supported")
		}
		p.file = pos[0]
	case "explain":
		p.prompt = strings.Join(pos, " ")
	default:
		if len(pos) != 0 {
			return p, usageError("unexpected argument; run ucli " + p.command + " --help")
		}
	}
	if p.command != "" && p.command != "run" && p.command != "tasks followup" && p.command != "explain" {
		for _, f := range []string{"model", "account", "effort", "permission", "cwd", "demo", "command-id"} {
			if f == "model" && isVoiceOperation(p.command) {
				continue
			}
			if p.set[f] {
				return p, usageError("--" + f + " applies to run, tasks followup, explain or the interactive prompt")
			}
		}
	}
	for _, f := range []string{"python", "python-site", "model-dir", "language", "duration"} {
		if p.set[f] && !isVoiceOperation(p.command) {
			return p, usageError("--" + f + " applies only to voice record, voice transcribe and voice setup")
		}
	}
	for _, f := range []string{"label", "native-home", "executable", "separate"} {
		if p.set[f] && p.command != "login" && p.command != "accounts add" {
			return p, usageError("--" + f + " applies only to login and accounts add")
		}
	}
	if isVoiceOperation(p.command) && (p.voiceDuration < 1 || p.voiceDuration > 120) {
		return p, usageError("--duration must be between 1 and 120 seconds")
	}
	if isVoiceOperation(p.command) && p.set["model"] && p.set["model-dir"] {
		return p, usageError("choose --model or --model-dir for voice, so the requested model is unambiguous")
	}
	if p.set["offline"] && !strings.HasPrefix(p.command, "voice ") && !strings.HasPrefix(p.command, "runtime ") {
		return p, usageError("--offline applies to voice and runtime preparation")
	}
	if (p.command == "explain" || p.command == "") && p.set["command-id"] {
		return p, usageError("--command-id applies to run and tasks followup submissions")
	}
	if p.command != "tasks show" && p.set["before-revision"] {
		return p, usageError("--before-revision applies only to tasks show")
	}
	if p.command != "tasks show" && p.command != "tasks list" && p.set["limit"] {
		return p, usageError("--limit applies only to tasks list and tasks show")
	}
	if p.command != "tasks list" && (p.set["search"] || p.set["cursor"]) {
		return p, usageError("--search and --cursor apply only to tasks list")
	}
	if p.command == "workflow" {
		p.help = true
	}
	return p, nil
}

func isVoiceOperation(command string) bool {
	return command == "voice record" || command == "voice transcribe" || command == "voice setup"
}

func command(name string) *commandSpec {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}
func usageError(message string) error { return core.Problem("usage", message, 2) }

func help(topic string, brief bool) string {
	if brief {
		return "UCLI - one place to choose an agent and keep your work\n\n" +
			"Usage: ucli [OPTIONS] COMMAND\n\n" +
			"  $ ucli run --demo \"Summarize this project\"\n  $ ucli models\n\n" +
			"  run       start a task       models     see available models\n" +
			"  accounts  inspect accounts   tasks      find saved work\n" +
			"  login     connect an account logout     unlink a connection\n" +
			"  explain   inspect routing    doctor     inspect local readiness\n" +
			"  workflow  check a file offline without starting tasks\n" +
			"  voice     dictate or transcribe into text; no task is sent\n" +
			"  runtime   inspect optional downloads; prepare only on request\n" +
			"  backup    save a standalone copy of local task data\n\n" +
			"  migrate   explicitly upgrade task search after a verified backup\n\n" +
			"Open ucli in a terminal for the interactive prompt.\n" +
			"Demo enables local simulation or explicit native echo. Neither uses AI.\n" +
			"Use login to connect a native account; --demo needs no sign-in.\n" +
			"Run 'ucli --help' for the reference; 'ucli run --help' for task options.\n"
	}
	c := command(topic)
	usage, description := "[OPTIONS] COMMAND", "choose models and accounts, run tasks, and return to saved work"
	examples := []example{{"inspect local model choices", "ucli models"}, {"start a local simulated task", "ucli run --demo \"Summarize this project\""}}
	if c != nil {
		usage, description, examples = c.usage, c.description, c.examples
	}
	offline := topic == "workflow" || topic == "workflow check" || topic == "workflow view"
	details := "  use a common task vocabulary across agent runtimes\n  login links native accounts; a managed adapter verifies each dispatch\n  --demo enables local simulation; --model muse/echo selects native echo\n  native echo needs a verified pinned runtime and uses no AI or login\n  native read-only uses text input with tools disabled\n  --permission sandbox requests full local computer access explicitly\n  keep task choices on follow-up unless an explicit flag changes them\n"
	if offline {
		details = "  validate workflow JSON and inspect initial dependency readiness\n  no task is accepted, persisted or executed; no model or account is contacted\n"
		if topic == "workflow view" {
			details = "  inspect a validated offline plan and step details\n  rich terminals show a keyboard browser; --plain prints the plan\n  --json includes the declared prompts and requested settings\n  all steps remain pending; no task, provider or account is started\n  no UCLI home is opened or modified\n"
		}
	} else if topic == "backup" {
		details = "  create a standalone SQLite snapshot of saved tasks, drafts and results\n  include committed changes from the live database and its write-ahead log\n  native credential stores are not included; no provider is contacted\n  unsettled attempts remain unsettled; backup does not restore or resume work\n"
	} else if topic == "migrate" {
		details = "  explicitly advance local schema 1 to 2 for faster task search metadata\n  first publish a verified pre-migration backup and its snapshot certificate\n  running/unknown attempts or workspace leases block migration\n  opening a home never upgrades it; new homes remain schema 1\n  old schema-1-only builds refuse an upgraded home; no automatic downgrade\n  the snapshot certificate does not prove migration committed; inspect doctor\n  no stdin is read; no provider, account or credential store is contacted\n"
	} else if topic == "voice" || strings.HasPrefix(topic, "voice ") {
		details = "  voice returns text; it never submits an AI task or types into another app\n  no microphone, Python package or model is loaded during ordinary startup\n  reuse installed dependencies and model files when available\n  download only the requested voice dependencies/model on first use\n  --offline forbids downloads; --model-dir selects existing local model files\n  voice models lists metadata only; voice setup remembers the chosen settings\n  rich terminal: Enter stops, Esc cancels; --duration bounds every take\n  plain/redirected capture is timed; Ctrl+C cancels\n  file input: 16 kHz mono PCM16 WAV, at most 120 seconds\n"
	} else if topic == "runtime" || strings.HasPrefix(topic, "runtime ") {
		details = "  catalog listing uses shipped metadata and never downloads or runs providers\n  prepare reuses exact reviewed native bytes, then downloads only this runtime\n  --offline permits reuse only; existing native installations remain untouched\n  preparing bytes does not execute a provider, log in or admit managed AI\n"
	} else if topic == "login" || topic == "logout" || topic == "accounts" || strings.HasPrefix(topic, "accounts ") {
		details = "  connect existing native sign-ins without copying their tokens\n  use --label to remember an account and --native-home for an explicit profile\n  --separate requests a new native sign-in home when the provider supports it\n  status refreshes authentication without AI inference; lists use cached metadata\n  login reuses an authenticated connection or starts its native device/browser flow\n  --no-input links available accounts and refuses unfinished interactive sign-in\n  logout/remove only unlink UCLI metadata; native credentials are not revoked\n  GitHub uses GitHub CLI identity locally; hosted UCLI identity/sync is separate\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "NAME\n  ucli - %s\n\nUSAGE\n  ucli %s\n\nDESCRIPTION\n%s\nEXAMPLES\n", description, usage, details)
	for _, e := range examples {
		fmt.Fprintf(&b, "  %s\n  $ %s\n", e.caption, e.command)
	}
	group := topic == "tasks" || topic == "workflow" || topic == "voice" || topic == "runtime" || topic == "accounts"
	if c == nil || group {
		b.WriteString("\nCOMMANDS\n")
		for _, item := range commands {
			if group && !strings.HasPrefix(item.name, topic+" ") {
				continue
			}
			if !group && strings.Contains(item.name, " ") {
				continue
			}
			fmt.Fprintf(&b, "  %-17s %s\n", item.name, item.description)
		}
		b.WriteString("  help COMMAND      show a command's full reference\n")
	}
	if offline {
		b.WriteString("\nOFFLINE CHECK\n  validate version 1 JSON and plan from all steps pending\n  read a regular file or redirected stdin (-); no home or task store is opened\n  --home is accepted but ignored; no runtime or account is contacted\n  check schema and dependencies only; no task is accepted or executed\n")
	}
	b.WriteString("\nOPTIONS\n")
	for _, f := range flags {
		connectionFlag := f.name == "label" || f.name == "native-home" || f.name == "executable" || f.name == "separate"
		if c != nil && connectionFlag && topic != "login" && topic != "accounts add" {
			continue
		}
		voiceFlag := f.name == "python" || f.name == "python-site" || f.name == "model-dir" || f.name == "language" || f.name == "duration"
		if c != nil && voiceFlag && topic != "voice" && !isVoiceOperation(topic) {
			continue
		}
		if c != nil && f.name == "offline" && !strings.HasPrefix(topic, "voice") && !strings.HasPrefix(topic, "runtime") {
			continue
		}
		if c != nil && c.name != "tasks show" && f.name == "before-revision" {
			continue
		}
		if c != nil && c.name != "tasks" && c.name != "tasks list" && (f.name == "search" || f.name == "cursor") {
			continue
		}
		if c != nil && c.name != "tasks show" && c.name != "tasks" && c.name != "tasks list" && f.name == "limit" {
			continue
		}
		if c != nil && c.name != "run" && c.name != "tasks followup" && c.name != "explain" && (f.name == "model" || f.name == "account" || f.name == "effort" || f.name == "permission" || f.name == "cwd" || f.name == "demo" || f.name == "command-id") {
			if f.name != "model" || (topic != "voice" && !isVoiceOperation(topic)) {
				continue
			}
		}
		if c != nil && c.name == "explain" && f.name == "command-id" {
			continue
		}
		label := "    --" + f.name
		if f.short != "" {
			label = "-" + f.short + ", --" + f.name
		}
		if f.argument != "" {
			label += " " + f.argument
		}
		description := f.description
		if offline && f.name == "home" {
			description = "accepted but ignored for this offline check"
		}
		fmt.Fprintf(&b, "  %-25s %s\n", label, description)
	}
	if offline {
		b.WriteString("\nARGUMENTS\n  FILE    workflow JSON file; - reads redirected stdin\n  --      treat the remaining file path literally\n\nENVIRONMENT\n  UCLI_HOME  ignored for offline checking\n  NO_COLOR   disable terminal styling; keyboard navigation remains enabled\n\nFILES\n  FILE  read only; input is not saved into UCLI state\n")
	} else if topic == "backup" || topic == "migrate" {
		b.WriteString("\nARGUMENTS\n  FILE    new local file outside the live state directory; parent must exist\n  --      treat the remaining file path literally\n  no prompt or stdin is read; existing files are never overwritten\n\nENVIRONMENT\n  UCLI_HOME  set the source state directory unless --home is supplied\n  NO_COLOR   retain plain text; this build never emits color\n\nFILES\n  UCLI_HOME  read local saved task data; never use native credential stores\n  FILE       create the standalone snapshot; --json returns its hash and counts\n")
	} else {
		b.WriteString("\nARGUMENTS\n  PROMPT  supply task text; use - to read redirected stdin\n  ID      copy a task ID from ucli tasks\n  FILE    workflow JSON file; - reads redirected stdin\n  --      treat remaining arguments as literal text\n\nENVIRONMENT\n  UCLI_HOME  set the local state directory unless --home is supplied\n  NO_COLOR   disable color; --plain or TERM=dumb selects the static line interface\n\nFILES\n  UCLI_HOME  store local tasks and drafts; never use native credential stores\n")
	}
	b.WriteString("\nEXIT STATUS\n  0 success; 2 usage; 3 setup/auth; 4 capacity; 5 policy/input\n  6 validation; 7 uncertain; 8 provider; 9 internal; 124 timeout; 130 interrupt\n\nSEE ALSO\n  docs/experience.md, docs/development-slice.md\n\nREPORT BUGS\n  give the project owner your version and a redacted reproduction\n")
	return b.String()
}
