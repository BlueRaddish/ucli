package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

var join = filepath.Join

func validText(v string, max int) bool {
	return len(v) <= max && utf8.ValidString(v) && !strings.ContainsAny(v, "\x00\r\n")
}
func providerKnown(v string) bool {
	return v == "codex" || v == "claude" || v == "agy" || v == "github"
}
func pathKey(v string) string {
	v = filepath.Clean(v)
	if runtime.GOOS == "windows" {
		v = strings.ToLower(v)
	}
	return v
}
func samePath(a, b string) bool { return pathKey(a) == pathKey(b) }
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func physicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) || !validText(path, 32768) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.Contains(path, "://") || filepath.Dir(filepath.Clean(path)) == filepath.Clean(path) {
		return "", problem("connection_input", "Choose an absolute dedicated local path without links or network locations.")
	}
	path = filepath.Clean(path)
	if err := localPath(path); err != nil {
		return "", err
	}
	existing := path
	missing := []string{}
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", problem("connection_home", "Native or UCLI home could not be inspected.")
		}
		missing = append(missing, filepath.Base(existing))
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", problem("connection_home", "Home path could not be resolved.")
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return "", problem("connection_home", "Home must be a directory.")
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = join(resolved, missing[i])
	}
	if err = localPath(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}
func checkChain(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return problem("connection_store", "Connection path could not be inspected.")
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || isReparse(p) || p != path && !info.IsDir()) {
			return problem("connection_corrupt", "Connection paths cannot contain links or reparse points; existing paths were kept.")
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
func checkDirectory(path string) error {
	if err := checkChain(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return problem("connection_home", "Native home is not an ordinary directory.")
	}
	return nil
}
func privateDirs(path string) error {
	if err := checkChain(path); err != nil {
		return err
	}
	missing := []string{}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if !info.IsDir() {
				return problem("connection_store", "Connection parent is not a directory.")
			}
			break
		}
		if !os.IsNotExist(err) {
			return problem("connection_store", "Connection parent could not be inspected.")
		}
		missing = append(missing, p)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0700); err != nil {
			if !os.IsExist(err) {
				return problem("connection_store", "Private connection directory could not be created.")
			}
			if err = checkDirectory(missing[i]); err != nil {
				return err
			}
		} else if err = secureCreatedPath(missing[i], true); err != nil {
			return problem("connection_store", "Private connection directory could not be secured.")
		}
	}
	return nil
}
func executableHash(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", problem("connection_input", "Connection discovery requires a context.")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) || !validText(path, 32768) || runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return "", problem("connection_executable", "Select an absolute native executable; shell scripts and updating shims are not launched.")
	}
	if err := localPath(path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || isReparse(path) || info.Size() < 1 || info.Size() > 512<<20 {
		return "", problem("connection_executable", "Native executable is missing, linked, empty or larger than 512 MiB.")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", problem("connection_executable", "Native executable could not be read.")
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	total := int64(0)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		total += int64(n)
		if total > 512<<20 {
			return "", problem("connection_executable", "Native executable exceeds its size bound.")
		}
		_, _ = h.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", problem("connection_executable", "Native executable could not be hashed.")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func verifyExecutable(ctx context.Context, c Connection) error {
	sha, err := executableHash(ctx, c.Executable)
	if err != nil {
		return err
	}
	if sha != c.SHA256 {
		return problem("connection_changed", "Native executable changed since connection setup. Reconnect the reviewed version explicitly; no process was started.")
	}
	return nil
}
