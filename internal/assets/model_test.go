package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"ucli.local/ucli/internal/artifact"
)

func syntheticModel(t *testing.T) (Model, ensureFile) {
	t.Helper()
	model := Model{ID: "fixture/tiny", Revision: "reviewed-fixture-v1", Engine: "fixture"}
	files := make(map[string]string)
	sources, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "model.bin", "tokenizer.json", "vocabulary.txt"} {
		data := []byte("Synthetic model fixture bytes: " + name)
		digest := sha256.Sum256(data)
		path := filepath.Join(sources, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		model.Files = append(model.Files, ModelFile{Name: name, URL: "https://fixture.invalid/" + name, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(data))})
		files[name] = path
	}
	ensure := func(ctx context.Context, manifest artifact.Manifest, options artifact.Options) (artifact.File, error) {
		if err := ctx.Err(); err != nil {
			return artifact.File{}, err
		}
		if manifest.Kind != artifact.TranscriptionModel || manifest.Platform != (artifact.Platform{OS: "any", Arch: "any"}) {
			return artifact.File{}, errors.New("model artifact is not portable data")
		}
		return artifact.File{Path: files[filepath.Base(manifest.ID)], SHA256: manifest.SHA256, Bytes: manifest.MaxBytes}, nil
	}
	return model, ensure
}

func manifestPath(t *testing.T, root string, model Model) string {
	t.Helper()
	id, err := catalogHash(model)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "models-v1", id+".json")
}

func requireNoStage(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "models-v1"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".model-") {
			t.Fatalf("private partial model directory survived: %s", entry.Name())
		}
	}
}

func TestCatalogIsIndependentPinnedMetadata(t *testing.T) {
	models := Catalog()
	if len(models) != 1 || models[0].ID != "whisper/tiny.en" || models[0].Revision != tinyRevision || len(models[0].Files) != 4 {
		t.Fatal(models)
	}
	var total int64
	for _, file := range models[0].Files {
		if !strings.HasPrefix(file.URL, "https://huggingface.co/Systran/faster-whisper-tiny.en/resolve/"+tinyRevision+"/") {
			t.Fatal("catalog is not pinned to the reviewed official revision")
		}
		total += file.Bytes
	}
	if total != 78090594 {
		t.Fatal(total)
	}
	models[0].Files[0].Name = "mutated"
	if Catalog()[0].Files[0].Name != "config.json" {
		t.Fatal("callers can mutate the shared catalog")
	}
	root := filepath.Join(t.TempDir(), "never-created")
	if _, err := PrepareModel(context.Background(), root, "unknown/model", false); err == nil {
		t.Fatal("unreviewed model was accepted")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("unknown model touched the cache")
	}
	if _, err := PrepareModel(nil, root, "whisper/tiny.en", true); err == nil {
		t.Fatal("nil context was accepted")
	}
}

func TestPrepareModelPublishesCompleteBundleAndReusesOffline(t *testing.T) {
	model, ensure := syntheticModel(t)
	root := filepath.Join(t.TempDir(), "cache")
	calls := 0
	prepared, err := prepareModel(context.Background(), root, model, false, func(ctx context.Context, manifest artifact.Manifest, options artifact.Options) (artifact.File, error) {
		calls++
		if _, err := os.Lstat(manifestPath(t, root, model)); !os.IsNotExist(err) {
			t.Fatal("bundle published before all files were acquired")
		}
		return ensure(ctx, manifest, options)
	})
	if err != nil || prepared.Reused || calls != 4 {
		t.Fatalf("prepare=%+v calls=%d err=%v", prepared, calls, err)
	}
	for _, expected := range model.Files {
		if err = verifyFile(context.Background(), filepath.Join(prepared.Directory, expected.Name), expected, nil); err != nil {
			t.Fatal(err)
		}
		source, err := ensure(context.Background(), model.artifact(expected), artifact.Options{})
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.Stat(source.Path)
		if err != nil {
			t.Fatal(err)
		}
		copy, err := os.Stat(filepath.Join(prepared.Directory, expected.Name))
		if err != nil || os.SameFile(original, copy) {
			t.Fatal("model materialization shares writable blob storage", err)
		}
	}
	before, err := os.ReadFile(manifestPath(t, root, model))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := prepareModel(context.Background(), root, model, true, func(context.Context, artifact.Manifest, artifact.Options) (artifact.File, error) {
		t.Fatal("verified bundle tried to reacquire an artifact")
		return artifact.File{}, nil
	})
	if err != nil || !reused.Reused || reused.Directory != prepared.Directory {
		t.Fatalf("reuse=%+v err=%v", reused, err)
	}
	after, _ := os.ReadFile(manifestPath(t, root, model))
	if string(before) != string(after) {
		t.Fatal("reuse rewrote ownership")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{prepared.Directory, filepath.Join(prepared.Directory, "model.bin")} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("model cache permissions are not private", err)
			}
		}
	}
}

func TestPrepareModelOfflineMissingHasNoWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	_, err := PrepareModel(context.Background(), root, "whisper/tiny.en", true)
	var typed *artifact.Error
	if !errors.As(err, &typed) || typed.Code != artifact.Offline {
		t.Fatalf("unexpected offline result: %v", err)
	}
	if _, err = os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("offline missing model created cache files")
	}
}

func TestPrepareModelOfflineMaterializesVerifiedBlobs(t *testing.T) {
	model, ensure := syntheticModel(t)
	root := filepath.Join(t.TempDir(), "cache")
	_, err := prepareModel(context.Background(), root, model, true, func(ctx context.Context, manifest artifact.Manifest, options artifact.Options) (artifact.File, error) {
		if !options.Offline {
			t.Fatal("offline intent was lost")
		}
		return ensure(ctx, manifest, options)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrepareModelRefusesChangedOrForeignCache(t *testing.T) {
	for _, damage := range []string{"manifest", "file", "extra", "directory", "traversal"} {
		t.Run(damage, func(t *testing.T) {
			model, ensure := syntheticModel(t)
			root := filepath.Join(t.TempDir(), "cache")
			prepared, err := prepareModel(context.Background(), root, model, false, ensure)
			if err != nil {
				t.Fatal(err)
			}
			manifest := manifestPath(t, root, model)
			protected := manifest
			switch damage {
			case "manifest":
				err = os.WriteFile(manifest, []byte("foreign owner"), 0600)
			case "file":
				protected = filepath.Join(prepared.Directory, "model.bin")
				info, _ := os.Stat(protected)
				err = os.WriteFile(protected, []byte(strings.Repeat("x", int(info.Size()))), 0600)
			case "extra":
				protected = filepath.Join(prepared.Directory, "foreign.txt")
				err = os.WriteFile(protected, []byte("foreign"), 0600)
			case "directory":
				err = os.Remove(manifest)
				if err == nil {
					err = os.Mkdir(manifest, 0700)
				}
			case "traversal":
				id, _ := catalogHash(model)
				data, _ := json.Marshal(modelReceipt{Format: 1, ModelID: model.ID, Revision: model.Revision, CatalogSHA256: id, Directory: "../outside"})
				err = os.WriteFile(manifest, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(protected)
			_, err = prepareModel(context.Background(), root, model, false, func(context.Context, artifact.Manifest, artifact.Options) (artifact.File, error) {
				t.Fatal("damaged/foreign published model attempted acquisition")
				return artifact.File{}, nil
			})
			if err == nil {
				t.Fatal("damaged/foreign published model was accepted")
			}
			after, _ := os.ReadFile(protected)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("foreign bytes were changed")
			}
			if _, err := os.Lstat(prepared.Directory); err != nil {
				t.Fatal("published model directory was removed")
			}
		})
	}
}

func TestPrepareModelCancellationAndWrongBytesNeverPublish(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "acquire-cancel", "changed-source", "wrong-size"} {
		t.Run(mode, func(t *testing.T) {
			model, ensure := syntheticModel(t)
			root := filepath.Join(t.TempDir(), "cache")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "pre-cancel" {
				cancel()
			}
			calls := 0
			_, err := prepareModel(ctx, root, model, false, func(ctx context.Context, manifest artifact.Manifest, options artifact.Options) (artifact.File, error) {
				calls++
				file, err := ensure(ctx, manifest, options)
				if calls == 1 {
					switch mode {
					case "acquire-cancel":
						cancel()
					case "changed-source", "wrong-size":
						data := []byte(strings.Repeat("x", int(file.Bytes)))
						if mode == "wrong-size" {
							data = append(data, 'x')
						}
						if err = os.WriteFile(file.Path, data, 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				return file, err
			})
			if err == nil {
				t.Fatal("canceled or damaged preparation passed")
			}
			if strings.Contains(mode, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity was lost", err)
			}
			if mode == "pre-cancel" && calls != 0 || mode == "acquire-cancel" && calls != 1 {
				t.Fatalf("acquisition continued after cancellation: %d", calls)
			}
			if _, err := os.Lstat(manifestPath(t, root, model)); !os.IsNotExist(err) {
				t.Fatal("incomplete bundle was published")
			}
			requireNoStage(t, root)
		})
	}
}

func TestPrepareModelConcurrentWinnerIsVerified(t *testing.T) {
	model, ensure := syntheticModel(t)
	root := filepath.Join(t.TempDir(), "cache")
	var ready sync.WaitGroup
	ready.Add(2)
	barrier := make(chan struct{})
	gate := func(ctx context.Context, manifest artifact.Manifest, options artifact.Options) (artifact.File, error) {
		if strings.HasSuffix(manifest.ID, "/config.json") {
			ready.Done()
			<-barrier
		}
		return ensure(ctx, manifest, options)
	}
	type reply struct {
		model PreparedModel
		err   error
	}
	results := make(chan reply, 2)
	for range 2 {
		go func() {
			result, err := prepareModel(context.Background(), root, model, false, gate)
			results <- reply{result, err}
		}()
	}
	ready.Wait()
	close(barrier)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.model.Directory != second.model.Directory || first.model.Reused == second.model.Reused {
		t.Fatalf("concurrent results differ: %+v %+v", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(root, "models-v1"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("losing stage survived or winner was incomplete: %v %v", entries, err)
	}
}

func TestPrepareModelForeignConcurrentWinnerIsUntouched(t *testing.T) {
	model, ensure := syntheticModel(t)
	root := filepath.Join(t.TempDir(), "cache")
	manifest := manifestPath(t, root, model)
	_, err := prepareModel(context.Background(), root, model, false, func(ctx context.Context, m artifact.Manifest, options artifact.Options) (artifact.File, error) {
		if strings.HasSuffix(m.ID, "/vocabulary.txt") {
			if err := os.MkdirAll(filepath.Dir(manifest), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifest, []byte("foreign concurrent winner"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return ensure(ctx, m, options)
	})
	if err == nil {
		t.Fatal("foreign winner was accepted")
	}
	data, _ := os.ReadFile(manifest)
	if string(data) != "foreign concurrent winner" {
		t.Fatal("foreign winner was modified")
	}
	requireNoStage(t, root)
}

func TestPrepareModelRefusesTraversal(t *testing.T) {
	for _, name := range []string{"../model.bin", "..", "CON", "model.bin:stream", "file.", "dir/file", "dir\\file", ".ucli-model.json"} {
		model, _ := syntheticModel(t)
		model.Files[0].Name = name
		if _, err := catalogHash(model); err == nil {
			t.Fatalf("unsafe catalog file name accepted: %q", name)
		}
	}
}

func TestPrepareModelRefusesSymlinks(t *testing.T) {
	model, ensure := syntheticModel(t)
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "models-v1")); err != nil {
		t.Skipf("host cannot create the symlink negative: %v", err)
	}
	_, err := prepareModel(context.Background(), root, model, false, ensure)
	if err == nil {
		t.Fatal("linked cache child accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("linked external directory was modified")
	}
}

type cancelOnWrite struct{ cancel context.CancelFunc }

func (w cancelOnWrite) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

func TestModelCopyCancellationCannotCertifyPartialBytes(t *testing.T) {
	model, ensure := syntheticModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file, err := ensure(ctx, model.artifact(model.Files[0]), artifact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyFile(ctx, file.Path, model.Files[0], cancelOnWrite{cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("copy completed after its consumer canceled: %v", err)
	}
}
