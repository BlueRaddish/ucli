package cli

import (
	"context"
	"fmt"
	"strings"

	"ucli.local/ucli/internal/connections"
)

// Browsing can read saved metadata, but cannot check or change native sign-in.
type loginCache interface {
	List(context.Context) ([]connections.Connection, error)
	Get(context.Context, string) (connections.Connection, error)
}

type loginPresentation struct {
	provider, label, status, next, details string
}

func presentConnection(item connections.Connection) loginPresentation {
	view := loginPresentation{provider: item.Provider, label: inlineText(item.Label), status: authenticationLabel(item.AuthState)}
	switch item.AuthState {
	case "authenticated":
		view.next = "Choose a model in /models. Sign-in and compatibility are checked again before sending."
	case "signed-out":
		view.next = "Choose Sign in to follow the provider's browser or device steps."
	case "identity-changed":
		view.next = "The saved account no longer matches. Unlink this connection and reconnect the intended account."
	default:
		view.next = "Check the existing account first. A new sign-in may not be needed."
	}
	view.details = "UCLI uses the native client's sign-in. It does not copy credentials. Checking or signing in does not send an AI prompt."
	if item.Compatibility == "unknown" {
		view.status += "; app compatibility not verified"
		view.next = "Check the existing account to verify the installed app. A new sign-in does not fix an unsupported app version."
		if item.AuthState == "identity-changed" {
			view.next = "The saved account no longer matches. Unlink this connection and reconnect the intended account."
		}
	}
	switch item.Provider {
	case "agy":
		view.status = "model list only; sign-in not verified"
		view.next = fmt.Sprintf("%d model names saved. Sending tasks through AGY is not supported in UCLI.", len(item.Models))
		view.details = "Inspect reads Antigravity's model names only. Use Antigravity itself to sign in or run its models."
	case "github":
		view.next = "GitHub connects a local CLI identity only; it supplies no AI models."
		if item.AuthState == "signed-out" {
			view.next = "Choose Sign in for GitHub's browser or device steps. GitHub supplies no AI models."
		} else if item.AuthState == "identity-changed" {
			view.next = "The saved GitHub account changed. Unlink and reconnect the intended account; GitHub supplies no AI models."
		}
		view.details = "This uses the GitHub CLI's local sign-in. It does not create a hosted UCLI account or enable cloud sync."
	}
	return view
}

func loginProviderRows(items []connections.Connection) []choice {
	rows := []choice{
		{"codex", "Codex - ChatGPT account"},
		{"claude", "Claude Code - Claude account"},
		{"github", "GitHub - identity only, no AI models"},
		{"agy", "Antigravity (AGY) - model list only"},
	}
	for i := range rows {
		count := 0
		for _, item := range items {
			if item.Provider == rows[i].value {
				count++
			}
		}
		if count != 0 {
			rows[i].label += fmt.Sprintf(" | %d saved", count)
		} else {
			rows[i].label += " | not linked here"
		}
	}
	return rows
}

func connectionActionRows(provider string) []choice {
	if provider == "agy" {
		return []choice{
			{"check", "Inspect model names - read Antigravity's list; no sign-in or task"},
			{"details", "Details - what this connection can do"},
		}
	}
	return []choice{
		{"check", "Use existing account - Use the account already signed in on this computer; check and link only"},
		{"login", "Sign in - follow " + providerName(provider) + " browser or device steps if needed"},
		{"details", "Details - what checking and signing in do"},
	}
}

func cachedLoginPresentation(ctx context.Context, identifier string, cache loginCache) (loginPresentation, error) {
	provider := nativeProvider(identifier)
	if provider == "" {
		if cache == nil {
			return loginPresentation{}, usageError("choose a provider or a saved connection")
		}
		item, err := cache.Get(ctx, identifier)
		if err != nil {
			return loginPresentation{}, err
		}
		return presentConnection(item), nil
	}
	view := presentConnection(connections.Connection{Provider: provider})
	view.status = "check an existing account or sign in"
	view.details += " Use existing account checks the native client's current account and links it here. Sign in checks first, then starts browser or device steps only if needed. Your draft and model choices are kept."
	if provider == "agy" {
		view = presentConnection(connections.Connection{Provider: provider})
		view.next = "Inspect the native model list. Sending tasks through AGY is not supported in UCLI."
	}
	if cache != nil {
		items, err := cache.List(ctx)
		if err != nil {
			return loginPresentation{}, err
		}
		count := 0
		for _, item := range items {
			if item.Provider == provider {
				count++
			}
		}
		if count > 0 {
			view.details += fmt.Sprintf(" %d connections are saved for this provider. This action checks its current default account; it does not switch saved accounts.", count)
		}
	}
	return view, nil
}

func (s *session) chooseLoginProvider(ctx context.Context, caches ...loginCache) (string, error) {
	var items []connections.Connection
	if len(caches) != 0 && caches[0] != nil {
		var err error
		items, err = caches[0].List(ctx)
		if err != nil {
			return "", err
		}
	}
	return s.choose(ctx, "Connect an account - choose a provider", loginProviderRows(items))
}

func (s *session) chooseConnectionAction(ctx context.Context, identifier string, caches ...loginCache) (string, error) {
	var cache loginCache
	if len(caches) != 0 {
		cache = caches[0]
	}
	view, err := cachedLoginPresentation(ctx, identifier, cache)
	if err != nil {
		return "", err
	}
	title := providerName(view.provider)
	if view.label != "" && view.label != title {
		title += " / " + view.label
	}
	title += " - " + view.status
	for {
		action, err := s.choose(ctx, title, connectionActionRows(view.provider))
		if err != nil || action != "details" {
			return action, err
		}
		detail := strings.Join([]string{view.status, view.next, view.details, "Saved status is not a live check. Native sign-in is unchanged until you choose an action."}, "\n\n")
		if s.terminal != nil {
			_, err = modelDetails(ctx, s.terminal, providerName(view.provider)+" connection details", detail, false)
		} else {
			err = s.out.text(detail + "\n")
		}
		if err != nil {
			return "", err
		}
	}
}
