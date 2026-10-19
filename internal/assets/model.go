package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ucli.local/ucli/internal/artifact"
)

type PreparedModel struct {
	ID             string   `json:"id"`
	Revision       string   `json:"revision"`
	Directory      string   `json:"directory"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Reused         bool     `json:"reused"`
	Warnings       []string `json:"warnings,omitempty"`
}

type modelReceipt struct {
	Format                           int
	ModelID, Revision, CatalogSHA256 string
	Directory                        string
}

type ensureFile func(context.Context, artifact.Manifest, artifact.Options) (artifact.File, error)

// PrepareModel is explicit first use, never a startup/listing hook. Offline can
// materialize already cached blobs, but cannot acquire missing bytes. Returned
// directories are data only; the caller separately admits its local runtime.
func PrepareModel(ctx context.Context, cacheRoot, modelID string, offline bool) (PreparedModel, error) {
	if ctx == nil {
		return PreparedModel{}, errors.New("model preparation requires a context")
	}
	for _, model := range Catalog() {
		if model.ID == modelID {
			cache, err := artifact.New(cacheRoot)
			if err != nil {
				return PreparedModel{}, err
			}
			return prepareModel(ctx, cacheRoot, model, offline, cache.Ensure)
		}
	}
	return PreparedModel{}, errors.New("model is not in the reviewed shipped catalog")
}

func catalogHash(model Model) (string, error) {
	if model.ID == "" || model.Revision == "" || len(model.Files) == 0 || len(model.Files) > 32 {
		return "", errors.New("invalid shipped model metadata")
	}
	seen := make(map[string]bool)
	for _, file := range model.Files {
		key := strings.ToLower(file.Name)
		digest, err := hex.DecodeString(file.SHA256)
		if !safeName(file.Name) || key == ".ucli-model.json" || seen[key] || err != nil || len(digest) != sha256.Size || strings.ToLower(file.SHA256) != file.SHA256 || file.Bytes < 1 || file.Bytes > artifact.MaxArtifactBytes {
			return "", errors.New("invalid shipped model file metadata")
		}
		seen[key] = true
	}
	data, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func prepareModel(ctx context.Context, cacheRoot string, model Model, offline bool, ensure ensureFile) (result PreparedModel, err error) {
	if ctx == nil || ensure == nil {
		return result, errors.New("model preparation requires a context and artifact cache")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	id, err := catalogHash(model)
	if err != nil {
		return result, err
	}
	root, err := physicalRoot(cacheRoot)
	if err != nil {
		return result, err
	}
	directory := filepath.Join(root, "models-v1")
	manifest := filepath.Join(directory, id+".json")
	if err = checkPath(manifest); err != nil {
		return result, err
	}
	result, err = verifyModel(ctx, directory, manifest, model, id)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	// Acquire every blob before making a model staging directory. Partial blob
	// caching is safe and reusable; only the final manifest activates a bundle.
	files := make([]artifact.File, len(model.Files))
	var warnings []string
	for index, file := range model.Files {
		if err = ctx.Err(); err != nil {
			return PreparedModel{}, err
		}
		files[index], err = ensure(ctx, model.artifact(file), artifact.Options{Offline: offline})
		if err != nil {
			return PreparedModel{}, err
		}
		warnings = append(warnings, files[index].Warnings...)
	}
	if err = ctx.Err(); err != nil {
		return PreparedModel{}, err
	}
	if err = privateDirs(directory); err != nil {
		return PreparedModel{}, err
	}
	stage, err := os.MkdirTemp(directory, ".model-"+id+"-")
	if err != nil {
		return PreparedModel{}, errors.New("private model directory could not be created")
	}
	published := false
	defer func() {
		if !published {
			if cleanup := removeStage(stage, model); cleanup != nil {
				err = errors.Join(err, cleanup)
			}
		}
	}()
	if err = secureCreated(stage, true); err != nil {
		return PreparedModel{}, errors.New("private model directory could not be secured")
	}
	for index, file := range model.Files {
		if err = copyVerified(ctx, files[index].Path, filepath.Join(stage, file.Name), file); err != nil {
			return PreparedModel{}, err
		}
	}
	receipt := modelReceipt{Format: 1, ModelID: model.ID, Revision: model.Revision, CatalogSHA256: id, Directory: filepath.Base(stage)}
	data, _ := json.Marshal(receipt)
	owned := filepath.Join(stage, ".ucli-model.json")
	if err = writeOwned(owned, data); err != nil {
		return PreparedModel{}, err
	}
	warnings = append(warnings, syncDirectory(stage)...)
	if err = ctx.Err(); err != nil {
		return PreparedModel{}, err
	}
	if err = checkPath(manifest); err != nil {
		return PreparedModel{}, err
	}
	// A hardlinked manifest is the atomic complete-publication point. Directory
	// rename cannot portably refuse an existing empty directory. No published
	// directory is ever replaced or deleted, including after cancellation.
	if err = os.Link(owned, manifest); err != nil {
		winner, verifyErr := verifyModel(ctx, directory, manifest, model, id)
		if verifyErr != nil {
			return PreparedModel{}, fmt.Errorf("model publication failed; existing cache was left untouched: %w", verifyErr)
		}
		winner.Warnings = append(winner.Warnings, warnings...)
		return winner, nil
	}
	published = true
	warnings = append(warnings, syncDirectory(directory)...)
	return PreparedModel{ID: model.ID, Revision: model.Revision, Directory: stage, ManifestSHA256: id, Warnings: warnings}, nil
}

func verifyModel(ctx context.Context, directory, manifest string, model Model, id string) (PreparedModel, error) {
	data, err := readOwned(manifest)
	if err != nil {
		return PreparedModel{}, err
	}
	var receipt modelReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&receipt); err != nil {
		return PreparedModel{}, errors.New("existing model manifest is not owned by this catalog; it was left untouched")
	}
	canonical, _ := json.Marshal(receipt)
	if !bytes.Equal(data, canonical) || receipt.Format != 1 || receipt.ModelID != model.ID || receipt.Revision != model.Revision || receipt.CatalogSHA256 != id || !safeName(receipt.Directory) || !strings.HasPrefix(receipt.Directory, ".model-"+id+"-") {
		return PreparedModel{}, errors.New("existing model manifest does not match this catalog; it was left untouched")
	}
	path := filepath.Join(directory, receipt.Directory)
	if err = checkPath(filepath.Join(path, ".ucli-model.json")); err != nil {
		return PreparedModel{}, err
	}
	owned, err := readOwned(filepath.Join(path, ".ucli-model.json"))
	if err != nil || !bytes.Equal(owned, data) {
		return PreparedModel{}, errors.New("published model directory lacks its matching ownership manifest")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != len(model.Files)+1 {
		return PreparedModel{}, errors.New("published model directory contains missing or unexpected entries")
	}
	for _, file := range model.Files {
		if err = verifyFile(ctx, filepath.Join(path, file.Name), file, nil); err != nil {
			return PreparedModel{}, err
		}
	}
	return PreparedModel{ID: model.ID, Revision: model.Revision, Directory: path, ManifestSHA256: id, Reused: true}, nil
}

func copyVerified(ctx context.Context, source, destination string, expected ModelFile) (err error) {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("private model file could not be created")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err = secureCreated(destination, false); err != nil {
		return errors.New("private model file could not be secured")
	}
	if err = verifyFile(ctx, source, expected, file); err != nil {
		return err
	}
	return file.Sync()
}

func verifyFile(ctx context.Context, path string, expected ModelFile, destination io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Bytes {
		return errors.New("model file does not have its exact pinned size or ordinary file type")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("model file could not be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("model file changed while it was opened")
	}
	hash := sha256.New()
	var writer io.Writer = hash
	if destination != nil {
		writer = io.MultiWriter(hash, destination)
	}
	n, err := io.Copy(writer, io.LimitReader(contextReader{ctx, file}, expected.Bytes+1))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("model file changed or does not match its pinned SHA-256")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(data)
}
