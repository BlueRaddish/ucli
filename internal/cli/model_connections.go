package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

// The first layer is runtime-sized, not model-sized. Identity-only providers
// stay in Accounts; a provider without a cached catalog still has a setup path.
func modelRuntimeChoices(catalog app.ModelCatalog) []choice {
	rows := []choice{{"auto", "Auto - route among connected, supported models"}}
	seen := map[string]bool{}
	add := func(id, name string) {
		if seen[id] || id == "" {
			return
		}
		seen[id] = true
		total, ready := 0, 0
		for _, model := range catalog.Models {
			if model.Runtime == id {
				total++
				if model.Available {
					ready++
				}
			}
		}
		detail := fmt.Sprintf("%d ready / %d cached models", ready, total)
		if total == 0 {
			detail = "No cached models - open setup and discovery"
		}
		rows = append(rows, choice{"runtime:" + id, name + " - " + detail})
	}
	for _, provider := range catalog.Providers {
		if provider.Integration != "identity-only" {
			add(provider.ID, provider.Name)
		}
	}
	for _, model := range catalog.Models {
		add(model.Runtime, modelRuntimeName(catalog, model.Runtime))
	}
	return rows
}

func modelRuntimeName(catalog app.ModelCatalog, runtime string) string {
	for _, provider := range catalog.Providers {
		if provider.ID == runtime {
			return provider.Name
		}
	}
	if runtime == "demo" {
		return "Local demo"
	}
	if runtime == "muse" {
		return "Muse echo (no AI)"
	}
	return runtime
}

func modelChoices(catalog app.ModelCatalog, runtime string) []choice {
	var rows []choice
	for _, model := range catalog.Models {
		if model.Runtime != runtime {
			continue
		}
		if model.Available {
			rows = append(rows, choice{"model:" + model.ID, model.Label + " - " + model.ID + " | available in cached catalog"})
		} else {
			rows = append(rows, choice{"info:" + model.ID, model.Label + " (not ready) - " + model.ID + " | details and setup"})
		}
	}
	for _, provider := range catalog.Providers {
		if provider.ID == runtime {
			rows = append(rows, choice{"provider:" + provider.ID, provider.Name + " setup - " + installationLabel(provider.Installation) + " | " + authenticationLabel(provider.Authentication)})
		}
	}
	return append(rows, choice{"back", "Back to CLIs"})
}

func modelEffortChoices(model core.Model, catalog app.ModelCatalog) []choice {
	detail := "choose a supported effort before dispatch"
	if model.ID != "auto" && len(model.Efforts) == 0 {
		detail = "no adjustable effort advertised; runtime compatibility is checked before sending"
	}
	rows := []choice{{"auto", "Auto - " + detail}}
	levels := model.Efforts
	if model.ID == "auto" {
		for _, candidate := range catalog.Models {
			if candidate.Available {
				levels = append(levels, candidate.Efforts...)
			}
		}
	}
	seen := map[string]bool{"auto": true}
	for _, effort := range levels {
		if effort != "" && !seen[effort] {
			seen[effort] = true
			rows = append(rows, choice{effort, effort + " - advertised by the selected model"})
			if model.ID == "auto" {
				rows[len(rows)-1].label = effort + " - limit Auto to models supporting this effort"
			}
		}
	}
	return append(rows, choice{"back", "Back to models"})
}

func (s *session) chooseModelStep(ctx context.Context, title string, rows []choice, current string, memory *pickerMemory) (string, error) {
	if s.terminal != nil {
		return pickTerminalRemember(ctx, s.terminal, title, rows, current, memory)
	}
	labelled := append([]choice(nil), rows...)
	for i := range labelled {
		if labelled[i].value == current {
			labelled[i].label += " (current)"
		}
	}
	return s.choose(ctx, title, labelled, current)
}

func (s *session) selectModel(ctx context.Context) error {
	return s.selectModelCatalog(ctx, s.app.ModelCatalog)
}

