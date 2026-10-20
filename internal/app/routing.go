package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/routing"
)

const museVersion = "1.4.7-R6101.1"
const museSHA256 = "2d1ac7af0a9ea77643cb8690eee70f52f0f8476c46b194f143c831f481aea59d"

// Discovery is local and cheap; it neither executes an updating shim nor hashes
// a large runtime during help or model browsing. Execute verifies the binary.
func museExecutable() (string, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return "", core.Problem("runtime_unavailable", "Muse echo is verified only with the pinned Windows x64 runtime so far.", 3)
	}
	path := os.Getenv("UCLI_MUSE_EXE")
	if path == "" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			path = filepath.Join(base, "Programs", "muse", "muse-bin-"+museVersion+".exe")
		}
	}
	if !filepath.IsAbs(path) {
		return "", core.Problem("runtime_unavailable", "Pinned Muse echo runtime not found. UCLI_MUSE_EXE may name its absolute native executable; no shim is launched.", 3)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 512*1024*1024 {
		return "", core.Problem("runtime_unavailable", "Pinned Muse echo executable is missing, linked, or outside its size bound.", 3)
	}
	return path, nil
}

func verifyMuseExecutable() (string, error) {
	path, err := museExecutable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", core.Problem("runtime_unavailable", "Cannot read the pinned Muse echo executable.", 3)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, 512*1024*1024+1)); err != nil || hex.EncodeToString(h.Sum(nil)) != museSHA256 {
		return "", core.Problem("runtime_unverified", "Muse binary differs from the verified echo version. No process was started; adapter verification is required for this version.", 3)
	}
	return path, nil
}

func (a *App) localModels() []core.Model {
	_, err := museExecutable()
	reason := "Pinned runtime found; hash verified at launch. Explicit echo only, no AI, account or reasoning."
	if err != nil {
		reason = err.Error()
	}
	models := []core.Model{
		{ID: "demo/fast", Label: "Fast local demonstration (simulated)", Runtime: "demo", Efforts: []string{"low", "medium"}, Available: true, Reason: "No AI inference or provider quota"},
		{ID: "demo/deep", Label: "Detailed local demonstration (simulated)", Runtime: "demo", Efforts: []string{"medium", "high", "max"}, Available: true, Reason: "No AI inference or provider quota"},
		{ID: "muse/echo", Label: "Muse native echo (no AI)", Runtime: "muse", Efforts: []string{}, Available: err == nil, Reason: reason},
	}
	return models
}
func (a *App) Accounts() []core.Account {
	accounts := []core.Account{
		{ID: "demo", Label: "Local demonstration - no provider account", Runtime: "demo", AuthMode: "none", Status: "synthetic"},
		{ID: "none", Label: "Isolated Muse echo - no provider login", Runtime: "muse", AuthMode: "none", Status: "no-auth-echo-only"},
	}
	items, err := a.LinkedConnections(context.Background())
	if err == nil {
		for _, item := range items {
			accounts = append(accounts, core.Account{ID: item.ID, Label: item.Label, Runtime: item.Provider, AuthMode: item.AuthMode, Status: item.AuthState})
		}
	}
	return accounts
}

func (a *App) Explain(ctx context.Context, opt Options) (core.Route, error) {
	route, _, err := a.resolveRoute(ctx, opt, false)
	return route, err
}

