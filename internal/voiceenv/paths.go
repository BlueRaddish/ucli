package voiceenv

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func physicalHome(home string) (string, error) {
	if !filepath.IsAbs(home) || !validText(home, 32768) || strings.HasPrefix(home, `\\`) || strings.HasPrefix(home, "//") || strings.Contains(home, "://") || filepath.Dir(filepath.Clean(home)) == filepath.Clean(home) {
		return "", problem("voice_env_input", "Voice setup requires an absolute local UCLI home.")
	}
	if err := localPath(home); err != nil {
		return "", err
	}
	existing := filepath.Clean(home)
	var missing []string
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", problem("voice_env_cache", "UCLI home ancestor is not a directory.")
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", problem("voice_env_cache", "UCLI home could not be inspected.")
		}
		missing = append(missing, filepath.Base(existing))
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", problem("voice_env_cache", "UCLI home could not be resolved.")
	}
	if err = localPath(resolved); err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return resolved, nil
}

func checkPath(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return problem("voice_env_cache", "Dependency cache path could not be inspected.")
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || isReparse(current) || current != path && !info.IsDir()) {
			return problem("voice_env_cache", "Dependency cache links, reparse points and foreign entries are refused without modification.")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func ordinaryDirectory(path string) error {
	if !filepath.IsAbs(path) || !validText(path, 32768) {
		return problem("voice_site_missing", "Select an absolute Python package directory.")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparse(path) {
		return problem("voice_site_missing", "Selected Python package directory is missing or linked; it was left untouched.")
	}
	return nil
}
func ordinaryPython(path string) error {
	if !filepath.IsAbs(path) || !validText(path, 32768) {
		return problem("voice_python_missing", "Python did not report a regular absolute executable path.")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return problem("voice_python_missing", "Resolved Python executable is unavailable. Select an existing regular interpreter.")
	}
	return nil
}

func privateDirs(directory string) error {
	if err := checkPath(filepath.Join(directory, "placeholder")); err != nil {
		return err
	}
	var missing []string
	for path := directory; ; path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil {
			if !info.IsDir() {
				return problem("voice_env_cache", "Dependency cache parent is not a directory.")
			}
			break
		} else if !os.IsNotExist(err) {
			return problem("voice_env_cache", "Dependency cache parent could not be read.")
		}
		missing = append(missing, path)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0700); err != nil {
			if !os.IsExist(err) {
				return problem("voice_env_cache", "Dependency cache could not be created.")
			}
			if err = checkPath(filepath.Join(missing[i], "placeholder")); err != nil {
				return err
			}
		} else if err = secureCreated(missing[i], true); err != nil {
			return problem("voice_env_cache", "Dependency cache could not be secured.")
		}
	}
	return nil
}

func checkTree(path string) error {
	if err := checkPath(filepath.Join(path, "placeholder")); err != nil {
		return err
	}
	files := 0
	var size int64
	return filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return problem("voice_env_cache", "Prepared dependency files could not be inspected.")
		}
		files++
		if files > 100000 {
			return problem("voice_env_cache", "Prepared dependency tree exceeds its file-count bound.")
		}
		if entry.Type()&os.ModeSymlink != 0 || isReparse(name) {
			return problem("voice_env_cache", "Prepared dependency tree contains a link or reparse point; it was retained.")
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return problem("voice_env_cache", "Prepared dependency tree contains an unsupported file.")
			}
			size += info.Size()
			if size > 2<<30 {
				return problem("voice_env_cache", "Prepared dependency tree exceeds its 2 GiB bound.")
			}
		}
		return nil
	})
}

func removeStage(base, stage string) error {
	// This is the only recursive removal: a newly created private sibling, never
	// a caller-provided site or completed manifest target. Resolve before deleting.
	if !filepath.IsAbs(stage) || filepath.Dir(stage) != base || !strings.HasPrefix(filepath.Base(stage), ".site-") {
		return problem("voice_env_cleanup", "Dependency cleanup target is outside its owned staging scope.")
	}
	resolved, err := filepath.EvalSymlinks(stage)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !samePath(resolved, stage) {
		return problem("voice_env_cleanup", "Dependency staging path changed; it was retained.")
	}
	if err = checkTree(stage); err != nil {
		return err
	}
	if err = os.RemoveAll(stage); err != nil {
		return problem("voice_env_cleanup", "Private dependency staging cleanup failed; inspect retained state.")
	}
	return nil
}

func readOwned(path string) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 512<<10 {
		return nil, problem("voice_env_cache", "Prepared dependency manifest is missing, foreign or outside its bound.")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, problem("voice_env_cache", "Prepared dependency manifest could not be opened.")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (512<<10)+1))
	if err != nil || len(data) > 512<<10 {
		return nil, problem("voice_env_cache", "Prepared dependency manifest could not be read within its bound.")
	}
	return data, nil
}

func writeOwned(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return problem("voice_env_cache", "Owned dependency completion manifest could not be created.")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err = secureCreated(path, false); err != nil {
		return problem("voice_env_cache", "Owned dependency completion manifest could not be secured.")
	}
	if _, err = file.Write(data); err != nil {
		return problem("voice_env_cache", "Owned dependency completion manifest could not be written.")
	}
	return file.Sync()
}
