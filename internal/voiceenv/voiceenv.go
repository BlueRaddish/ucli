// Package voiceenv resolves dependencies only when an explicit voice request
// calls Ensure. It never imports a model, captures audio, or changes global Python.
package voiceenv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"ucli.local/ucli/internal/core"
)

type Options struct {
	Python, SiteDir                string
	Offline, AllowInstall, Capture bool
}
type Runtime struct {
	Python, SiteDir, Source string
	Versions                map[string]string
}
type receipt struct {
	Format                             int
	Key, Directory, PythonVersion, ABI string
	Capture                            bool
	Runtime                            Runtime
}
type hooks struct {
	probe    func(context.Context, string, string) (report, error)
	install  func(context.Context, string, string, bool) error
	lookPath func(string) (string, error)
}

func problem(code, message string) error {
	exit := 3
	if code == "voice_process_cleanup" {
		exit = 9
	}
	return core.Problem(code, message, exit)
}

// Ensure must be called only for deliberate Voice use. Existing interpreter/site
// selection is read-only. Install is an explicit opt-in, refused while offline.
func Ensure(ctx context.Context, home string, options Options) (Runtime, error) {
	return ensure(ctx, home, options, hooks{probe, install, exec.LookPath})
}

func ensure(ctx context.Context, home string, o Options, h hooks) (Runtime, error) {
	if ctx == nil {
		return Runtime{}, problem("voice_env_input", "Voice dependency resolution requires a context.")
	}
	if err := ctx.Err(); err != nil {
		return Runtime{}, err
	}
	root, err := physicalHome(home)
	if err != nil {
		return Runtime{}, err
	}
	base := filepath.Join(root, "voice-env-v1")
	if o.SiteDir != "" {
		if err = ordinaryDirectory(o.SiteDir); err != nil {
			return Runtime{}, err
		}
	}
	python := o.Python
	if python != "" && (!filepath.IsAbs(python) || !validText(python, 32768)) {
		return Runtime{}, problem("voice_python_missing", "Choose an absolute Python interpreter path.")
	}
	// A prepared site is preferred over PATH discovery, but never overrides an
	// explicitly selected external site or a different explicit interpreter.
	if o.SiteDir == "" {
		prepared, found, err := prepared(ctx, base, python, o.Capture, h.probe)
		if err != nil {
			return Runtime{}, err
		}
		if found {
			return prepared, nil
		}
	}
	// PATH can be a Windows execution alias. The probe reports sys.executable;
	// require an actual owned probe of that regular path too, since an execution
	// alias can work while its packaged Program Files target denies CreateProcess.
	observed, err := selectPython(ctx, python, o.SiteDir, h)
	if err != nil {
		return Runtime{}, err
	}
	python = observed.Python
	if o.SiteDir != "" {
		if !compatible(observed.Selected, o.Capture) {
			return Runtime{}, problem("voice_dependencies_missing", "Selected package directory lacks compatible voice dependencies and was left untouched. Choose another directory or clear the override to allow private setup.")
		}
		return Runtime{python, o.SiteDir, "explicit-site", observed.Selected.Versions}, nil
	}
	if cached, found, err := prepared(ctx, base, python, o.Capture, h.probe); err != nil {
		return Runtime{}, err
	} else if found {
		return cached, nil
	}
	if compatible(observed.Global, o.Capture) {
		return Runtime{python, "", "interpreter", observed.Global.Versions}, nil
	}
	if compatible(observed.Selected, o.Capture) {
		if err = ordinaryDirectory(observed.UserSite); err != nil {
			return Runtime{}, err
		}
		return Runtime{python, observed.UserSite, "user-site", observed.Selected.Versions}, nil
	}
	if o.Offline || !o.AllowInstall {
		return Runtime{}, missing(o)
	}
	if !observed.Global.Modules["pip"] {
		return Runtime{}, problem("voice_pip_missing", "Selected Python has no isolated global pip. Install pip separately or select an interpreter with voice dependencies; UCLI will not run ensurepip.")
	}
	return prepare(ctx, base, observed, o.Capture, h)
}

