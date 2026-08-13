// Command recall caches the output of a CLI command for a TTL.
package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	cacheFormat = "v2"
	cacheExt    = ".pb"

	lockWait = 10 * time.Second
	lockPoll = 25 * time.Millisecond
)

const (
	exitUsage    = 2
	exitInternal = 1
	exitNotRun   = 127
)

var version = "dev"

type entry struct {
	StoredAt time.Time `json:"stored_at,omitzero"`
	ExitCode int       `json:"exit_code"`
	Stdout   []byte    `json:"stdout,omitzero"`
	Stderr   []byte    `json:"stderr,omitzero"`
}

type app struct {
	stdout, stderr io.Writer
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	os.Exit(a.run(ctx, os.Args[1:]))
}

func (a *app) run(ctx context.Context, argv []string) int {
	flags := flag.NewFlagSet("recall", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	flags.Usage = func() {
		fmt.Fprintln(a.stderr, "usage: recall [flags] -- <command> [args...]")
		flags.PrintDefaults()
	}

	ttl := flags.Duration("ttl", 30*time.Second, "how long a cached result stays valid")
	force := flags.Bool("force", false, "ignore any cached result and re-run")
	timeout := flags.Duration("timeout", 0, "kill the command if it runs longer than this (0 disables)")
	maxOutput := flags.Int64("max-output", 1<<20, "do not cache results whose output exceeds this many bytes")
	prune := flags.Bool("prune", false, "delete cached results older than -max-age, then exit")
	maxAge := flags.Duration("max-age", 24*time.Hour, "age at which -prune deletes a cached result")
	showVersion := flags.Bool("version", false, "print version and exit")

	if err := flags.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}

	if *prune {
		dir, err := cacheDir()
		if err != nil {
			a.warn("%v", err)
			return exitInternal
		}
		defer dir.Close()
		return a.prune(dir, *maxAge)
	}
	if *showVersion {
		fmt.Fprintln(a.stdout, version)
		return 0
	}

	args := flags.Args()
	if len(args) == 0 {
		flags.Usage()
		return exitUsage
	}

	dir, err := cacheDir()
	if err != nil {
		a.warn("%v", err)
		return exitInternal
	}
	defer dir.Close()

	name := key(args) + cacheExt

	if !*force {
		if e, ok := load(dir, name); ok && fresh(e, *ttl) {
			return a.recall(e)
		}
	}

	if release, ok := a.lock(ctx, dir, name+".lock"); ok {
		defer release()
		if !*force {
			if e, ok := load(dir, name); ok && fresh(e, *ttl) {
				return a.recall(e)
			}
		}
	}

	e, cacheable := a.execute(ctx, args, *timeout, *maxOutput)
	if cacheable {
		a.store(dir, name, e)
	}
	return e.ExitCode
}

func key(args []string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "\x00nocwd\x00" + err.Error()
	}
	h := sha256.New()
	for _, part := range slices.Concat([]string{cacheFormat, cwd}, args) {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *app) execute(ctx context.Context, args []string, timeout time.Duration, maxOutput int64) (entry, bool) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out := &capBuffer{limit: maxOutput}
	errb := &capBuffer{limit: maxOutput}
	cmd.Stdout = io.MultiWriter(a.stdout, out)
	cmd.Stderr = io.MultiWriter(a.stderr, errb)
	cmd.WaitDelay = 5 * time.Second

	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			a.warn("cannot run %q: %v", args[0], err)
			return entry{ExitCode: exitNotRun}, false
		}
		code = exitCode(ee)
	}

	e := entry{StoredAt: time.Now(), ExitCode: code, Stdout: out.Bytes(), Stderr: errb.Bytes()}

	switch {
	case ctx.Err() != nil:
		return e, false
	case out.overflow || errb.overflow:
		a.warn("output exceeded %d bytes; result not cached", maxOutput)
		return e, false
	}
	return e, true
}

func (a *app) recall(e entry) int {
	a.stdout.Write(e.Stdout)
	a.stderr.Write(e.Stderr)
	return e.ExitCode
}

func load(dir *os.Root, name string) (entry, bool) {
	b, err := dir.ReadFile(name)
	if err != nil {
		return entry{}, false
	}
	e, err := decodeEntry(b)
	if err != nil {
		return entry{}, false
	}
	return e, true
}

