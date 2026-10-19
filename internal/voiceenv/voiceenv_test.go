package voiceenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func fixtureReport(t *testing.T, ready bool) report {
	t.Helper()
	python, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := profile{Versions: map[string]string{}, Modules: map[string]bool{}}
	for name, version := range map[string]string{"faster-whisper": "1.2.1", "ctranslate2": "4.7.0", "numpy": "2.3.0", "tokenizers": "0.22.0", "huggingface-hub": "0.35.0", "sounddevice": "0.5.6", "pip": "25.2"} {
		p.Modules[name] = ready || name == "pip"
		if p.Modules[name] {
			p.Versions[name] = version
		}
	}
	return report{1, python, "3.13.14", "cp313", t.TempDir(), p, p}
}
func fakeHooks(r report) hooks {
	return hooks{
		probe:    func(context.Context, string, string) (report, error) { return r, nil },
		install:  func(context.Context, string, string, bool) error { return errors.New("unexpected install") },
		lookPath: func(string) (string, error) { return r.Python, nil },
	}
}
func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestExistingDependenciesAndMissingModesDoNotCreateHome(t *testing.T) {
	for _, kind := range []string{"global", "user", "explicit", "missing", "offline", "no-pip", "capture-missing", "tokenizers-missing", "hub-missing"} {
		t.Run(kind, func(t *testing.T) {
			r := fixtureReport(t, true)
			h := fakeHooks(r)
			o := Options{Python: r.Python, Capture: true}
			home := filepath.Join(t.TempDir(), "new-home")
			switch kind {
			case "user":
				r.Global = fixtureReport(t, false).Global
			case "explicit":
				o.SiteDir = t.TempDir()
			case "missing", "offline", "no-pip":
				r = fixtureReport(t, false)
				o.Python = r.Python
			case "capture-missing":
				delete(r.Global.Versions, "sounddevice")
				r.Global.Modules["sounddevice"] = false
			case "tokenizers-missing":
				delete(r.Global.Versions, "tokenizers")
				r.Global.Modules["tokenizers"] = false
			case "hub-missing":
				delete(r.Global.Versions, "huggingface-hub")
				r.Global.Modules["huggingface-hub"] = false
			}
			if kind == "offline" {
				o.Offline = true
				o.AllowInstall = true
			}
			if kind == "no-pip" {
				o.AllowInstall = true
				r.Global.Modules["pip"] = false
			}
			h.probe = func(context.Context, string, string) (report, error) { return r, nil }
			got, err := ensure(context.Background(), home, o, h)
			switch kind {
			case "global", "user", "explicit":
				if err != nil {
					t.Fatal(err)
				}
				source := map[string]string{"global": "interpreter", "user": "user-site", "explicit": "explicit-site"}[kind]
				if got.Source != source || got.Python != r.Python {
					t.Fatalf("wrong route %+v", got)
				}
			case "offline":
				errorCode(t, err, "voice_dependencies_offline")
			case "no-pip":
				errorCode(t, err, "voice_pip_missing")
			default:
				errorCode(t, err, "voice_dependencies_missing")
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("read-only resolution created home")
			}
		})
	}
}

