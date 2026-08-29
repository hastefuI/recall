# recall [![Build](https://github.com/hastefuI/recall/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/hastefuI/recall/actions/workflows/ci.yml) [![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev) [![Release](https://img.shields.io/github/v/release/hastefuI/recall)](https://github.com/hastefuI/recall/releases) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/hastefuI/recall/blob/main/LICENSE)

Cache and recall CLI command results with a TTL.

<img src="./demo.gif" alt="A GitHub search API query taking 1.8s, then 0.006s from cache" style="width:100%; max-width:900px;" />

## Overview

`recall` is a command wrapper for avoiding repeated execution of slow, rate-limited, or expensive commands.

To use it, simply place `recall --` in front of an existing command. The first invocation runs normally and stores the result. Identical invocations from the same working directory return the cached result until the TTL expires.

```bash
$ time recall -- gh api user
{"login":"octocat","id":1,"type":"User",...}

real    0m0.31s

$ time recall -- gh api user
{"login":"octocat","id":1,"type":"User",...}

real    0m0.01s
```

Cached results preserve stdout, stderr, and the exit status of the original command.

`recall` requires no daemon or configuration file and does not require changing the command it wraps.

## Features

- TTL-based caching with configurable expiration
- Replays stdout, stderr, and exit status
- Concurrent duplicate calls collapse into a single execution
- Optional environment variables for cache-sensitive commands
- Optional execution timeouts and output limits
- Cache pruning for removing old results
- User-private cache storage
- One static binary with no runtime dependencies
- Runs on Linux, macOS, FreeBSD, and Windows

## Installation

### Homebrew

```bash
$ brew tap hastefui/tap
$ brew install --cask recall
```

### Pre-built Binaries

Download and extract the latest release for your platform from the repository's Releases page.

Release binaries are available for Linux, macOS, FreeBSD, and Windows on amd64 and arm64.

### Build From Source

`recall` requires Go 1.27 or newer.

```bash
$ go build -o recall .
$ go install .
```

### Verify Installation

```bash
$ recall --version
```

Or run a command through `recall`:

```bash
$ recall -- echo recalled
recalled
```

## Quick Start

Put `recall --` in front of a command:

```bash
$ recall -- kubectl get pods -o json
```

Run the same command again and `recall` returns the stored result.

The default TTL is 30 seconds. Use `--ttl` to change it:

```bash
$ recall --ttl=10s -- kubectl get pods -o json
```

Once the TTL expires, the command executes again and replaces the cached result.

## Usage

```bash
recall [flags] -- <command> [args...]

  --ttl         how long a cached result stays valid   (default 30s)
  --force       ignore any cached result and re-run    (default false)
  --env         add a variable to the cache key        (repeatable)
  --timeout     kill the command if it runs longer     (default 0, disabled)
  --max-output  skip caching results larger than this  (default 1MiB)
  --prune       delete old cached results, then exit   (default false)
  --max-age     age at which --prune deletes a result  (default 24h)
  --version     print version and exit
```

## Examples

### Cache an API Request

Cache a GitHub API response for 10 minutes:

```bash
$ recall --ttl=10m -- gh api user
```

Repeated calls return the same result without another API request.

### Cache Kubernetes State

Use a short TTL for frequently requested cluster state:

```bash
$ recall --ttl=10s -- kubectl get pods -o json
```

### Include Environment Variables

Use `--env` when the result of a command depends on an environment variable:

```bash
$ recall --ttl=10m --env=GH_TOKEN -- gh api user
```

Multiple variables can be provided as a comma-separated list:

```bash
$ recall --ttl=5m --env=AWS_PROFILE,AWS_REGION -- \
    aws sts get-caller-identity
```

`--env` is also repeatable:

```bash
$ recall --env=AWS_PROFILE --env=AWS_REGION -- \
    aws sts get-caller-identity
```

### Force a Refresh

Ignore an existing cached result and execute the command again:

```bash
$ recall --force -- gh api user
```

### Set an Execution Timeout

Stop a command that runs longer than expected:

```bash
$ recall --timeout=30s -- some-expensive-command
```

Timed-out commands are not cached.

### Limit Cached Output

Skip caching when command output exceeds a configured size:

```bash
$ recall --max-output=5242880 -- some-command
```

The command still runs normally, but oversized output is not stored.

## Use Cases

- CI pipelines that repeat the same API requests across steps
- Development scripts that repeatedly execute slow commands
- Rate-limited APIs and CLI tools
- Commands backed by billed APIs or compute
- Kubernetes and infrastructure status queries
- Build tooling and code generation
- Short-lived API and metadata lookups

## Docker

Build the image:

```bash
$ docker build -t recall .
```

The image runs as an unprivileged user and caches results under `/tmp/recall`.

`recall` can only execute commands that exist inside the container. For CI use, create a derived image containing the tools that need to be cached:

```dockerfile
FROM recall

USER root
RUN apk add --no-cache github-cli jq
USER recall
```

Build the derived image:

```bash
$ docker build -t ci-tools .
```

Mount a volume over `/tmp/recall` to persist the cache between containers:

```bash
$ docker run --rm \
    -v recall-cache:/tmp/recall \
    -e GH_TOKEN \
    ci-tools \
    --ttl=10m --env=GH_TOKEN -- gh api user
```

If an existing image already contains the required tools, copy `recall` into it instead:

```dockerfile
FROM your-existing-tools-image

COPY --from=recall /usr/local/bin/recall /usr/local/bin/recall

ENTRYPOINT ["recall"]
```

## License

Licensed under the MIT License. See `LICENSE` for details.

Copyright (c) 2026 hasteful.
