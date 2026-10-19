package assets

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func safeName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 160 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	for _, character := range name {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character)) {
			return false
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func physicalRoot(root string) (string, error) {
	if !utf8.ValidString(root) || len(root) > 32768 || strings.ContainsRune(root, 0) || !filepath.IsAbs(root) || strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, "//") || strings.Contains(root, "://") {
		return "", errors.New("model cache root must be an absolute local directory")
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return "", errors.New("model cache root must be a dedicated directory")
	}
	if err := localPath(root); err != nil {
		return "", err
	}
	existing := root
	var missing []string
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", errors.New("model cache ancestor is not a directory")
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", errors.New("model cache could not be inspected")
		}
		missing = append(missing, filepath.Base(existing))
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", errors.New("model cache could not be resolved")
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	if err = localPath(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func checkPath(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return errors.New("model cache path could not be inspected")
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || isReparse(current) || current != path && !info.IsDir()) {
			return errors.New("model cache path contains a link, reparse point or invalid parent; it was left untouched")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func privateDirs(directory string) error {
	if err := checkPath(filepath.Join(directory, ".probe")); err != nil {
		return err
	}
	var missing []string
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return errors.New("model cache parent is not a directory")
			}
			break
		}
		if !os.IsNotExist(err) {
			return errors.New("model cache parent could not be inspected")
		}
		missing = append(missing, current)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		path := missing[index]
		if err := os.Mkdir(path, 0700); err != nil {
			if !os.IsExist(err) {
				return errors.New("private model cache parent could not be created")
			}
			if err = checkPath(filepath.Join(path, ".probe")); err != nil {
				return err
			}
		} else if err = secureCreated(path, true); err != nil {
			return errors.New("private model cache parent could not be secured")
		}
	}
	return nil
}

func readOwned(path string) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return nil, errors.New("model ownership manifest is not a bounded ordinary file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("model ownership manifest changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 {
		return nil, errors.New("model ownership manifest could not be fully read within its bound")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("model ownership manifest changed while reading")
	}
	return data, nil
}

func writeOwned(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err = secureCreated(path, false); err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func removeStage(stage string, model Model) error {
	// Never recursively delete a cache tree. Only the random directory we created
	// and its known flat names are eligible; unfamiliar entries are left intact.
	if err := checkPath(filepath.Join(stage, ".ucli-model.json")); err != nil {
		return errors.New("private model staging cleanup was unsafe; inspect the cache before retrying")
	}
	var result error
	names := []string{".ucli-model.json"}
	for _, file := range model.Files {
		names = append(names, file.Name)
	}
	for _, name := range names {
		if err := os.Remove(filepath.Join(stage, name)); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, errors.New("private model staging file could not be removed"))
		}
	}
	if err := os.Remove(stage); err != nil && !os.IsNotExist(err) {
		result = errors.Join(result, errors.New("private model staging directory could not be removed"))
	}
	return result
}

func syncDirectory(path string) []string {
	directory, err := os.Open(path)
	if err == nil {
		err = errors.Join(directory.Sync(), directory.Close())
	}
	if err != nil {
		return []string{"Model directory sync was unavailable; power-loss durability is unverified."}
	}
	return nil
}
