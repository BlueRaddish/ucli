package app

import (
	"context"

	"ucli.local/ucli/internal/adapters/claude"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

// ModelCatalog separates browsing from admission. No native process, credential
// read or network request happens here; dispatch still verifies its own binding.
type ModelCatalog struct {
	Models    []core.Model      `json:"models"`
	Providers []ProviderCatalog `json:"providers"`
}

type ProviderCatalog struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Installation   string `json:"installation"`
	Authentication string `json:"authentication"`
	Integration    string `json:"integration"`
	Reason         string `json:"reason"`
	NextStep       string `json:"next_step"`
	Connections    int    `json:"connections"`
}

func (a *App) Models() []core.Model {
	return append(a.localModels(), a.nativeModels()...)
}

func (a *App) ModelCatalog(ctx context.Context) (ModelCatalog, error) {
	items, err := a.LinkedConnections(ctx)
	if err != nil {
		return ModelCatalog{}, err
	}
	present := map[string]bool{}
	for _, id := range []string{"codex", "claude", "agy", "github"} {
		if err := ctx.Err(); err != nil {
			return ModelCatalog{}, err
		}
		present[id] = connections.ExecutablePresent(id)
	}
	return ModelCatalog{
		Models:    append(a.localModels(), nativeCatalogModels(items)...),
		Providers: providerCatalog(items, present),
	}, nil
}

func providerCatalog(items []connections.Connection, present map[string]bool) []ProviderCatalog {
	providers := []ProviderCatalog{
		{ID: "codex", Name: "Codex", Integration: "experimental", Reason: "A linked ChatGPT account and a reviewed Codex version are required.", NextStep: "Use the account already signed in on this computer, or sign in through Codex."},
		{ID: "claude", Name: "Claude Code", Integration: "experimental", Reason: "The adapter is reviewed for Claude Code " + claude.Version + ". Model metadata is discovered natively; unreviewed builds remain unavailable for execution.", NextStep: "Check the existing Claude sign-in. An unsupported version is not a signed-out account."},
		{ID: "agy", Name: "Antigravity (AGY)", Integration: "catalog-only", Reason: "Native model names can be inspected; AGY task execution and sign-in verification are not supported in UCLI.", NextStep: "Refresh the native model list here. Use Antigravity itself to sign in and run its models."},
		{ID: "github", Name: "GitHub", Integration: "identity-only", Reason: "Connects a local GitHub CLI identity. It supplies no AI models and is not a hosted UCLI account.", NextStep: "Use the account already signed in through GitHub CLI, or its browser sign-in."},
	}
	for i := range providers {
		p := &providers[i]
		p.Installation, p.Authentication = "not-found", "not-checked"
		if present[p.ID] {
			p.Installation = "executable-found"
		}
		for _, item := range items {
			if item.Provider != p.ID {
				continue
			}
			p.Connections++
			if p.Installation == "not-found" {
				p.Installation = "linked-path-not-checked"
			}
			// This is an aggregate browsing hint, never a choice of account.
			if p.Authentication != "authenticated" {
				p.Authentication = item.AuthState
			}
		}
	}
	return providers
}

func nativeCatalogModels(items []connections.Connection) []core.Model {
	result := []core.Model{}
	indices := map[string]int{}
	for _, item := range items {
		if item.Provider != "codex" && item.Provider != "claude" && item.Provider != "agy" {
			continue
		}
		available := item.AuthState == "authenticated" && item.Identity != "" && item.SHA256 != "" &&
			(item.Provider == "codex" && item.AuthMode == "chatgpt" || item.Provider == "claude" && item.AuthMode == "claude.ai")
		reason := "Cached native catalog; fresh identity, version, model and permission checks are required before dispatch. Listing does not prove quota or entitlement."
		if !available {
			reason = "Account is not ready in UCLI. Check or connect its native sign-in; your current selection is kept."
		}
		if item.Provider == "agy" {
			reason = "Native AGY catalog only. UCLI cannot execute this model or verify its account entitlement; use Antigravity itself."
		}
		for _, model := range item.Models {
			id := universalModelID(item.Provider, model.ID)
			var efforts []string
			for _, effort := range nativePublicEfforts(item.Provider, model.Efforts) {
				efforts = append(efforts, effort.Level)
			}
			modelAvailable, modelReason := available, reason
			if limitation := nativeModelLimitation(item, model); limitation != "" {
				modelAvailable, modelReason = false, limitation
			}
			entry := core.Model{ID: id, Label: model.Name, Runtime: item.Provider, Efforts: efforts, Available: modelAvailable, Reason: modelReason}
			if index, exists := indices[id]; exists {
				// A signed-out connection must not hide the same model offered
				// by an authenticated one. No account pin is changed here.
				if modelAvailable && !result[index].Available {
					result[index] = entry
				}
				continue
			}
			indices[id] = len(result)
			result = append(result, entry)
		}
	}
	return result
}

func nativeModelLimitation(item connections.Connection, model connections.Model) string {
	if item.Compatibility == "unknown" {
		return "Native executable or catalog compatibility is unknown. Cached model and account references were kept; execution requires review."
	}
	if model.Unavailable {
		return "The native CLI marks this model unavailable. No entitlement or quota is inferred from its listing."
	}
	if item.Provider != "claude" {
		return ""
	}
	if model.ResolvedID == "" {
		return "Native model resolution is unverified. Refresh its catalog before using this model; no alias prefix is assumed."
	}
	if model.EffortState == "unsupported" {
		return "This model reports fixed effort. UCLI currently requires a model with explicit effort choices; no effort setting was invented."
	}
	if model.EffortState != "supported" || len(nativePublicEfforts(item.Provider, model.Efforts)) == 0 {
		return "Native effort capabilities are unknown or not mapped. Refresh its catalog; explicit effort will not be lowered."
	}
	return ""
}
