package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func defaultCacheBase() string {
	return os.TempDir()
}

func checkOwner(root *os.Root) error {
	sd, err := windows.GetNamedSecurityInfo(root.Name(), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%s: cannot read owner: %w", root.Name(), err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("%s: cannot read owner: %w", root.Name(), err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot read process token: %w", err)
	}
	if !owner.Equals(user.User.Sid) {
		return fmt.Errorf("%s is owned by %v, not %v, refusing to use it", root.Name(), owner, user.User.Sid)
	}
	return nil
}

func (a *app) lock(ctx context.Context, dir *os.Root, name string) (func(), bool) {
	deadline := time.Now().Add(lockWait)

	for range lockOpenAttempts {
		f, err := dir.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
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
				unlockRegion(f)
				f.Close()
			}, true
		}
		unlockRegion(f)
		f.Close()
	}
	return nil, false
}

func (a *app) waitForLock(ctx context.Context, f *os.File, name string, deadline time.Time) bool {
	for {
		err := lockRegion(f)
		if err == nil {
			return true
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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
	f, err := dir.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	if err := lockRegion(f); err != nil {
		f.Close()
		return false // Another process runs this command now.
	}
	unlockRegion(f)
	f.Close()

	return dir.Remove(name) == nil
}

const lockOpenAttempts = 8

// A Windows byte-range lock is mandatory, so lock one byte.
const lockBytes = 1

func lockRegion(f *os.File) error {
	return withHandle(f, func(h windows.Handle) error {
		return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, lockBytes, 0, new(windows.Overlapped))
	})
}

func unlockRegion(f *os.File) error {
	return withHandle(f, func(h windows.Handle) error {
		return windows.UnlockFileEx(h, 0, lockBytes, 0, new(windows.Overlapped))
	})
}

func withHandle(f *os.File, fn func(windows.Handle) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if err := rc.Control(func(fd uintptr) {
		opErr = fn(windows.Handle(fd))
	}); err != nil {
		return err
	}
	return opErr
}
