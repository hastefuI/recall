# recall [![Build](https://github.com/hastefuI/recall/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/hastefuI/recall/actions/workflows/ci.yml) [![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/hastefuI/recall/blob/main/LICENSE)

Cache and recall CLI command results with a TTL. 🔄

## Overview

recall is used to avoid re-running expensive commands.

It caches a command's result for a short window of time and returns the result from cache
when the same command is invoked again with the same set of arguments.

Example:
```bash
$ time recall -- gh api user
{"login":"octocat","id":1,"type":"User",...}
real    0m0.31s

$ time recall -- gh api user
{"login":"octocat","id":1,"type":"User",...}
real    0m0.01s
```

recall is particularly useful in CI pipelines. A job may query the same API from
several steps: once to decide what to build, again to label the result, again to
report the outcome. Each call executes in full, even though the answer is
identical. When parallel jobs share a rate limit, the duplicate calls can
exhaust it and fail the run.

Development tooling follows the same pattern. Build scripts, code generators,
and editor integrations often run the same command many times in one session.

A short TTL backed cache keeps those loops responsive without changing the tools themselves.

recall aims to stay lightweight and to keep its dependencies to a minimum. It
needs no daemon, no config file, and no change to the command it wraps.

Use it for any command that is slow, rate-limited, or billed per call.

## Features

- Zero configuration. Install the binary and start caching.
- Cached calls return the same output and the same exit status as the original.
- Concurrent duplicate calls collapse into a single run.
- The cache stays private to you, and an untrusted directory is refused.
- One static binary, no runtime dependencies. Runs on Linux, macOS, FreeBSD,
  and Windows.

## Installation

### Build From Source

recall requires Go 1.27 or newer. Clone this repository. Then run one of these
commands:

```bash
# Build
$ go build -o recall .

# Install
$ go install .
```

### Docker

```bash
$ docker build -t recall .
```

The image runs as an unprivileged user. The image caches to `/tmp/recall`.
recall can only cache a command that the image contains. Use the image as a
base. The base image drops to a non-root user, so switch to root to install
packages:

```dockerfile
FROM recall
USER root
RUN apk add --no-cache github-cli jq
USER recall
```

A cache only helps next to the command it caches. A derived image pairs recall
with the commands that your pipeline repeats. Each duplicate call then reads a
file instead of the network. This pays off when an answer costs a lot and the
pipeline asks for it often.

Mount a volume over `/tmp/recall`. The cache then outlives the container, and
every step in the pipeline shares it:

```bash
$ docker build -t ci-tools .
$ docker run --rm -v recall-cache:/tmp/recall -e GH_TOKEN ci-tools \
    --ttl=10m --env=GH_TOKEN -- gh api user
```

If your image already contains the commands you need, add recall to that image
instead:

```dockerfile
FROM your-existing-tools-image
COPY --from=recall /usr/local/bin/recall /usr/local/bin/recall
ENTRYPOINT ["recall"]
```

### Verify Installation

Run this command to verify the installation:

```bash
$ recall -- echo recalled
```

## Quick Start

Put `recall --` in front of a slow command:

```bash
recall -- kubectl get pods -o json
```

Run the same command again. recall returns the stored result at once. To set how
long the result stays valid, use `--ttl`:

```bash
recall --ttl=10s -- kubectl get pods -o json
```

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

recall keys the cache on the working directory, the command, and the arguments.
It ignores the environment, because it cannot know which variables a command
reads. Name the ones that matter with `--env`, or two calls that differ only in
a token or a profile share one result:

```bash
recall --ttl=10m --env=GH_TOKEN -- gh api user
recall --ttl=5m --env=AWS_PROFILE,AWS_REGION -- aws sts get-caller-identity
```

## License

Licensed under [MIT License](https://opensource.org/licenses/MIT), see [LICENSE](./LICENSE) for details.

Copyright (c) 2026 hasteful.