func TestOwnedPreparationReuseFailureAndForeignPreservation(t *testing.T) {
	r := fixtureReport(t, false)
	ready := fixtureReport(t, true)
	ready.Python = r.Python
	ready.UserSite = r.UserSite
	h := fakeHooks(r)
	installs := 0
	h.probe = func(_ context.Context, python, site string) (report, error) {
		if site != "" {
			return ready, nil
		}
		return r, nil
	}
	h.install = func(_ context.Context, python, site string, capture bool) error {
		installs++
		return os.WriteFile(filepath.Join(site, "synthetic-wheel.txt"), []byte("not an executable package"), 0600)
	}
	home := filepath.Join(t.TempDir(), "home")
	got, err := ensure(context.Background(), home, Options{Python: r.Python, AllowInstall: true, Capture: true}, h)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "prepared-site" || installs != 1 || !reflect.DeepEqual(got.Versions, ready.Selected.Versions) {
		t.Fatalf("bad preparation %+v count%d", got, installs)
	}
	h.lookPath = func(string) (string, error) { t.Fatal("prepared environment unexpectedly used PATH"); return "", nil }
	again, err := ensure(context.Background(), home, Options{Offline: true, Capture: true}, h)
	if err != nil || !reflect.DeepEqual(got, again) || installs != 1 {
		t.Fatalf("reuse %+v %v", again, err)
	}
	manifest := filepath.Join(home, "voice-env-v1", planKey(r, true)+".json")
	if err = os.WriteFile(manifest, []byte("foreign sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ensure(context.Background(), home, Options{AllowInstall: true, Capture: true}, h)
	errorCode(t, err, "voice_env_cache")
	actual, _ := os.ReadFile(manifest)
	if string(actual) != "foreign sentinel" || installs != 1 {
		t.Fatal("foreign entry altered")
	}
	for _, failure := range []error{errors.New("fixture installation failure"), context.Canceled} {
		other := filepath.Join(t.TempDir(), "home")
		h.install = func(_ context.Context, _ string, site string, _ bool) error {
			os.WriteFile(filepath.Join(site, "partial"), []byte("partial"), 0600)
			return failure
		}
		_, err = ensure(context.Background(), other, Options{Python: r.Python, AllowInstall: true}, h)
		if !errors.Is(err, failure) {
			t.Fatal("failure lost", err)
		}
		entries, readErr := os.ReadDir(filepath.Join(other, "voice-env-v1"))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("own failed stage remained: %v %v", entries, readErr)
		}
	}
}

func TestPreparedMetadataChangesAndExplicitSiteAreNeverOverwritten(t *testing.T) {
	r := fixtureReport(t, false)
	ready := fixtureReport(t, true)
	h := fakeHooks(r)
	h.probe = func(_ context.Context, _ string, site string) (report, error) {
		if site != "" {
			return ready, nil
		}
		return r, nil
	}
	h.install = func(context.Context, string, string, bool) error { return nil }
	home := filepath.Join(t.TempDir(), "home")
	got, err := ensure(context.Background(), home, Options{Python: r.Python, AllowInstall: true}, h)
	if err != nil {
		t.Fatal(err)
	}
	ready.Selected.Versions["numpy"] = "2.4.0"
	_, err = ensure(context.Background(), home, Options{}, h)
	errorCode(t, err, "voice_env_changed")
	if _, err = os.Stat(got.SiteDir); err != nil {
		t.Fatal("changed environment deleted")
	}
	h = fakeHooks(r)
	external := t.TempDir()
	marker := filepath.Join(external, "keep")
	os.WriteFile(marker, []byte("sentinel"), 0600)
	_, err = ensure(context.Background(), filepath.Join(t.TempDir(), "new"), Options{Python: r.Python, SiteDir: external, AllowInstall: true}, h)
	errorCode(t, err, "voice_dependencies_missing")
	if raw, _ := os.ReadFile(marker); string(raw) != "sentinel" {
		t.Fatal("external site changed")
	}
}

func TestAliasMustLaunchResolvedInterpreterAndExplicitPinNeverFallsBack(t *testing.T) {
	r := fixtureReport(t, true)
	h := fakeHooks(r)
	alias := filepath.Join(t.TempDir(), "alias.exe")
	calls := []string{}
	h.probe = func(_ context.Context, python, _ string) (report, error) {
		calls = append(calls, python)
		if python == r.Python {
			return report{}, problem("voice_python_unavailable", "fixture access denied")
		}
		return r, nil
	}
	_, err := ensure(context.Background(), filepath.Join(t.TempDir(), "home"), Options{Python: alias}, h)
	errorCode(t, err, "voice_python_unavailable")
	if !reflect.DeepEqual(calls, []string{alias, r.Python}) {
		t.Fatal(calls)
	}
	calls = nil
	h.lookPath = func(name string) (string, error) {
		if name == "python" {
			return alias, nil
		}
		return r.Python, nil
	}
	h.probe = func(_ context.Context, python, _ string) (report, error) {
		calls = append(calls, python)
		if python == alias {
			return report{}, errors.New("fixture failed alias")
		}
		return r, nil
	}
	got, err := ensure(context.Background(), filepath.Join(t.TempDir(), "home"), Options{}, h)
	if err != nil || got.Python != r.Python || !reflect.DeepEqual(calls, []string{alias, r.Python}) {
		t.Fatalf("fallback %+v %v %v", got, err, calls)
	}
	calls = nil
	h.probe = func(_ context.Context, python, _ string) (report, error) {
		calls = append(calls, python)
		return report{}, errors.Join(problem("voice_process_cleanup", "fixture uncertain cleanup"), context.Canceled)
	}
	_, err = ensure(context.Background(), filepath.Join(t.TempDir(), "home"), Options{}, h)
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Code != "voice_process_cleanup" || typed.Exit != 9 || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(calls, []string{alias}) {
		t.Fatalf("uncertain cleanup hidden by fallback: %v %v", err, calls)
	}
}

func TestMetadataDecoderRejectsMalformedAndIncompatibleReports(t *testing.T) {
	r := fixtureReport(t, true)
	raw, _ := json.Marshal(r)
	if _, err := decodeReport(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte("{}"), append(append([]byte{}, raw...), raw...), []byte("\xff"), []byte(`{"format":1,"extra":"bad"}`)} {
		if _, err := decodeReport(bad); err == nil {
			t.Fatal("invalid report accepted")
		}
	}
	for _, name := range []string{"faster-whisper", "ctranslate2", "sounddevice"} {
		altered := fixtureReport(t, true)
		altered.Selected.Versions[name] = "99.0"
		if compatible(altered.Selected, true) {
			t.Fatal("unsupported major accepted", name)
		}
	}
	r.Selected.Modules["sounddevice"] = false
	if !compatible(r.Selected, false) || compatible(r.Selected, true) {
		t.Fatal("capture dependency scope wrong")
	}
}

func TestVoiceEnvProcessFixture(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "voiceenv-ok":
		fmt.Print("synthetic result")
		os.Exit(0)
	case "voiceenv-fail":
		fmt.Fprint(os.Stderr, "private diagnostic must not escape")
		os.Exit(19)
	case "voiceenv-stall":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}
func TestActualOwnedDependencyProcessBoundaries(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Darwin owned external process execution remains unavailable")
	}
	python, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"voiceenv-ok", "voiceenv-fail", "voiceenv-stall"} {
		t.Run(mode, func(t *testing.T) {
			deadline := 2 * time.Second
			if mode == "voiceenv-stall" {
				deadline = 100 * time.Millisecond
			}
			raw, err := command(context.Background(), python, []string{"-test.run=^TestVoiceEnvProcessFixture$", "--", mode}, t.TempDir(), deadline)
			if mode == "voiceenv-ok" {
				if err != nil || string(raw) != "synthetic result" {
					t.Fatalf("owned process result %q %v", raw, err)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "private diagnostic") {
					t.Fatal("failure/diagnostic boundary", err)
				}
				if mode == "voiceenv-stall" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("deadline lost", err)
				}
				if mode == "voiceenv-stall" && runtime.GOOS == "windows" {
					var typed *core.Error
					if errors.As(err, &typed) && typed.Code == "voice_process_cleanup" {
						t.Fatal("confirmed canceled Windows child was treated as live", err)
					}
				}
			}
		})
	}
}