func selectPython(ctx context.Context, explicit, site string, h hooks) (report, error) {
	candidates := []string{explicit}
	if explicit == "" {
		candidates = nil
		for _, name := range []string{"python", "python3"} {
			path, err := h.lookPath(name)
			if err == nil && filepath.IsAbs(path) {
				duplicate := false
				for _, prior := range candidates {
					duplicate = duplicate || samePath(prior, path)
				}
				if !duplicate {
					candidates = append(candidates, path)
				}
			}
		}
	}
	last := problem("voice_python_missing", "Python was not found. Select an existing Python interpreter; UCLI does not install Python.")
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return report{}, err
		}
		observed, err := h.probe(ctx, candidate, site)
		if err == nil {
			err = ordinaryPython(observed.Python)
		}
		if err == nil && !samePath(candidate, observed.Python) {
			var resolved report
			resolved, err = h.probe(ctx, observed.Python, site)
			if err == nil && (resolved.Python != observed.Python || resolved.PythonVersion != observed.PythonVersion || resolved.ABI != observed.ABI) {
				err = problem("voice_python_changed", "Resolved Python identity differed from its discovery probe.")
			}
			if err == nil {
				observed = resolved
			}
		}
		if err == nil {
			return observed, nil
		}
		var typed *core.Error
		if explicit != "" || errors.As(err, &typed) && typed.Code == "voice_process_cleanup" {
			return report{}, err
		}
		last = err
	}
	return report{}, last
}

func missing(o Options) error {
	if o.Offline {
		return problem("voice_dependencies_offline", "Compatible voice dependencies are missing. Offline mode permits existing packages only.")
	}
	return problem("voice_dependencies_missing", "Voice needs faster-whisper 1.x, ctranslate2 4.x, numpy, tokenizers, huggingface-hub, and sounddevice 0.x for capture. Select an existing site or explicitly allow first-use wheel installation.")
}

func planKey(r report, capture bool) string {
	data, _ := json.Marshal([]any{1, r.Python, r.PythonVersion, r.ABI, capture, "faster-whisper==1.2.1", "sounddevice==0.5.6"})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func prepare(ctx context.Context, base string, observed report, capture bool, h hooks) (result Runtime, err error) {
	key := planKey(observed, capture)
	manifest := filepath.Join(base, key+".json")
	if err = checkPath(manifest); err != nil {
		return result, err
	}
	if _, statErr := os.Lstat(manifest); statErr == nil {
		return verifyPrepared(ctx, base, manifest, "", capture, h.probe)
	} else if !os.IsNotExist(statErr) {
		return result, problem("voice_env_cache", "Prepared voice metadata could not be inspected.")
	}
	if err = privateDirs(base); err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(base, ".site-"+key+"-")
	if err != nil {
		return result, problem("voice_env_cache", "Private dependency stage could not be created.")
	}
	published := false
	defer func() {
		if !published {
			var typed *core.Error
			if errors.As(err, &typed) && typed.Code == "voice_process_cleanup" {
				return
			}
			if cleanup := removeStage(base, stage); cleanup != nil {
				err = errors.Join(err, cleanup)
			}
		}
	}()
	if err = secureCreated(stage, true); err != nil {
		return result, problem("voice_env_cache", "Private dependency stage could not be secured.")
	}
	site := filepath.Join(stage, "site")
	if err = os.Mkdir(site, 0700); err != nil {
		return result, problem("voice_env_cache", "Private dependency site could not be created.")
	}
	if err = secureCreated(site, true); err != nil {
		return result, problem("voice_env_cache", "Private dependency site could not be secured.")
	}
	if err = h.install(ctx, observed.Python, site, capture); err != nil {
		return result, err
	}
	if err = checkTree(stage); err != nil {
		return result, err
	}
	post, err := h.probe(ctx, observed.Python, site)
	if err != nil {
		return result, err
	}
	if post.Python != observed.Python || post.PythonVersion != observed.PythonVersion || post.ABI != observed.ABI || !compatible(post.Selected, capture) {
		return result, problem("voice_dependencies_incompatible", "Installed wheel metadata does not satisfy the selected interpreter and voice dependency requirements.")
	}
	result = Runtime{observed.Python, site, "prepared-site", post.Selected.Versions}
	entry := receipt{1, key, filepath.Base(stage), post.PythonVersion, post.ABI, capture, result}
	data, _ := json.Marshal(entry)
	owned := filepath.Join(stage, ".ucli-voiceenv.json")
	if err = writeOwned(owned, data); err != nil {
		return Runtime{}, err
	}
	if err = ctx.Err(); err != nil {
		return Runtime{}, err
	}
	if err = checkPath(manifest); err != nil {
		return Runtime{}, err
	}
	if err = os.Link(owned, manifest); err != nil {
		return verifyPrepared(ctx, base, manifest, observed.Python, capture, h.probe)
	}
	published = true
	return result, nil
}

func prepared(ctx context.Context, base, python string, capture bool, probeFn func(context.Context, string, string) (report, error)) (Runtime, bool, error) {
	if err := checkPath(filepath.Join(base, "placeholder")); err != nil {
		return Runtime{}, false, err
	}
	directory, err := os.Open(base)
	if os.IsNotExist(err) {
		return Runtime{}, false, nil
	}
	if err != nil {
		return Runtime{}, false, problem("voice_env_cache", "Prepared dependency cache could not be read.")
	}
	entries, readErr := directory.ReadDir(257)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > 256 {
		return Runtime{}, false, problem("voice_env_cache", "Prepared dependency cache exceeds its directory bound or could not be read.")
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if len(name) == 69 && strings.HasSuffix(name, ".json") {
			if _, err := hex.DecodeString(name[:64]); err == nil {
				paths = append(paths, filepath.Join(base, name))
			}
		}
	}
	if len(paths) > 64 {
		return Runtime{}, false, problem("voice_env_cache", "Prepared dependency cache exceeds its 64-environment bound; inspect it before adding more.")
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Runtime{}, false, err
		}
		entry, err := readReceipt(base, path)
		if err != nil {
			return Runtime{}, false, err
		}
		if entry.Capture != capture || python != "" && !samePath(entry.Runtime.Python, python) {
			continue
		}
		value, err := verifyPrepared(ctx, base, path, python, capture, probeFn)
		return value, true, err
	}
	return Runtime{}, false, nil
}

