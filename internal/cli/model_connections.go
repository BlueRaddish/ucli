package cli

import (
	"context"
	"fmt"
	"strings"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func modelChoices(catalog app.ModelCatalog) []choice {
	rows := []choice{{"model:auto", "Auto - choose among connected, supported models"}}
	for _, model := range catalog.Models {
		if model.Available {
			rows = append(rows, choice{"model:" + model.ID, model.Label + " - " + model.ID + " | available in cached catalog"})
		} else {
			rows = append(rows, choice{"info:" + model.ID, model.Label + " (not ready) - " + model.ID + " | details and setup"})
		}
	}
	for _, provider := range catalog.Providers {
		rows = append(rows, choice{"provider:" + provider.ID, provider.Name + " setup - " + installationLabel(provider.Installation) + " | " + authenticationLabel(provider.Authentication)})
	}
	return rows
}

func (s *session) selectModel(ctx context.Context) error {
	for {
		catalog, err := s.app.ModelCatalog(ctx)
		if err != nil {
			return connectionProblem(err)
		}
		value, err := s.choose(ctx, "Choose a model", modelChoices(catalog), "model:"+s.selection.Model)
		if err != nil || value == "" {
			return err
		}
		if !strings.HasPrefix(value, "model:") {
			provider := strings.TrimPrefix(value, "provider:")
			if strings.HasPrefix(value, "info:") {
				provider = ""
				for _, model := range catalog.Models {
					if model.ID == strings.TrimPrefix(value, "info:") {
						provider = model.Runtime
						break
					}
				}
			}
			if err := s.modelProvider(ctx, provider, catalog); err != nil {
				return err
			}
			continue
		}
		model := strings.TrimPrefix(value, "model:")
		// Check the snapshot again rather than interpreting arbitrary choice
		// values as model IDs. Browsing a blocked entry never changes a pin.
		allowed := model == "auto"
		for _, entry := range catalog.Models {
			allowed = allowed || entry.ID == model && entry.Available
		}
		if !allowed {
			return core.Problem("model_unavailable", "That model is not ready in UCLI. Your current model and account are kept.", 3)
		}
		previous := s.selection
		s.selection.Model = model
		if s.task != nil && s.task.Draft != "" {
			if err := s.storeDraft(ctx, s.task.Draft, false); err != nil {
				s.selection = previous
				return err
			}
		}
		if err := s.out.text("Model selected. Your account, effort and permission choices are kept. Native identity, version and capabilities are checked again before sending.\n"); err != nil {
			return err
		}
		return s.out.notice("Model selected: "+model, toneSuccess)
	}
}

func (s *session) modelProvider(ctx context.Context, id string, catalog app.ModelCatalog) error {
	var provider app.ProviderCatalog
	for _, candidate := range catalog.Providers {
		if candidate.ID == id {
			provider = candidate
			break
		}
	}
	if provider.ID == "" {
		return s.out.notice("This local demonstration needs no provider sign-in.", toneInfo)
	}
	if err := s.out.text(fmt.Sprintf("%s\n%s\n%s\nBrowsing does not check current sign-in or submit an AI task.\n", provider.Name, provider.Reason, provider.NextStep)); err != nil {
		return err
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