func TestWheelInstallArgumentsHaveNoGlobalOrSourceBuildFallback(t *testing.T) {
	target := filepath.Join(t.TempDir(), "site")
	for _, capture := range []bool{false, true} {
		args := pipArguments(target, capture)
		joined := strings.Join(args, "\n")
		for _, required := range []string{"-I\n-B\n-m\npip\n--isolated", "--no-input", "--disable-pip-version-check", "--only-binary=:all:", "--index-url\nhttps://pypi.org/simple", "--target\n" + target, "--ignore-installed", "--no-compile", "--no-cache-dir", "faster-whisper==1.2.1"} {
			if !strings.Contains(joined, required) {
				t.Fatal("missing install constraint", required)
			}
		}
		if strings.Contains(joined, "sounddevice==0.5.6") != capture || strings.Contains(joined, "ensurepip") || strings.Contains(joined, "--user") {
			t.Fatal("wrong installation scope", args)
		}
	}
}

func TestNativeMetadataProbeNeverImportsSelectedPackagesOrPth(t *testing.T) {
	python := os.Getenv("UCLI_VOICEENV_TEST_PYTHON")
	if python == "" {
		t.Skip("opt in with a known regular Python for stdlib metadata-only probe")
	}
	site := t.TempDir()
	marker := filepath.Join(site, "import-was-executed")
	for name, module := range map[string]string{"faster-whisper": "faster_whisper", "ctranslate2": "ctranslate2", "numpy": "numpy", "tokenizers": "tokenizers", "huggingface-hub": "huggingface_hub", "sounddevice": "sounddevice"} {
		version := map[string]string{"faster-whisper": "1.2.1", "ctranslate2": "4.7.0", "numpy": "2.3.0", "tokenizers": "0.22.0", "huggingface-hub": "0.35.0", "sounddevice": "0.5.6"}[name]
		directory := filepath.Join(site, strings.ReplaceAll(name, "-", "_")+"-"+version+".dist-info")
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(directory, "METADATA"), []byte("Metadata-Version: 2.1\nName: "+name+"\nVersion: "+version+"\n"), 0600)
		os.WriteFile(filepath.Join(site, module+".py"), []byte("raise RuntimeError('package must never import in metadata probe')\n"), 0600)
	}
	code := "import pathlib; pathlib.Path(" + fmt.Sprintf("%q", filepath.ToSlash(marker)) + ").write_text('executed')\n"
	os.WriteFile(filepath.Join(site, "dangerous.pth"), []byte(code), 0600)
	os.WriteFile(filepath.Join(site, "sitecustomize.py"), []byte(code), 0600)
	r, err := probe(context.Background(), python, site)
	if err != nil {
		t.Fatal(err)
	}
	if !compatible(r.Selected, true) || !samePath(r.Python, python) {
		t.Fatalf("metadata not ready: %+v", r.Selected)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("metadata probe executed a package startup hook")
	}
	t.Logf("Metadata-only owned probe: Python %s; synthetic selected packages recognized without importing", r.PythonVersion)
}
