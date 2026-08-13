package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newApp(t *testing.T) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	var out, errb bytes.Buffer
	return &app{stdout: &out, stderr: &errb}, &out, &errb
}

func countRuns(t *testing.T, marker string) int {
	t.Helper()
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run")
}

func TestRunCachesAndRecalls(t *testing.T) {
	a, out, _ := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--ttl=1m"}, helperCmd(t, "-marker="+marker, "-stdout=hello\n"))

	if code := a.run(t.Context(), args); code != 0 {
		t.Fatalf("first run: exit code = %d, want 0", code)
	}
	if got := out.String(); got != "hello\n" {
		t.Errorf("first run: stdout = %q, want %q", got, "hello\n")
	}

	out.Reset()
	if code := a.run(t.Context(), args); code != 0 {
		t.Fatalf("second run: exit code = %d, want 0", code)
	}
	if got := out.String(); got != "hello\n" {
		t.Errorf("second run: stdout = %q, want %q", got, "hello\n")
	}

	if got := countRuns(t, marker); got != 1 {
		t.Errorf("command executed %d times, want 1 (second call should be a cache hit)", got)
	}
}

func TestRunForceBypassesCache(t *testing.T) {
	a, _, _ := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	cmd := helperCmd(t, "-marker="+marker)

	if code := a.run(t.Context(), recallArgs([]string{"--ttl=1m"}, cmd)); code != 0 {
		t.Fatalf("first run: exit code = %d, want 0", code)
	}
	if code := a.run(t.Context(), recallArgs([]string{"--ttl=1m", "--force"}, cmd)); code != 0 {
		t.Fatalf("forced run: exit code = %d, want 0", code)
	}

	if got := countRuns(t, marker); got != 2 {
		t.Errorf("command executed %d times, want 2 (--force must re-run)", got)
	}
}

func TestRunExpiredEntryReRuns(t *testing.T) {
	a, _, _ := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--ttl=1ns"}, helperCmd(t, "-marker="+marker))

	for range 2 {
		if code := a.run(t.Context(), args); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	}

	if got := countRuns(t, marker); got != 2 {
		t.Errorf("command executed %d times, want 2 (a 1ns TTL always expires)", got)
	}
}

func TestRunPreservesExitCodeAndStderr(t *testing.T) {
	a, _, errb := newApp(t)

	args := recallArgs(nil, helperCmd(t, "-stderr=oops\n", "-exit=3"))
	if code := a.run(t.Context(), args); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if !strings.Contains(errb.String(), "oops") {
		t.Errorf("stderr = %q, want it to contain %q", errb.String(), "oops")
	}

	errb.Reset()
	if code := a.run(t.Context(), args); code != 3 {
		t.Errorf("recalled exit code = %d, want 3", code)
	}
	if !strings.Contains(errb.String(), "oops") {
		t.Errorf("recalled stderr = %q, want it to contain %q", errb.String(), "oops")
	}
}

func TestRunSignalledCommandUsesShellConvention(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no signals; ExitCode is reported directly")
	}
	a, _, _ := newApp(t)

	code := a.run(t.Context(), []string{"--", "sh", "-c", "kill -9 $$"})
	if code != 137 {
		t.Errorf("exit code = %d, want 137 (128+SIGKILL)", code)
	}
}

func TestRunMissingCommand(t *testing.T) {
	a, _, errb := newApp(t)

	code := a.run(t.Context(), []string{"--", "recall-no-such-command"})
	if code != exitNotRun {
		t.Errorf("exit code = %d, want %d", code, exitNotRun)
	}
	if !strings.Contains(errb.String(), "cannot run") {
		t.Errorf("stderr = %q, want it to explain the failure", errb.String())
	}
}

func TestRunNoArgs(t *testing.T) {
	a, _, errb := newApp(t)

	if code := a.run(t.Context(), nil); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "usage:") {
		t.Errorf("stderr = %q, want a usage message", errb.String())
	}
}

func TestRunVersionFlagWorksWithoutCommand(t *testing.T) {
	a, out, errb := newApp(t)

	if code := a.run(t.Context(), []string{"--version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "dev\n" {
		t.Errorf("stdout = %q, want %q", got, "dev\n")
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errb.String())
	}
}

func TestRunVersionFlagDoesNotRunCommand(t *testing.T) {
	a, out, errb := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--version"}, helperCmd(t, "-marker="+marker))
	if code := a.run(t.Context(), args); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "dev\n" {
		t.Errorf("stdout = %q, want %q", got, "dev\n")
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errb.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("command ran, marker state = %v, want no marker file", err)
	}
}