func (a *app) store(dir *os.Root, name string, e entry) {
	b, err := encodeEntry(e)
	if err != nil {
		a.warn("cannot encode cache entry: %v", err)
		return
	}

	tmp := name + ".tmp." + strconv.FormatUint(rand.Uint64(), 16)
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		a.warn("cannot create cache file: %v", err)
		return
	}
	// An empty tmp means the rename succeeded, so the cleanup deletes nothing.
	defer func() {
		if tmp != "" {
			dir.Remove(tmp)
		}
	}()

	if _, err := f.Write(b); err != nil {
		f.Close()
		a.warn("cannot write cache entry: %v", err)
		return
	}
	if err := f.Close(); err != nil {
		a.warn("cannot write cache entry: %v", err)
		return
	}
	if err := dir.Rename(tmp, name); err != nil {
		a.warn("cannot publish cache entry: %v", err)
		return
	}
	tmp = ""
}

func (a *app) prune(dir *os.Root, maxAge time.Duration) int {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		a.warn("cannot read cache directory: %v", err)
		return exitInternal
	}

	cutoff := time.Now().Add(-maxAge)
	var results, locks int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			continue // The file vanished, which is the outcome prune wants.
		}
		if info.ModTime().After(cutoff) {
			continue
		}

		switch {
		case strings.HasSuffix(name, ".lock"):
			if tryRemoveLock(dir, name) {
				locks++
			}
		case prunable(name):
			if err := dir.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				a.warn("cannot remove %s: %v", name, err)
				continue
			}
			results++
		}
	}

	fmt.Fprintf(a.stdout, "pruned %s and %s\n", count(results, "result"), count(locks, "lock file"))
	return 0
}

func prunable(name string) bool {
	return strings.HasSuffix(name, cacheExt) ||
		strings.Contains(name, cacheExt+".tmp.") ||
		strings.HasSuffix(name, ".json") ||
		strings.Contains(name, ".json.tmp.")
}

func encodeEntry(e entry) ([]byte, error) {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(e.StoredAt.UnixNano()))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(e.ExitCode))
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendBytes(b, e.Stdout)
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendBytes(b, e.Stderr)
	return b, nil
}

func decodeEntry(b []byte) (entry, error) {
	var e entry
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return entry{}, protowire.ParseError(n)
		}
		b = b[n:]

		switch num {
		case 1:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return entry{}, protowire.ParseError(m)
			}
			e.StoredAt = time.Unix(0, int64(v))
			b = b[m:]
		case 2:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return entry{}, protowire.ParseError(m)
			}
			e.ExitCode = int(v)
			b = b[m:]
		case 3:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return entry{}, protowire.ParseError(m)
			}
			e.Stdout = append([]byte(nil), v...)
			b = b[m:]
		case 4:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return entry{}, protowire.ParseError(m)
			}
			e.Stderr = append([]byte(nil), v...)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return entry{}, protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return e, nil
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fresh(e entry, ttl time.Duration) bool {
	age := time.Since(e.StoredAt)
	return age >= 0 && age < ttl
}

func cacheDir() (*os.Root, error) {
	base := cmp.Or(os.Getenv("RECALL_CACHE_DIR"), defaultCacheBase())
	path := filepath.Join(base, "recall")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(root); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func exitCode(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}

func confirmLock(f *os.File, dir *os.Root, name string) bool {
	if !sameFile(f, dir, name) {
		return false
	}
	now := time.Now()
	dir.Chtimes(name, now, now)
	return true
}

func sameFile(f *os.File, dir *os.Root, name string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	current, err := dir.Stat(name)
	if err != nil {
		return false
	}
	return os.SameFile(held, current)
}

func (a *app) warn(format string, args ...any) {
	fmt.Fprintf(a.stderr, "recall: "+format+"\n", args...)
}

type capBuffer struct {
	buf      bytes.Buffer
	limit    int64
	overflow bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if !c.overflow {
		if int64(c.buf.Len())+int64(len(p)) > c.limit {
			c.overflow = true
			c.buf.Reset()
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *capBuffer) Bytes() []byte {
	if c.overflow {
		return nil
	}
	return c.buf.Bytes()
}