func verifyPrepared(ctx context.Context, base, path, python string, capture bool, probeFn func(context.Context, string, string) (report, error)) (Runtime, error) {
	entry, err := readReceipt(base, path)
	if err != nil {
		return Runtime{}, err
	}
	if entry.Capture != capture || python != "" && !samePath(entry.Runtime.Python, python) {
		return Runtime{}, problem("voice_env_conflict", "Existing prepared dependencies belong to a different selection; they were left untouched.")
	}
	if err = ordinaryPython(entry.Runtime.Python); err != nil {
		return Runtime{}, err
	}
	if err = checkTree(filepath.Dir(entry.Runtime.SiteDir)); err != nil {
		return Runtime{}, err
	}
	post, err := probeFn(ctx, entry.Runtime.Python, entry.Runtime.SiteDir)
	if err != nil {
		return Runtime{}, err
	}
	if post.Python != entry.Runtime.Python || post.PythonVersion != entry.PythonVersion || post.ABI != entry.ABI || planKey(post, capture) != entry.Key || !compatible(post.Selected, capture) || !reflect.DeepEqual(post.Selected.Versions, entry.Runtime.Versions) {
		return Runtime{}, problem("voice_env_changed", "Prepared dependency metadata changed; it was left untouched. Select a different site or inspect the retained environment.")
	}
	return entry.Runtime, nil
}

func readReceipt(base, path string) (receipt, error) {
	data, err := readOwned(path)
	if err != nil {
		return receipt{}, err
	}
	var entry receipt
	if json.Unmarshal(data, &entry) != nil {
		return entry, problem("voice_env_cache", "Existing dependency manifest is invalid; it was left untouched.")
	}
	canonical, _ := json.Marshal(entry)
	if !bytes.Equal(data, canonical) || entry.Format != 1 || len(entry.Key) != 64 || filepath.Base(path) != entry.Key+".json" || filepath.Base(entry.Directory) != entry.Directory || !strings.HasPrefix(entry.Directory, ".site-"+entry.Key+"-") || entry.Runtime.Source != "prepared-site" || entry.Runtime.SiteDir != filepath.Join(base, entry.Directory, "site") {
		return receipt{}, problem("voice_env_cache", "Existing dependency manifest is not an owned complete environment; it was left untouched.")
	}
	owned, err := readOwned(filepath.Join(base, entry.Directory, ".ucli-voiceenv.json"))
	if err != nil || !bytes.Equal(owned, data) {
		return receipt{}, problem("voice_env_cache", "Prepared dependency directory lacks its matching completion manifest.")
	}
	return entry, nil
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
