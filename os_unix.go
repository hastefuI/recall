//go:build unix

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

func defaultCacheBase() string {
	return cmp.Or(os.Getenv("XDG_RUNTIME_DIR"), os.TempDir())
}

func checkOwner(root *os.Root) error {
	fi, err := root.Stat(".")
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is group- or world-accessible, refusing to use it", root.Name())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot determine owner", root.Name())
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not %d, refusing to use it", root.Name(), st.Uid, os.Getuid())
	}
	return nil
}

func (a *app) lock(ctx context.Context, dir *os.Root, name string) (func(), bool) {
	deadline := time.Now().Add(lockWait)

	for range lockOpenAttempts {
		f, err := openLockFile(dir, name)
		if err != nil {
			a.warn("cannot open lock file: %v", err)
			return nil, false
		}
		if !a.waitForLock(ctx, f, name, deadline) {
			f.Close()
			return nil, false
		}
		if confirmLock(f, dir, name) {
			return func() {
				flock(f, syscall.LOCK_UN)
				f.Close()
			}, true
		}
		flock(f, syscall.LOCK_UN)
		f.Close()
	}
	return nil, false
}

func (a *app) waitForLock(ctx context.Context, f *os.File, name string, deadline time.Time) bool {
	for {
		err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return true
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			a.warn("cannot lock %s: %v", name, err)
			return false
		}
		if time.Now().After(deadline) {
			return false
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(lockPoll):
		}
	}
}

func tryRemoveLock(dir *os.Root, name string) bool {
	f, err := openLockFile(dir, name)
	if err != nil {
		return false
	}
	defer f.Close()

	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false // Another process runs this command now.
	}
	defer flock(f, syscall.LOCK_UN)

	return dir.Remove(name) == nil
}

const lockOpenAttempts = 8

// Retries a spurious darwin ENOENT.
func openLockFile(dir *os.Root, name string) (*os.File, error) {
	var err error
	for range lockOpenAttempts {
		var f *os.File
		if f, err = dir.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, err
}

func flock(f *os.File, how int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lockErr error
	if err := rc.Control(func(fd uintptr) {
		lockErr = syscall.Flock(int(fd), how)
	}); err != nil {
		return err
	}
	return lockErr
}