func (s *session) selectModelCatalog(ctx context.Context, load func(context.Context) (app.ModelCatalog, error)) error {
	catalog, err := load(ctx)
	if err != nil {
		return connectionProblem(err)
	}
	currentRuntime := "auto"
	for _, model := range catalog.Models {
		if model.ID == s.selection.Model {
			currentRuntime = "runtime:" + model.Runtime
			break
		}
	}
	var runtimes pickerMemory
	models, efforts := map[string]*pickerMemory{}, map[string]*pickerMemory{}
	memory := func(items map[string]*pickerMemory, key string) *pickerMemory {
		if items[key] == nil {
			items[key] = &pickerMemory{}
		}
		return items[key]
	}
	for {
		runtime, err := s.chooseModelStep(ctx, "Choose a CLI", modelRuntimeChoices(catalog), currentRuntime, &runtimes)
		if err != nil || runtime == "" {
			return err
		}
		for {
			selected := core.Model{ID: "auto", Label: "Auto routing", Available: true}
			if runtime != "auto" {
				id := strings.TrimPrefix(runtime, "runtime:")
				value, err := s.chooseModelStep(ctx, modelRuntimeName(catalog, id)+" - Choose a model", modelChoices(catalog, id), "model:"+s.selection.Model, memory(models, id))
				if err != nil {
					return err
				}
				if value == "" || value == "back" {
					break
				}
				var model *core.Model
				for _, entry := range catalog.Models {
					if entry.Runtime == id && ("model:"+entry.ID == value || "info:"+entry.ID == value) {
						copy := entry
						model = &copy
						break
					}
				}
				if strings.HasPrefix(value, "provider:") || model != nil && !model.Available {
					if err := s.modelProvider(ctx, id, catalog, model); err != nil {
						return err
					}
					catalog, err = load(ctx)
					if err != nil {
						return connectionProblem(err)
					}
					continue
				}
				if model == nil || !model.Available {
					return core.Problem("model_unavailable", "That model is not ready in UCLI. Your current model and account are kept.", 3)
				}
				selected = *model
			}
			effort, err := s.chooseModelStep(ctx, "Choose effort - "+selected.Label, modelEffortChoices(selected, catalog), s.selection.Effort, memory(efforts, selected.ID))
			if err != nil {
				return err
			}
			if effort == "" || effort == "back" {
				if runtime == "auto" {
					break
				}
				continue
			}
			// Model and effort are one pending choice until this last action.
			// Persist once, preserving account/permission and the exact draft/CAS.
			previous := s.selection
			s.selection.Model, s.selection.Effort = selected.ID, effort
			if s.task != nil {
				if err := s.storeDraft(ctx, s.task.Draft, false); err != nil {
					s.selection = previous
					return err
				}
			}
			if err := s.out.text("Model selected. Model and effort saved together; your account and permission choices are kept. Native identity, version and capabilities are checked again before sending.\n"); err != nil {
				return err
			}
			return s.out.notice("Model selected: "+selected.ID+" | effort "+effort, toneSuccess)
		}
	}
}

func (s *session) modelProvider(ctx context.Context, id string, catalog app.ModelCatalog, model *core.Model) error {
	var provider app.ProviderCatalog
	for _, candidate := range catalog.Providers {
		if candidate.ID == id {
			provider = candidate
			break
		}
	}
	if provider.ID == "" && model == nil {
		return s.out.notice("Provider setup is unavailable for this entry.", toneInfo)
	}
	title := provider.Name + " setup"
	detail := provider.Reason + "\n\n" + provider.NextStep
	if model != nil {
		title = model.Label + " - not ready"
		detail = model.ID + "\n" + model.Reason + "\n\n" + detail
	}
	if provider.ID == "" {
		detail += "\nNo account setup is available for this runtime."
	}
	detail += "\n\nBrowsing does not check current sign-in or submit an AI task."
	if s.terminal != nil {
		proceed, err := modelDetails(ctx, s.terminal, title, detail, provider.ID != "")
		if err != nil || !proceed {
			return err
		}
	} else if err := s.out.text(title + "\n" + detail + "\n"); err != nil {
		return err
	}
	if provider.ID == "" {
		return nil
	}
	action := "Check existing sign-in and refresh models"
	if id == "agy" {
		action = "Refresh Antigravity model names - catalog only"
	} else if id == "github" {
		action = "Check existing GitHub CLI sign-in - no AI models"
	}
	rows := []choice{{"refresh", action}}
	if id != "agy" {
		rows = append(rows, choice{"login", "Sign in through " + provider.Name + " - browser/device steps if needed"})
	}
	value, err := s.choose(ctx, provider.Name+" - your model and draft stay unchanged", rows)
	if err != nil || value == "" {
		return err
	}
	svc, err := connections.New(s.app.Home)
	if err != nil {
		return connectionProblem(err)
	}
	items, err := svc.List(ctx)
	if err != nil {
		return connectionProblem(err)
	}
	identifier := id
	var matches []choice
	for _, item := range items {
		if item.Provider == id {
			matches = append(matches, choice{item.ID, item.Label + " | " + authenticationLabel(item.AuthState)})
		}
	}
	if len(matches) == 1 {
		identifier = matches[0].value
	} else if len(matches) > 1 {
		identifier, err = s.choose(ctx, "Which linked account should be checked?", matches)
		if err != nil || identifier == "" {
			return err
		}
	}
	command := "login"
	if value == "refresh" {
		command = "accounts add"
		if identifier != id {
			command = "accounts status"
		}
	}
	p := invocation{command: command, id: identifier, home: s.app.Home}
	// Refresh/status uses the same owned, cancellable operation as sign-in.
	if command == "accounts status" {
		var screen taskPickerTerminal
		if s.terminal != nil {
			screen = s.terminal
		}
		item, err := performLoginOperation(ctx, func(ctx context.Context, _ func(connections.LoginEvent) error) (connections.Connection, error) {
			return svc.Status(ctx, identifier)
		}, s.out, screen)
		if err == nil {
			err = showConnection(item, s.out)
		}
		return s.loginResult(err)
	}
	return s.loginResult(applyConnectionCommand(ctx, svc, p, s.out, s.terminal))
}

