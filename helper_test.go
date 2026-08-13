package main

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "-helper" {
		os.Exit(helper(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func helper(argv []string) int {
	fs := flag.NewFlagSet("helper", flag.ContinueOnError)
	marker := fs.String("marker", "", "append a line to this file")
	out := fs.String("stdout", "", "write this to stdout")
	errOut := fs.String("stderr", "", "write this to stderr")
	sleep := fs.Duration("sleep", 0, "stay alive this long before exiting")
	code := fs.Int("exit", 0, "exit with this status")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	if *marker != "" {
		mark(*marker, "run")
	}
	if *out != "" {
		fmt.Print(*out)
	}
	if *errOut != "" {
		fmt.Fprint(os.Stderr, *errOut)
	}
	time.Sleep(*sleep)
	if *marker != "" {
		mark(*marker, "done")
	}
	return *code
}

func mark(path, word string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Fprintln(f, word)
	f.Close()
}

func helperCmd(t *testing.T, args ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{exe, "-helper"}, args...)
}

func recallArgs(flags []string, cmd []string) []string {
	return append(append(append([]string{}, flags...), "--"), cmd...)
}
