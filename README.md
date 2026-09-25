# gospy

[![Go Report Card](https://goreportcard.com/badge/github.com/hakastein/gospy)](https://goreportcard.com/report/github.com/hakastein/gospy)
[![Pull Request Checks](https://github.com/hakastein/gospy/actions/workflows/pull-request.yml/badge.svg)](https://github.com/hakastein/gospy/actions/workflows/pull-request.yml)

![gospy.jpg](gospy.jpg)

**gospy** runs the [phpspy](https://github.com/adsr/phpspy) sampling profiler next to php-fpm and
ships what it sees to [Pyroscope](https://pyroscope.io/). phpspy reads PHP stacks out of running
workers without touching your code. gospy starts it as a child process, folds its output into
stacks, groups them by tags (static ones such as `env=production`, and per-request ones such as the
URI taken from `$_SERVER`), and sends a batch per tag set to Pyroscope's `/ingest` endpoint every
few seconds. You get continuous flame graphs of production PHP that you can slice by tag.

## Privileges

phpspy attaches to PHP processes with `ptrace`, so gospy has to run as root or with
`CAP_SYS_PTRACE`. The kernel must also allow the attach: `kernel.yama.ptrace_scope` of `0`, `1` or
`2` works with that capability, and `3` blocks `ptrace` entirely. gospy must share a PID namespace
with php-fpm, which in practice means the same container (`cap_add: [SYS_PTRACE]` under Docker).
Whatever can run code as gospy can read your PHP workers' memory. Read [SECURITY.md](SECURITY.md)
before you deploy it.

## Contents

- [Privileges](#privileges)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Command line](#command-line)
- [Tags](#tags)
- [Entry points](#entry-points)
- [Restart policy](#restart-policy)
- [Delivery to Pyroscope](#delivery-to-pyroscope)
- [phpspy compatibility](#phpspy-compatibility)
- [Running in a container](#running-in-a-container)
- [Operations](#operations)
- [Security](#security)
- [Performance](#performance)
- [Contributing](#contributing)

## Installation

This README describes the command line on `main`. The release archives, `checksums.txt` and the
container image described below are published from the first release after v0.11.0; the examples
use `0.12.0`. v0.11.0 and older ship a single `v<version>.tar.gz` with an older flag set, so read
the README at that tag if you run one of them.

gospy is useful only on Linux, because phpspy is Linux-only. The darwin archives exist so the
binary can be inspected and tested on a Mac.

### Release archive

Every [release](https://github.com/hakastein/gospy/releases) publishes
`gospy_<version>_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64, plus a
`checksums.txt` that covers every file. `<version>` is the tag without its leading `v`.

```bash
VERSION=0.12.0
ARCH=amd64 # or arm64
curl -fsSLO "https://github.com/hakastein/gospy/releases/download/v${VERSION}/gospy_${VERSION}_linux_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/hakastein/gospy/releases/download/v${VERSION}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "gospy_${VERSION}_linux_${ARCH}.tar.gz" gospy
sudo install -m 0755 gospy /usr/local/bin/gospy
gospy --version
```

Each release also publishes an SPDX SBOM for every archive (`<archive>.sbom.json`), and each
archive has a GitHub build attestation that ties it to the workflow run and commit that built it:

```bash
gh attestation verify "gospy_${VERSION}_linux_${ARCH}.tar.gz" --repo hakastein/gospy
```

### go install

Needs Go 1.26 or newer:

```bash
go install github.com/hakastein/gospy/cmd/gospy@latest
```

Pin a release with `@v0.12.0` in place of `@latest`. A binary installed this way reports the
module version it was built from in `gospy --version` (for example `v0.12.0`), not `dev`.

### Building from source

Needs Go 1.26 or newer and `make`:

```bash
git clone https://github.com/hakastein/gospy.git
cd gospy
make build VERSION=$(git describe --tags)
./gospy --version
```

`make build` writes `./gospy` from `./cmd/gospy` and stamps `VERSION` into the binary. Without
`VERSION` it reports `dev`. It builds a static `linux/amd64` binary by default; for arm64 add
`GOARCH=arm64`.

### Container image

`ghcr.io/hakastein/gospy:<version>` (and `:latest`) is a multi-arch (linux amd64 and arm64) image
that holds the static binary at `/gospy` and nothing else. gospy has to run in the same container
as php-fpm, so the image is a source to copy from, not something to run:

```dockerfile
COPY --from=ghcr.io/hakastein/gospy:0.12.0 /gospy /usr/local/bin/gospy
```

A complete image is shown in [Running in a container](#running-in-a-container).

## Quick start

Install phpspy (see [Installing phpspy](#installing-phpspy)). Then, as root or with
`CAP_SYS_PTRACE`, on the host or in the container where php-fpm runs:

```bash
export GOSPY_PYROSCOPE_AUTH='<ingest token>'

gospy \
  --pyroscope=https://pyroscope.example.com \
  --app=my-app \
  --tag=env=production \
  --tag='uri={{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}' \
  phpspy -P php-fpm -H 99 -T 8 -b 65536 -J m -c -g server.REQUEST_URI
```

Here is what each part does:

- `--pyroscope`: the Pyroscope base URL. gospy posts to `<url>/ingest`.
- `GOSPY_PYROSCOPE_AUTH`: the token, sent as `Authorization: Bearer <token>`. It is read from the
  environment so that it stays off the command line.
- `--app`: the application name in Pyroscope.
- `--tag=env=production`: a static tag on every profile.
- `--tag='uri={{ ... }}'`: a dynamic tag. It takes the `glopeek server.REQUEST_URI` value that
  phpspy prints for each sample, strips the query string with the regex, and tags the sample with
  the result, so `/orders/42?utm_source=mail` becomes `uri=/orders/42`.
- Everything from `phpspy` on is phpspy's own command line:
  - `-P php-fpm` attaches to every process that `pgrep php-fpm` finds.
  - `-H 99` samples at 99 Hz. gospy reads this rate and reports it to Pyroscope.
  - `-T 8` runs 8 tracer threads.
  - `-b 65536` raises the output buffer for deep stacks, and `-J m` stops the threads from
    interlacing their output (see [Interlaced writes](#interlaced-writes)).
  - `-c` keeps going after an error.
  - `-g server.REQUEST_URI` prints `$_SERVER['REQUEST_URI']` with every sample, and the `uri` tag
    reads it from there.

Stop gospy with `SIGTERM` or `Ctrl-C`. It stops phpspy, sends the batches it still holds, and
exits 0.

## Command line

```text
gospy [gospy flags] phpspy [phpspy flags]
```

gospy's flags come first. The first word that is not a flag names the profiler, and every argument
after it is passed to phpspy unchanged. A gospy flag written after `phpspy` goes to phpspy, and
gospy logs a warning about it.

This table matches `gospy --help`:

| Flag | Default | Description |
| --- | --- | --- |
| `--pyroscope` | required | Pyroscope server URL, `http://` or `https://`. Samples are posted to `<url>/ingest`, and a query string in the URL is kept. |
| `--pyroscope-auth` | none | Pyroscope token, sent as a `Bearer` token. Environment: `GOSPY_PYROSCOPE_AUTH`, which is the preferred form because the command line is readable by every process in the PID namespace. gospy warns when a token is set on a plain `http://` URL. |
| `--pyroscope-timeout` | `10s` | Timeout for each request to Pyroscope, and for each retry of it. |
| `--pyroscope-workers` | `5` | Number of requests sent to Pyroscope concurrently. Must be at least `1`. |
| `--app` | required | Application name in Pyroscope. |
| `--tag` | none | Static (`key=value`) or dynamic (`key={{ "meta.key" }}`, `key={{ "meta.key" "regex" "replacement" }}`) tag. Repeatable. See [Tags](#tags). |
| `--tag-entrypoint` | `false` | Tag every sample with `entrypoint=<script path>`. |
| `--rate-mb` | `4` | Upload rate limit in MB per second (1 MB = 1,048,576 bytes). `0` means unlimited. Fractions are allowed. |
| `--rate-burst-mb` | `6` | Upload burst in MB. Must be above zero unless `--rate-mb` is `0`. |
| `--restart` | `no` | When to restart phpspy after it exits: `always`, `onerror`, `onsuccess` or `no`. See [Restart policy](#restart-policy). |
| `--entrypoint` | none | Keep only samples whose entry script matches. Repeatable. See [Entry points](#entry-points). |
| `--keep-entrypoint-name` | `true` | Keep the entry script path in the root frame (`<main> /var/www/public/index.php`). `--keep-entrypoint-name=false` leaves only `<main>`, which merges the same code reached through different scripts. |
| `--instance-name` | `gospy` | Value of the `instance` field on every log line. Useful when several gospy processes log to one place. |
| `--batch-interval` | `5s` | Window over which samples are collected before a batch is sent. `0` or less means `5s`. |
| `--stats-interval` | `10s` | Interval of the [statistics log line](#statistics). `0` or less disables it. |
| `--drain-timeout` | `10s` | How long shutdown keeps sending buffered batches before it drops them. `0` or less means `10s`. A second `SIGTERM` or `SIGINT` ends the drain at once. |
| `-v`, `--verbose` | off | Once for `debug` logs, twice (`-vv`) for `trace`. |
| `-h`, `--help` | | Print help and exit. |
| `-V`, `--version` | | Print `gospy version <version>` and exit. |

Durations use Go syntax (`500ms`, `10s`, `1m`). A bare number such as `10` is rejected.

gospy exits with an error before it starts phpspy if a required flag is missing, the URL is not
`http` or `https`, `--pyroscope-workers` is below 1, a rate is negative, `--rate-burst-mb` is `0`
while a rate limit is set, `--restart` is not one of the four modes, a tag does not parse, the
profiler is not `phpspy`, or the phpspy flags break one of the
[rules below](#phpspy-compatibility).

## Tags

Every batch is sent to Pyroscope as `<app>{<static tags>,<dynamic tags>}`, so each distinct tag set
becomes its own series.

### Static tags

```bash
--tag=env=production --tag=host=web-01 --tag=region=eu-west-1
```

A key may contain `A-Z a-z 0-9 _ .`. A static value must not contain `{`, `}`, `=`, `,`, `"`,
whitespace or control characters. gospy refuses to start on such a value rather than send a broken
name. Whitespace around the key and the value is trimmed.

### Dynamic tags

A dynamic tag takes its value from each sample's meta lines. phpspy prints them under the stack as
`# <meta key> = <value>`, and the dynamic tag names the meta key in double quotes:

```bash
--tag='uri={{ "glopeek server.REQUEST_URI" }}'
```

The two accepted forms, exactly:

- `key={{ "meta key" }}`: the value as phpspy printed it.
- `key={{ "meta key" "regex" "replacement" }}`: the value rewritten with Go's
  [`regexp.ReplaceAllString`](https://pkg.go.dev/regexp#Regexp.ReplaceAllString), so
  `$1` and `${name}` refer to capture groups. The replacement may be empty (`""`), which deletes
  whatever the regex matches.

Blanks between the braces and the quoted strings are optional. Anything else inside the braces
(two quoted strings, four or more, or text outside the quotes) is an error. A value that opens with
`{{` is always read as a dynamic tag, so an unterminated `{{`, a missing `}}` or a malformed body
stops gospy at startup instead of turning into a static tag.

Inside a quoted string a backslash escapes the next character, so `\"` is a literal quote. To pass
a backslash to the regex, double it: `\\?` reaches the regex as `\?`, a literal question mark.
Single-quote the whole `--tag` argument in the shell so that the shell passes backslashes and `$1`
unchanged.

Examples:

```bash
# URI without the query string: /orders/42?utm_source=mail -> /orders/42
--tag='uri={{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'

# The same with an empty replacement
--tag='uri={{ "glopeek server.REQUEST_URI" "\\?.*$" "" }}'

# Numeric ids collapsed into a route: /orders/42 -> /orders/:id
--tag='route={{ "glopeek server.REQUEST_URI" "^/orders/[0-9]+(\\?.*)?$" "/orders/:id" }}'

# Request method
--tag='method={{ "glopeek server.REQUEST_METHOD" }}'
```

A dynamic value is sanitised after the regex runs: `,` becomes `͵` (U+0375, which looks like a
comma), and `{`, `}`, `=`, `"`, whitespace and control characters become `_`. A value can therefore
never break the series name. A sample that has no matching meta line simply lacks that tag. Several
tags may read the same meta key.

### Where the meta lines come from

The meta key is whatever phpspy prints between `# ` and ` = `. These phpspy flags produce the meta
lines a dynamic tag can use:

| phpspy flag | Meta key | Example tag |
| --- | --- | --- |
| `-g server.REQUEST_URI` (`--peek-global`) | `glopeek server.REQUEST_URI` | `uri={{ "glopeek server.REQUEST_URI" }}` |
| `-g server.HTTP_HOST` | `glopeek server.HTTP_HOST` | `vhost={{ "glopeek server.HTTP_HOST" }}` |
| `-r u` / `-r p` / `-r q` (`--request-info`) | `uri`, `path`, `qstring` | `script={{ "path" }}` |
| `-e 'var@file:line'` (`--peek-var`) | `varpeek var@file:line` | `tenant={{ "varpeek tenant@/app/src/Kernel.php:42" }}` |

`-g` reads the PHP superglobal, and PHP fills `$_SERVER` lazily (`auto_globals_jit`): a request
whose script has not touched `$_SERVER` yet has no `glopeek` line, and its samples carry no `uri`
tag. Most frameworks read `$_SERVER` during bootstrap, so this mainly affects samples taken before
that point.

A dynamic URI tag creates one series per distinct value. Collapse ids with a regex (see `route`
above) rather than tagging raw URIs on an application with unbounded URLs. gospy also caps what it
holds (see [Delivery to Pyroscope](#delivery-to-pyroscope)).

## Entry points

The entry point is the script that started the request, taken from the outermost frame of the
stack (`/var/www/public/index.php`). By default every sample is kept.

```bash
--entrypoint=index.php --entrypoint='/var/www/bin/**/*.php'
```

A pattern without `*`, `?` or `[` matches a path that is equal to it or ends in `/<pattern>`, so
`index.php` matches `/var/www/public/index.php`. A pattern with one of those characters is a
[doublestar](https://github.com/bmatcuk/doublestar) glob matched against the full path. Samples
whose entry point matches no pattern are discarded before they are counted anywhere.

`--tag-entrypoint` adds the entry point as the `entrypoint` tag, and `--keep-entrypoint-name`
decides whether it also stays in the root frame of the stack.

## Restart policy

`--restart` decides what happens when phpspy exits by itself:

| Mode | Restarts phpspy after |
| --- | --- |
| `no` (default) | never. gospy shuts down when phpspy does. |
| `always` | any exit. |
| `onerror` | a non-zero exit. A clean exit shuts gospy down. |
| `onsuccess` | a clean exit. A failure shuts gospy down. |

A restart that follows a failure waits first: 1s, then 2s, 4s and so on up to 1m. A clean exit, or
a session that ran for at least 30 seconds before failing, resets the delay and the failure count.
A nightly php-fpm reload therefore does not count against the budget. After **10 failures in a
row** gospy gives up, logs the last phpspy error and exits non-zero.

phpspy's stderr is logged at `warn` (at most 20 lines a second), so the reason for a failed attach
shows at the default verbosity. A phpspy that cannot be started at all, for example because the
binary is missing, is not retried.

## Delivery to Pyroscope

**Batching.** Samples are grouped by tag set and cut into a batch every `--batch-interval`
(`5s`). A batch holds each distinct stack with its count and the time range it covers, and is sent
as one `folded`-format request with the sample rate taken from phpspy's `-H`/`--rate-hz` or
`-s`/`--sleep-ns` (phpspy's default is 99 Hz). `--pyroscope-workers` requests run at once.

**Memory caps.** gospy holds at most 1,000 tag sets and 10,000 distinct stacks per tag set in the
current window, and at most 64 cut batches waiting for a worker. A tag set that reaches the stack
cap is cut early. Samples past these caps are dropped and counted as `dropped_samples`, so a
runaway dynamic tag or a Pyroscope outage costs samples, not memory.

**Rate limiting.** `--rate-mb` limits the request bodies gospy uploads, and `--rate-burst-mb` sets
how much may go at once. A batch larger than the burst is paced burst by burst, never dropped.
`--rate-mb=0` removes the limit, and the burst is then ignored.

**Retries.** A request is tried up to 5 times, waiting 500ms, then 1s, 2s and 4s between attempts
(never more than 30s). Network errors, timeouts, `429` and `5xx` are retried. A `Retry-After`
header, in seconds or as an HTTP date, replaces the computed wait, still capped at 30s. Other
`4xx` responses are final: the request is not repeated. A `3xx` is not followed either, so the
token never travels to a host you did not configure, and it counts as a final error.

**Failures are dropped.** When the last attempt fails, gospy logs an error and drops the batch. It
does not spool to disk. The statistics line counts it under `failed_requests` and in `errors`.

**Shutdown.** On the first `SIGTERM` or `SIGINT` gospy stops phpspy (`SIGTERM` to phpspy's process
group, `SIGKILL` after 2s), cuts every open batch and keeps sending for up to `--drain-timeout`
(`10s`). When the timeout passes, requests in flight are cancelled, the batches still queued are
counted as dropped, and gospy exits 0. A second `SIGTERM` or `SIGINT` during the drain abandons it
at once, and gospy exits 1.

**Delivery is at most once past the retries.** gospy holds a batch only while it retries it.
Nothing is spooled or sent again later, so a batch is lost for good when its retries run out, when
its samples exceed a memory cap, when the drain times out, or when gospy is killed. Losses show in
the statistics line and in the error logs. Within the retry window a batch can be stored twice: if
Pyroscope accepted an attempt but the response never arrived (a timeout, for example), the retry
sends it again. Size `--drain-timeout` below your orchestrator's stop grace period (Docker's default
is 10s, Kubernetes' 30s) so that the drain can finish before `SIGKILL`.

## phpspy compatibility

gospy reads phpspy's command line with a copy of phpspy's own option table. The table was
written against **phpspy 0.7.0**, the latest phpspy release, and the container example below runs
that version. It also knows `-q`/`--quiet` from phpspy's unreleased `master`. phpspy 0.7.0
supports PHP 7.0 to 8.4.

phpspy's unreleased `master` drops the short forms `-j`, `-J`, `-x`, `-a` and `-w`. On a phpspy
built from `master`, write `--event-handler-opts=m` in place of `-J m`. gospy understands both
forms.

**The binary must be called `phpspy`.** gospy picks the profiler by the base name of the command
that follows its flags: `phpspy` and `/usr/local/bin/phpspy` both work, while a wrapper script or a
renamed binary is rejected as `unsupported profiler`.

**Rules gospy enforces on phpspy's flags:**

| phpspy flags | Rule |
| --- | --- |
| `-o`, `--output` | Output must go to stdout: omit the flag, or pass `-o -`. |
| `-j`, `--event-handler` | Must be `fout` (the default). `callgrind` is rejected. |
| `-v`/`--version`, `-h`/`--help`, `-t`/`--top`, `-1`/`--single-line` | Rejected. They make phpspy print something other than trace blocks. |
| `-s`, `--sleep-ns` | Must not exceed `1000000000` (1s). Pyroscope needs a sample rate of at least 1 Hz. |
| `-P` with `-b` above 4096 | Needs `-J m` (see below). gospy logs a warning at startup if it is missing. |

Flags gospy does not mention above are passed through untouched. Useful ones are `-P`/`-p` to
choose the target, `-H` for the rate, `-T` for threads in pgrep mode, `-c` to keep going on
errors, `-g`/`-r`/`-e` for [tag sources](#where-the-meta-lines-come-from), `-f`/`-F` to filter
stacks, and `-n` for maximum depth.

### Interlaced writes

In pgrep mode (`-P`) several phpspy threads write to the same stdout. phpspy's default buffer is
4096 bytes, the size of `PIPE_BUF`, so each write stays atomic. Deep stacks or large peeked values
need a bigger buffer (`-b 65536`), and above `PIPE_BUF` the kernel no longer keeps one thread's
write in one piece: stacks from different workers mix and gospy parses nonsense. `-J m` makes
phpspy serialise its writes with a mutex. Always pair `-b` above 4096 with `-J m` in pgrep mode.

### Runtime dependencies

phpspy shells out to `pgrep` (procps), `objdump` and `strings` (binutils), `awk` and `grep` when
it attaches, so those must be installed next to it.

### Installing phpspy

phpspy publishes source only, and the build needs its `termbox2` submodule. Clone the tag with
submodules rather than downloading the source archive:

```bash
git clone --depth 1 --branch v0.7.0 --recurse-submodules --shallow-submodules \
  https://github.com/adsr/phpspy.git
make -C phpspy
sudo install -m 0755 phpspy/phpspy /usr/local/bin/phpspy
```

The build needs a C compiler, `make` and `git` (`build-essential git` on Debian).

## Running in a container

gospy, phpspy and php-fpm share one container. The example below is an official `php:8.3-fpm`
image with phpspy 0.7.0 built from source and gospy copied from its image.

`Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1
ARG PHP_VERSION=8.3
ARG GOSPY_VERSION=0.12.0

# gospy: a static binary published as an image to copy from.
FROM ghcr.io/hakastein/gospy:${GOSPY_VERSION} AS gospy

# phpspy: built from the upstream tag, submodules included, on the same base as the runtime.
FROM php:${PHP_VERSION}-fpm AS phpspy
ARG PHPSPY_VERSION=v0.7.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential ca-certificates git \
    && git clone --depth 1 --branch "${PHPSPY_VERSION}" --recurse-submodules --shallow-submodules \
        https://github.com/adsr/phpspy.git /usr/src/phpspy \
    && make -C /usr/src/phpspy

FROM php:${PHP_VERSION}-fpm
# phpspy shells out to pgrep (procps) and to objdump and strings (binutils) when it attaches.
RUN apt-get update \
    && apt-get install -y --no-install-recommends binutils procps \
    && rm -rf /var/lib/apt/lists/*
COPY --from=phpspy /usr/src/phpspy/phpspy /usr/local/bin/phpspy
COPY --from=gospy /gospy /usr/local/bin/gospy
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["entrypoint.sh"]
```

To take gospy from the release archive instead of the image, replace the `gospy` stage with this
one. The checksum is verified before the binary is used:

```dockerfile
FROM debian:bookworm-slim AS gospy
ARG GOSPY_VERSION
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl
WORKDIR /tmp/gospy
RUN curl -fsSLO "https://github.com/hakastein/gospy/releases/download/v${GOSPY_VERSION}/gospy_${GOSPY_VERSION}_linux_${TARGETARCH}.tar.gz" \
    && curl -fsSLO "https://github.com/hakastein/gospy/releases/download/v${GOSPY_VERSION}/checksums.txt" \
    && sha256sum -c --ignore-missing checksums.txt \
    && tar -xzf "gospy_${GOSPY_VERSION}_linux_${TARGETARCH}.tar.gz" gospy \
    && mv gospy /gospy
```

`entrypoint.sh` (make it executable, `chmod +x entrypoint.sh`):

```bash
#!/usr/bin/env bash
# Runs php-fpm and gospy side by side. The container lives as long as php-fpm:
# if gospy stops (bad configuration, restart budget spent) the site keeps serving.
set -uo pipefail
: "${PYROSCOPE_URL:?}" "${GOSPY_APP:?}"

php-fpm &
fpm_pid=$!

gospy \
    --pyroscope="${PYROSCOPE_URL}" \
    --app="${GOSPY_APP}" \
    --restart=always \
    --tag="env=${APP_ENV:-production}" \
    --tag="host=${HOSTNAME}" \
    --tag='uri={{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}' \
    phpspy -P '-x php-fpm' -H 99 -T 8 -b 65536 -J m -c -g server.REQUEST_URI &
gospy_pid=$!

stopping=0
stop() {
    # One SIGTERM lets gospy drain its buffered batches; a second one would abort the drain.
    ((stopping)) && return
    stopping=1
    kill -TERM "$gospy_pid" 2>/dev/null
    # SIGQUIT is php-fpm's graceful stop, and the stop signal the official image declares.
    kill -QUIT "$fpm_pid" 2>/dev/null
}
trap stop TERM INT QUIT

wait "$fpm_pid"
stop
wait "$gospy_pid"
wait "$fpm_pid"
```

gospy reads the token from `GOSPY_PYROSCOPE_AUTH`, which the script inherits from the container
environment, so the token never appears in a process list. `-P '-x php-fpm'` makes phpspy run
`pgrep -x php-fpm`, which matches the php-fpm master and workers by exact process name.

`compose.yaml`:

```yaml
services:
  app:
    build: .
    cap_add:
      - SYS_PTRACE
    environment:
      PYROSCOPE_URL: https://pyroscope.example.com
      GOSPY_APP: my-app
      APP_ENV: production
      # Taken from the shell or an .env file next to this file, never written here.
      GOSPY_PYROSCOPE_AUTH: ${GOSPY_PYROSCOPE_AUTH:?set GOSPY_PYROSCOPE_AUTH}
    stop_grace_period: 30s
```

```bash
GOSPY_PYROSCOPE_AUTH='<ingest token>' docker compose up -d --build
```

`cap_add: [SYS_PTRACE]` is what lets phpspy attach. Docker's default seccomp profile allows
`ptrace` once the capability is granted, so no `seccomp:unconfined` is needed. Without the
capability every read of a worker fails with `Operation not permitted`, which gospy logs at `warn`
as `profiler stderr`. `stop_grace_period` leaves
room for gospy's `--drain-timeout` before Docker sends `SIGKILL`.

On Kubernetes the equivalent is `securityContext.capabilities.add: ["SYS_PTRACE"]` on the
container that runs php-fpm and gospy, and `GOSPY_PYROSCOPE_AUTH` from a Secret.

## Operations

### Exit codes

| Exit code | When |
| --- | --- |
| `0` | gospy was stopped by `SIGTERM`/`SIGINT` and the drain finished or timed out, or phpspy exited cleanly and the restart policy did not restart it. |
| `1` | A configuration error at startup, phpspy could not be started, the restart budget ran out (10 failures in a row), a second signal aborted the drain, or phpspy's stdout could not be read and the restart policy did not restart it. |
| phpspy's code | phpspy exited non-zero and the restart policy did not restart it (`--restart=no` or `onsuccess`). gospy exits with phpspy's exit code, or `255` if phpspy was killed by a signal. |

### Logging

gospy logs JSON lines to stderr through [zerolog](https://github.com/rs/zerolog). `time` is a unix
timestamp in seconds, durations such as `delay` are in milliseconds, and `instance` carries
`--instance-name`. The default level is `info`, `-v` adds `debug` and `-vv` adds `trace` (one line
per parsed stack, which is useful only for debugging). The token never appears in logs: the
startup line prints it as `***`.

```json
{"level":"info","instance":"gospy","pyroscope_url":"https://pyroscope.example.com","pyroscope_auth":"***","app_name":"my-app","tag_entrypoint":false,"keep_entrypoint_name":true,"restart":"always","rate_mb":4,"rate_burst_mb":6,"version":"v0.12.0","tags":["env=production"],"time":1790324831,"message":"gospy started"}
```

A message printed when phpspy's own exit code is propagated (`exit status 3`) is plain text, not
JSON.

### Statistics

Every `--stats-interval` gospy logs one `info` line that covers the interval. The line is skipped
when nothing was sent or dropped in that interval. For example:

```json
{"level":"info","instance":"gospy","total_requests":12,"total_bytes":48213,"success_requests":11,"retried_attempts":3,"failed_requests":1,"dropped_samples":0,"errors":{"http code: 503, response isn't json: upstream unavailable":1},"time":1790324614,"message":"pyroscope sending statistics"}
```

| Field | Meaning |
| --- | --- |
| `total_requests` | Batches whose delivery finished in the interval, delivered or not. |
| `total_bytes` | Body bytes of those batches. |
| `success_requests` | Batches Pyroscope accepted. |
| `retried_attempts` | Extra attempts spent on retries. |
| `failed_requests` | Batches dropped after their last attempt. |
| `dropped_samples` | Samples lost before they were sent: over the memory caps, or still queued when a drain was cut short. |
| `errors` | Final error message of each failed batch, with a count. |

A final line is flushed on a clean shutdown, drain timeout included, but not when a second signal
aborts the drain.

## Security

- **Privileges**: phpspy needs `ptrace` on php-fpm (see [Privileges](#privileges)). Grant
  `CAP_SYS_PTRACE` rather than full root where you can, and keep the container free of untrusted
  input.
- **Token**: pass it through `GOSPY_PYROSCOPE_AUTH`, not `--pyroscope-auth`. Anything that can read
  `/proc/<pid>/cmdline` in the PID namespace sees a command-line token. Use an ingest-only token
  and an `https://` URL. gospy warns when a token goes over plain `http://`, masks it in logs, and
  does not follow redirects, so the token only reaches the host you configured.
- **Delivery**: at most once past the retries. A batch that Pyroscope does not accept within 5
  attempts is dropped, never spooled to disk or sent later (see
  [Delivery to Pyroscope](#delivery-to-pyroscope)).

gospy opens no listening socket and writes nothing to disk. The full privilege model, what gospy
does on the host, and how to report a vulnerability privately are in [SECURITY.md](SECURITY.md).

## Performance

There is no current measurement of gospy's overhead. Earlier figures in this README were undated
and did not name the hardware or versions, so they were removed. phpspy's own cost depends on its
rate (`-H`), its thread count (`-T`) and the number of workers it traces. If you measure gospy,
please share the numbers in an issue together with the versions and the method you used.

## Contributing

Bug reports, questions and pull requests are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for
setup, checks and conventions, and the [code of conduct](CODE_OF_CONDUCT.md).

Changelog: see [Releases](https://github.com/hakastein/gospy/releases).

gospy is released under the [MIT License](LICENSE).