func (a *App) resolveRoute(ctx context.Context, opt Options, verifyRuntime bool) (core.Route, string, error) {
	if opt.Input != "" {
		if err := core.ValidateInput(opt.Input); err != nil {
			return core.Route{}, "", err
		}
	}
	w, task, err := a.resolveWorkspace(ctx, opt.TaskID, opt.CWD)
	if err != nil {
		return core.Route{}, "", err
	}
	selection := inherit(task.Selection, opt.Selection)
	if err = core.ValidateSelection(selection); err != nil {
		return core.Route{}, "", err
	}
	if !opt.Demo {
		route, executable, _, err := a.resolveNativeRoute(ctx, opt)
		return route, executable, err
	}
	if selection.Permission != "read-only" {
		return core.Route{}, "", core.Problem("unsupported_permission", "Demonstrations support read-only work only; they never edit your workspace.", 5)
	}
	model := selection.Model
	why := "Explicit local demonstration: no AI model, tools, credentials or provider usage."
	if model == "auto" {
		model = "demo/fast"
		lower := strings.ToLower(opt.Input)
		if len(opt.Input) > 400 || strings.Contains(lower, "architecture") || strings.Contains(lower, "review") || strings.Contains(lower, "debug") {
			model = "demo/deep"
		}
		why += " Auto model uses a local length/keyword rule, not a decision agent; it never selects native echo."
	}
	var selected *core.Model
	for _, m := range a.Models() {
		if m.ID == model {
			copy := m
			selected = &copy
			break
		}
	}
	if selected == nil {
		return core.Route{}, "", core.Problem("model_unavailable", "Unknown model. Run ucli models to see experimental choices.", 3)
	}
	account, provider, mode, effort := "demo", "local", "demo", "medium"
	executable := ""
	if model == "demo/deep" {
		effort = "high"
	}
	var efforts []routing.Effort
	if model == "muse/echo" {
		account, provider, mode, effort = "none", "echo", "echo", ""
		if selection.Effort != "auto" {
			return core.Route{}, "", core.Problem("effort_unsupported", "Muse echo has no reasoning effort. Use auto; its effective effort is none.", 5)
		}
		if verifyRuntime {
			executable, err = verifyMuseExecutable()
		} else {
			executable, err = museExecutable()
		}
		if err != nil {
			return core.Route{}, "", err
		}
		why = "Explicit isolated native echo: no AI inference, provider login or user workspace access. Native session is ephemeral; follow-ups start a fresh native session. Disable flags are not an OS sandbox."
	} else {
		for _, level := range selected.Efforts {
			efforts = append(efforts, routing.Effort{Level: level, Native: level})
		}
	}
	if selection.Account != "auto" && selection.Account != account {
		return core.Route{}, "", core.Problem("account_unavailable", "This route uses only account "+account+". Choose auto or that account explicitly.", 3)
	}
	if model != "muse/echo" && selection.Effort != "auto" && selection.Effort != "max" {
		ok := false
		for _, level := range selected.Efforts {
			ok = ok || level == selection.Effort
		}
		if !ok {
			return core.Route{}, "", core.Problem("effort_unsupported", fmt.Sprintf("%s supports %s; choose one or auto.", model, strings.Join(selected.Efforts, ", ")), 5)
		}
	}
	now := time.Now().UTC()
	pool := routing.PoolKey{Scope: "local-demonstration", Bucket: "unmetered"}
	candidate := routing.Candidate{ID: model, Route: core.Route{Runtime: selected.Runtime, Provider: provider, Model: model, Account: account, Mode: mode}, Workspace: w,
		Admitted: true, Available: true, WorkspaceAllowed: true, AuthMode: "none", PrimaryModelFixed: true,
		Advertised: true, CatalogObservedAt: now, Entitlement: "not-required", Billing: "unmetered", Efforts: efforts, DefaultEffort: effort,
		Permissions: map[string]map[string]routing.Capability{"filesystem": {"read-only": {Support: "mapped"}}},
		Pools:       []routing.PoolUse{{Key: pool, Unit: "local-runs"}}}
	decision, err := routing.Resolve(routing.Request{Requested: selection, Workspace: w, AllowedAccounts: []string{account}, Candidates: []routing.Candidate{candidate},
		Pools: []routing.PoolSnapshot{{Key: pool, Unit: "local-runs", Unmetered: true, ObservedAt: now}}, Now: now, BoundedAttempt: true})
	if err != nil {
		return core.Route{}, "", err
	}
	route := decision.Effective
	if model == "muse/echo" {
		route.Effort = "none"
	}
	route.Reasons = append([]string{why}, route.Reasons...)
	return route, executable, nil
}