// Keep the full selected-model limitation readable before offering provider
// actions. Picker descriptions are previews and may be clipped to one row.
func modelDetails(ctx context.Context, terminal pickerTerminal, title, detail string, setup bool) (bool, error) {
	scroll, wrappedWidth := 0, 0
	var rows []terminalLine
	for {
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		width = max(1, width-1)
		budget := max(1, height-1)
		usable := width >= 16 && budget >= 3
		bodyRows := max(1, budget-2)
		if width != wrappedWidth {
			rows = nil
			for _, line := range strings.Split(detail, "\n") {
				rows = append(rows, wrapTerminalText(line, width)...)
			}
			wrappedWidth = width
		}
		scroll = min(max(0, scroll), max(0, len(rows)-bodyRows))
		frame := terminalFrame{CursorRow: -1}
		if usable {
			frame.Lines = append(frame.Lines, terminalLine{Text: clipCells(inlineText(title), width)})
			frame.Lines = append(frame.Lines, rows[scroll:min(len(rows), scroll+bodyRows)]...)
			hint := fmt.Sprintf("Enter setup | Esc back | Up/Down scroll (%d-%d/%d)", scroll+1, min(len(rows), scroll+bodyRows), len(rows))
			if width < 39 {
				hint = "Enter setup / Esc / Up/Down"
			} else if width < 65 {
				hint = "Enter setup | Esc back | Up/Down scroll"
			}
			if width < 26 {
				hint = "Enter / Esc"
			}
			if !setup {
				hint = strings.Replace(hint, "Enter setup", "Enter back", 1)
			}
			frame.Lines = append(frame.Lines, terminalLine{Text: hint, Dim: true})
		} else {
			frame.Lines = []terminalLine{{Text: "Resize / Esc"}}
		}
		if err := terminal.paint(frame); err != nil {
			return false, err
		}
		event, err := terminal.next(ctx)
		if err != nil {
			return false, err
		}
		switch event.kind {
		case keyEnter:
			if usable {
				return setup, nil
			}
		case keyEscape, keyLeft:
			return false, nil
		case keyUp:
			scroll--
		case keyDown:
			scroll++
		case keyPageUp:
			scroll -= bodyRows
		case keyPageDown:
			scroll += bodyRows
		case keyHome:
			scroll = 0
		case keyEnd:
			scroll = len(rows)
		case keyEOF:
			return false, io.EOF
		case keyInterrupt:
			return false, context.Canceled
		}
	}
}

func installationLabel(state string) string {
	switch state {
	case "executable-found":
		return "installed (version not checked)"
	case "linked-path-not-checked":
		return "saved connection (installation not checked)"
	default:
		return "not found - install the native CLI or specify its path"
	}
}

func authenticationLabel(state string) string {
	switch state {
	case "authenticated":
		return "signed in at last check"
	case "signed-out":
		return "signed out at last check"
	case "identity-changed":
		return "native account changed - reconnect explicitly"
	case "unknown":
		return "sign-in could not be verified"
	default:
		return "sign-in not checked"
	}
}
