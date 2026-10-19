package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func canonicalRoot(root string) (string, error) {
	if !utf8.ValidString(root) || len(root) > 32768 || strings.ContainsRune(root, 0) || !filepath.IsAbs(root) || strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, "//") || strings.Contains(root, "://") {
		return "", problem(InvalidRequest, "Artifact cache root must be an absolute local directory.")
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return "", problem(InvalidRequest, "Artifact cache root must be a dedicated directory, not a filesystem root.")
	}
	if err := localPath(root); err != nil {
		return "", err
	}
	// Resolve existing ancestor aliases once (for example macOS /var). Entries
	// created below that physical path cannot themselves be links/reparse points.
	existing := root
	var missing []string
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", problem(CacheUnsafe, "Artifact cache ancestor is not a directory.")
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", problem(CacheIO, "Artifact cache root could not be inspected.")
		}
		missing = append(missing, filepath.Base(existing))
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", problem(CacheIO, "Artifact cache root could not be resolved.")
	}
	if err = localPath(resolved); err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return resolved, nil
}

func checkChain(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return problem(CacheIO, "Artifact cache path could not be inspected.")
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || isReparse(current) {
				return problem(CacheUnsafe, "Artifact cache entries cannot be links or reparse points; existing paths were left untouched.")
			}
			if current != path && !info.IsDir() {
				return problem(CacheUnsafe, "Artifact cache parent is not a directory.")
			}
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func makePrivateDirs(directory string) error {
	if err := checkChain(filepath.Join(directory, "blob")); err != nil {
		return err
	}
	var missing []string
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return problem(CacheUnsafe, "Artifact cache parent is not a directory.")
			}
			break
		}
		if !os.IsNotExist(err) {
			return problem(CacheIO, "Artifact cache directory could not be inspected.")
		}
		missing = append(missing, current)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		path := missing[i]
		if err := os.Mkdir(path, 0700); err != nil {
			if !os.IsExist(err) {
				return problem(CacheIO, "Artifact cache directory could not be created.")
			}
			if err = checkChain(filepath.Join(path, "blob")); err != nil {
				return err
			}
		} else if err = secureCreatedPath(path, true); err != nil {
			return problem(CacheIO, "Artifact cache directory could not be secured.")
		}
	}
	return nil
}
