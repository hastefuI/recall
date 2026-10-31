# Contributing to recall

Thanks for your interest in contributing. This is a small project, so the guidelines are short.

## Prerequisites

- [Go](https://go.dev/doc/install) 1.27 or later
- [Git](https://git-scm.com/downloads)

## Getting started

Clone this repo and then:

```bash
$ go build -o recall .
$ go test -race ./...
$ ./recall -- echo recalled
```

## Before you open a pull request

Run the checks CI runs:

```bash
$ gofmt -l .
$ go vet ./...
$ go test -race ./...
$ GOOS=windows go vet ./...
$ go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

`gofmt -l .` must print nothing. The Windows lock has no runner on macOS or
Linux, so `GOOS=windows go vet ./...` is the local check for that code.

## Layout

- `main.go` holds flags, the cache key, store, load, and prune.
- `os_unix.go` and `os_windows.go` hold the cache location, the ownership
  check, and the lock.
- `helper_test.go` holds the re-exec harness. `main_test.go` holds the tests.

Put new platform code in the `os_*.go` files. Put everything else in `main.go`.

## Code style

- Standard Go, targeting the version in `go.mod`.
- Tests use the standard library only. Do not add a test dependency.
- Do not add a comment to any Go file, the Dockerfile, or the CI workflow.

## Commits

[Conventional Commits](https://www.conventionalcommits.org).

- Types in use: `feat`, `fix`, `docs`, `test`, `refactor`, `ci`, `build`,
  `chore`.
- Scope is optional, lowercase, and names an area rather than a file.
- Imperative mood, lowercase after the colon, no trailing period.
- Keep the whole subject under 72 characters.

```
feat(cache): add --env to include named variables in the cache key
docs(readme): add badge for the latest release
```

## Pull requests

1. Fork the repository and branch from `main`.
2. Keep the branch to one change.
3. Cover new behavior with a test in `main_test.go`.
4. Open the pull request with a conventional-commit title and a short summary
   of what changed and why.

## Security

Do not open a public issue for a vulnerability. Use [private vulnerability
reporting](https://github.com/hastefuI/recall/security) as described in
[SECURITY.md](../SECURITY.md).

## License

By contributing you agree that your contributions are licensed under the
[MIT License](../LICENSE).