func TestRunTimeoutIsNotCached(t *testing.T) {
	a, _, _ := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--timeout=50ms", "--ttl=1m"}, helperCmd(t, "-marker="+marker, "-sleep=2s"))

	if code := a.run(t.Context(), args); code == 0 {
		t.Error("exit code = 0, want non-zero for a timed-out command")
	}
	if code := a.run(t.Context(), args); code == 0 {
		t.Error("second run: exit code = 0, want the command to run again")
	}

	if got := countRuns(t, marker); got != 2 {
		t.Errorf("command executed %d times, want 2 (a timeout must not be cached)", got)
	}
}

func TestRunOversizeOutputIsNotCached(t *testing.T) {
	a, out, errb := newApp(t)

	cmd := helperCmd(t, "-stdout="+strings.Repeat("x", 40))
	if code := a.run(t.Context(), recallArgs([]string{"--max-output=16", "--ttl=1m"}, cmd)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != strings.Repeat("x", 40) {
		t.Errorf("stdout = %q, want the complete command output", got)
	}
	if !strings.Contains(errb.String(), "not cached") {
		t.Errorf("stderr = %q, want a warning that the result was not cached", errb.String())
	}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if _, ok := load(dir, key(cmd)+".json"); ok {
		t.Error("an oversize result was cached")
	}
}

func TestConcurrentRunsExecuteOnce(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())

	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--ttl=1m"}, helperCmd(t, "-marker="+marker, "-sleep=300ms"))

	var wg sync.WaitGroup
	for range 8 {
		a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		wg.Go(func() {
			if code := a.run(t.Context(), args); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
		})
	}
	wg.Wait()

	if got := countRuns(t, marker); got != 1 {
		t.Errorf("command executed %d times, want 1 (the lock must collapse the stampede)", got)
	}
}

func TestKeyDistinguishesCommands(t *testing.T) {
	tests := [][]string{
		{"git", "log"},
		{"git", "lo", "g"},
		{"gitlog"},
		{"git", "log", ""},
		{"git"},
	}

	seen := make(map[string][]string, len(tests))
	for _, args := range tests {
		k := key(args)
		if prev, dup := seen[k]; dup {
			t.Errorf("key(%q) collides with key(%q)", args, prev)
		}
		seen[k] = args
	}
}

func TestKeyDependsOnWorkingDirectory(t *testing.T) {
	args := []string{"pwd"}

	first := key(args)
	t.Chdir(t.TempDir())
	if second := key(args); second == first {
		t.Error("key is identical across directories, want it to change")
	}
}

