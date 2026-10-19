package connections

import (
	"context"
	"os"
	"time"
)

// One cooperative per-home lock serializes metadata publication and native login.
// The OS releases it on crash; the persistent inode must never be unlinked. This
// deliberately serializes distinct providers too; per-binding locks can follow
// when concurrent login is needed and native keyring scope has been established.
func (s *Service) lock(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, problem("connection_input", "Connections require a context.")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := privateDirs(s.directory); err != nil {
		return nil, err
	}
	path := join(s.directory, ".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if os.IsExist(err) {
		if err = checkChain(path); err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			return nil, problem("connection_corrupt", "The connection lock path is not an ordinary empty lock file.")
		}
		file, err = os.OpenFile(path, os.O_RDWR, 0)
	} else if err == nil {
		err = secureCreatedPath(path, false)
	}
	if err != nil {
		if file != nil {
			file.Close()
		}
		return nil, problem("connection_store", "Connection lock could not be opened securely.")
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		locked, err := tryLock(file)
		if err != nil {
			file.Close()
			return nil, problem("connection_store", "Connection lock is unavailable on this filesystem.")
		}
		if locked {
			return func() { unlockFile(file); file.Close() }, nil
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-deadline.C:
			file.Close()
			return nil, problem("connection_busy", "Another connection operation is active. Wait for it or cancel its native sign-in.")
		case <-tick.C:
		}
	}
}