func TestFresh(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		ttl  time.Duration
		want bool
	}{
		{"within ttl", -time.Second, time.Minute, true},
		{"older than ttl", -time.Hour, time.Minute, false},
		{"exactly at ttl", -time.Minute, time.Minute, false},
		{"stored in the future", time.Hour, time.Minute, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := entry{StoredAt: time.Now().Add(tt.age)}
			if got := fresh(e, tt.ttl); got != tt.want {
				t.Errorf("fresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStoreLeavesNoTemporaryFiles(t *testing.T) {
	base := t.TempDir()
	t.Setenv("RECALL_CACHE_DIR", base)

	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	want := entry{StoredAt: time.Now(), ExitCode: 7, Stdout: []byte("out"), Stderr: []byte("err")}
	a.store(dir, "entry.json", want)

	got, ok := load(dir, "entry.json")
	if !ok {
		t.Fatal("entry was not stored")
	}
	if got.ExitCode != want.ExitCode || string(got.Stdout) != "out" || string(got.Stderr) != "err" {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	names, err := filepath.Glob(filepath.Join(base, "recall", "*.tmp.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("temporary files left behind: %v", names)
	}
}

func TestCacheDirRejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits; ownership is checked by SID")
	}
	base := t.TempDir()
	t.Setenv("RECALL_CACHE_DIR", base)

	path := filepath.Join(base, "recall")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := cacheDir(); err == nil {
		t.Error("cacheDir accepted a world-readable directory, want an error")
	}
}

func TestCacheDirAcceptsOwnedDirectory(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())

	dir, err := cacheDir()
	if err != nil {
		t.Fatalf("cacheDir() = %v, want a usable directory we own", err)
	}
	dir.Close()
}

func TestCapBufferOverflowDiscardsContent(t *testing.T) {
	c := &capBuffer{limit: 10}

	if n, err := c.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatalf("Write() = %d, %v, want 5, nil", n, err)
	}
	if got := string(c.Bytes()); got != "12345" {
		t.Errorf("Bytes() = %q, want %q", got, "12345")
	}

	if n, err := c.Write([]byte("678901")); n != 6 || err != nil {
		t.Fatalf("Write() = %d, %v, want 6, nil", n, err)
	}
	if !c.overflow {
		t.Error("overflow = false, want true")
	}
	if c.Bytes() != nil {
		t.Errorf("Bytes() = %q, want nil after overflow", c.Bytes())
	}
}

func backdate(t *testing.T, dir *os.Root, name string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	if err := dir.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRemovesOldEntriesAndKeepsFresh(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	a.store(dir, "old.json", entry{StoredAt: time.Now(), Stdout: []byte("stale")})
	a.store(dir, "new.json", entry{StoredAt: time.Now(), Stdout: []byte("current")})

	backdate(t, dir, "old.json")

	if code := a.prune(dir, 24*time.Hour); code != 0 {
		t.Fatalf("prune exit code = %d, want 0", code)
	}

	if _, ok := load(dir, "old.json"); ok {
		t.Error("an entry older than max-age survived prune")
	}
	if _, ok := load(dir, "new.json"); !ok {
		t.Error("an entry within max-age was pruned")
	}
}

func TestPruneRemovesUnheldLockFiles(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	f, err := dir.OpenFile("x.json.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	backdate(t, dir, "x.json.lock")

	if code := a.prune(dir, time.Hour); code != 0 {
		t.Fatalf("prune exit code = %d, want 0", code)
	}
	if _, err := dir.Stat("x.json.lock"); err == nil {
		t.Error("an unused lock file survived prune")
	}
}

func TestPruneKeepsHeldLockFiles(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	release, ok := a.lock(t.Context(), dir, "held.json.lock")
	if !ok {
		t.Fatal("could not take the lock")
	}
	defer release()
	backdate(t, dir, "held.json.lock")

	if code := a.prune(dir, time.Hour); code != 0 {
		t.Fatalf("prune exit code = %d, want 0", code)
	}
	if _, err := dir.Stat("held.json.lock"); err != nil {
		t.Errorf("prune removed a lock file that was being held: %v", err)
	}
}

func TestLockDetectsReplacedLockFile(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	release, ok := a.lock(t.Context(), dir, "swap.json.lock")
	if !ok {
		t.Fatal("could not take the lock")
	}
	if err := dir.Remove("swap.json.lock"); err != nil {
		t.Fatal(err)
	}
	release()

	release2, ok := a.lock(t.Context(), dir, "swap.json.lock")
	if !ok {
		t.Fatal("could not take the lock after the file was replaced")
	}
	release2()
}

func TestPruneRemovesOrphanedTempFiles(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	orphan := "abc.json.tmp.deadbeef"
	f, err := dir.OpenFile(orphan, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	backdate(t, dir, orphan)

	if code := a.prune(dir, time.Hour); code != 0 {
		t.Fatalf("prune exit code = %d, want 0", code)
	}
	if _, err := dir.Stat(orphan); err == nil {
		t.Error("an orphaned temporary file survived prune")
	}
}

func TestRunPruneFlagRemovesCachedResult(t *testing.T) {
	a, out, _ := newApp(t)

	marker := filepath.Join(t.TempDir(), "runs")
	cmd := helperCmd(t, "-marker="+marker)
	if code := a.run(t.Context(), recallArgs([]string{"--ttl=1h"}, cmd)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	out.Reset()
	if code := a.run(t.Context(), []string{"--prune", "--max-age=0"}); code != 0 {
		t.Fatalf("prune exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "pruned 1 result") {
		t.Errorf("prune output = %q, want it to report one pruned entry", out.String())
	}

	if code := a.run(t.Context(), recallArgs([]string{"--ttl=1h"}, cmd)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := countRuns(t, marker); got != 2 {
		t.Errorf("command executed %d times, want 2 (prune should have cleared the entry)", got)
	}
}

func TestPruneDoesNotBreakMutualExclusion(t *testing.T) {
	t.Setenv("RECALL_CACHE_DIR", t.TempDir())

	// The run count and the prune maxAge are load-bearing.
	marker := filepath.Join(t.TempDir(), "runs")
	args := recallArgs([]string{"--ttl=1ns"}, helperCmd(t, "-marker="+marker, "-sleep=5ms"))

	done := make(chan struct{})
	var pruner sync.WaitGroup
	pruner.Go(func() {
		p := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		dir, err := cacheDir()
		if err != nil {
			return
		}
		defer dir.Close()
		for {
			select {
			case <-done:
				return
			default:
				p.prune(dir, 200*time.Millisecond)
			}
		}
	})

	var runners sync.WaitGroup
	for range 3 {
		a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		runners.Go(func() {
			for range 2 {
				a.run(t.Context(), args)
			}
		})
	}
	runners.Wait()
	close(done)
	pruner.Wait()

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	depth, overlaps, executions := 0, 0, 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		switch line {
		case "run":
			executions++
			depth++
			if depth > 1 {
				overlaps++
			}
		case "done":
			depth--
		}
	}
	if executions == 0 {
		t.Fatal("the command never ran")
	}
	if overlaps != 0 {
		t.Errorf("%d overlapping executions across %d runs; prune broke mutual exclusion", overlaps, executions)
	}
	t.Logf("%d executions, no overlap, while prune ran continuously", executions)
}
