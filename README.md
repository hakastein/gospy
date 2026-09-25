# gospy

[![Go Report Card](https://goreportcard.com/badge/github.com/hakastein/gospy)](https://goreportcard.com/report/github.com/hakastein/gospy)
[![Pull Request Checks](https://github.com/hakastein/gospy/actions/workflows/pull-request.yml/badge.svg)](https://github.com/hakastein/gospy/actions/workflows/pull-request.yml)

![gospy.jpg](gospy.jpg)

**gospy** profiles PHP in production with [phpspy](https://github.com/adsr/phpspy) and ships
what it sees to [Pyroscope](https://pyroscope.io/). It reads the process table itself, sorts the
PHP processes it finds into **targets** (php-fpm workers, queue listeners, cron jobs, CLI runs),
runs one `phpspy -p <pid>` per process it attaches to, and rotates those attaches so that a
whole pool gets profiled over time rather than the same handful of workers forever. Every target
has its own sample rate, slot count and tags, and every sample flows through one collector into
one Pyroscope app, sliced by tags such as `source=fpm` or `uri=/orders/42`.

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
- [Configuration file](#configuration-file)
- [Targets](#targets)
- [Tags](#tags)
- [Entry points](#entry-points)
- [Slot policy](#slot-policy)
- [Failed attaches](#failed-attaches)
- [Delivery to Pyroscope](#delivery-to-pyroscope)
- [phpspy compatibility](#phpspy-compatibility)
- [Running in a container](#running-in-a-container)
- [Operations](#operations)
- [Migrating from 0.12](#migrating-from-012)
- [Security](#security)
- [Performance](#performance)
- [Contributing](#contributing)

## Installation

This README describes the configuration file on `main`, first released as v0.13.0; the examples
use `0.13.0`. v0.12.0 and older wrap a single phpspy in pgrep mode behind a command line of
flags — read the README at that tag if you run one of them, and
[Migrating from 0.12](#migrating-from-012) for what changed.

gospy is useful only on Linux, because phpspy is Linux-only and gospy reads `/proc`. The darwin
archives exist so the binary can be inspected and tested on a Mac.

### Release archive

Every [release](https://github.com/hakastein/gospy/releases) publishes
`gospy_<version>_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64, plus a
`checksums.txt` that covers every file. `<version>` is the tag without its leading `v`.

```bash
VERSION=0.13.0
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

Pin a release with `@v0.13.0` in place of `@latest`. A binary installed this way reports the
module version it was built from in `gospy --version` (for example `v0.13.0`), not `dev`.

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
COPY --from=ghcr.io/hakastein/gospy:0.13.0 /gospy /usr/local/bin/gospy
```

A complete image is shown in [Running in a container](#running-in-a-container).

## Quick start

Install phpspy (see [Installing phpspy](#installing-phpspy)). Write `/etc/gospy/gospy.yaml`:

```yaml
pyroscope:
  url: https://pyroscope.example.com
app: my-app
tags:
  env: production

targets:
  - name: fpm
    match: { comm: php-fpm, cmdline: '^php-fpm: pool ' }
    max-processes: 5
    rate: 25
    rotate: 30s
    tags:
      source: fpm
      uri: '{{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'
    phpspy-args: [--peek-global=server.REQUEST_URI, -c]

  - name: cli
    match: { comm: php }
    max-processes: 1
    rate: 10
    tags: { source: cli }
    tag-entrypoint: true
```

Then, as root or with `CAP_SYS_PTRACE`, on the host or in the container where php-fpm runs:

```bash
export GOSPY_PYROSCOPE_AUTH='<ingest token>'
gospy
```

Here is what happens:

- gospy reads the process table every second. Processes named `php-fpm` whose command line
  starts with `php-fpm: pool ` belong to the `fpm` target; the master process (`php-fpm: master
  process`) does not match and is left alone. Anything else named `php` belongs to `cli`.
- `fpm` holds up to five workers at a time, sampled at 25 Hz, and every 30 seconds hands a slot
  to a worker that has waited longest, so the whole pool is seen over time. `cli` follows one
  `php` process at a time at 10 Hz.
- Every sample carries the global tag `env=production` and the target's `source` tag. `fpm`
  samples also carry `uri`, read from `$_SERVER['REQUEST_URI']` with the query string cut off;
  `cli` samples carry `entrypoint=<script path>` instead.
- Batches go to `https://pyroscope.example.com/ingest` every five seconds as
  `my-app{env=production,source=fpm,uri=/orders/42}` and the like, each with the rate of the
  target that produced it.

Stop gospy with `SIGTERM` or `Ctrl-C`. It ends every phpspy, sends the batches it still holds,
and exits 0.

## Command line

```text
gospy [--config <path>] [-v | -vv]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--config` | `/etc/gospy/gospy.yaml` | Path of the configuration file. Environment: `GOSPY_CONFIG`. |
| `-v`, `--verbose` | off | Once for `debug` logs, twice (`-vv`) for `trace`. |
| `-h`, `--help` | | Print help and exit. |
| `-V`, `--version` | | Print `gospy version <version>` and exit. |

Everything else is a file setting. A positional argument is an error that names `--config`, and
so is any other flag: there is no passthrough to phpspy any more, phpspy's arguments live in each
target's `phpspy-args`.

gospy exits with an error before it starts anything if the file cannot be read, does not parse,
references an unset environment variable without a default, holds a key it does not know, a bad
matcher regex, a tag that does not parse, or a phpspy flag gospy manages itself. A missing
`phpspy` binary is caught at startup too.

## Configuration file

The file is YAML. Every key is shown here with its default; only `pyroscope.url`, `app` and
`targets` are required.

```yaml
pyroscope:
  url: https://pyroscope.example.com   # required; posts go to <url>/ingest, a query string is kept
  timeout: 10s                         # per request, and per retry of it; above 0
  workers: 5                           # requests sent concurrently; 1 to 1000
  rate-mb: 4                           # upload limit in MB/s (1 MB = 1,048,576 bytes); 0 is unlimited
  rate-burst-mb: 6                     # upload burst in MB; must be above 0 unless rate-mb is 0
app: my-app                            # required; the application name in Pyroscope
tags: {}                               # global static tags, key: value; see Tags
phpspy: phpspy                         # the binary; resolved on PATH when it has no slash
batch-interval: 5s                     # window over which samples are collected before a batch is sent; above 0
stats-interval: 10s                    # interval of the statistics lines; 0 disables them
drain-timeout: 10s                     # how long shutdown keeps sending buffered batches; above 0
instance-name: gospy                   # value of the `instance` field on every log line
targets: []                            # required; at least one target, see Targets
```

The Pyroscope token is **not** a file setting. It is read from `GOSPY_PYROSCOPE_AUTH` and sent as
`Authorization: Bearer <token>`; a `pyroscope.auth` key is rejected, so the file can be baked into
an image without a secret in it. gospy warns at startup when a token is set on a plain `http://`
URL.

Durations use Go syntax (`500ms`, `10s`, `1m`). A bare number such as `10` is rejected; `0` is
accepted where the key documents it. Unknown keys, anywhere in the file, are a startup error, and
so is a key given twice.

### Environment variables in the file

Every value may reference the environment, so per-host settings keep coming from the container:

| Syntax | Meaning |
| --- | --- |
| `${VAR}` | The value of `VAR`. A startup error naming `VAR` if it is not set. |
| `${VAR:-default}` | The value of `VAR`, or `default` when `VAR` is unset or empty. |
| `$$` | A literal `$`. |

A `$` followed by anything else is kept as written. References are expanded before the value is
typed, so `max-processes: ${GOSPY_THREADS_FPM:-5}` is the integer 5. Inside a flow mapping
(`{ key: value }`) quote the reference, because YAML reads `{` and `}` there as syntax:
`{ max-processes: "${GOSPY_THREADS_FPM:-5}" }`. Keys are never expanded, and neither is a
dynamic tag (a `tags` value that opens with `{{`): its `$1` and `${name}` belong to the regexp
engine, not to the environment.

## Targets

A target is a named group of processes with its own slots, rate, tags and phpspy arguments.
gospy scans the process table on the shortest `scan-interval` of the enabled targets, sorts every
process into the **first target in file order** whose matchers all hold, and each target then
plans on its own `scan-interval`. gospy itself and every process under it (phpspy, the `sh`,
`awk`, `grep` and `objdump` phpspy runs) are never candidates, whatever the matchers say — so
gospy must not be the ancestor of the PHP processes it profiles; run it next to php-fpm, not as
its parent.

```yaml
targets:
  - name: fpm                          # required, unique; letters, digits, '_', '-', '.'
    match:                             # required, at least one matcher; all must hold
      comm: php-fpm                    # exact match against /proc/<pid>/comm, 15 characters at most
      cmdline: '^php-fpm: pool '       # RE2 regex searched in the command line
      exe: /usr/local/sbin/php-fpm     # exact path of the executable, as it was started
      uid: 33                          # real user id
      exclude: 'pool admin'            # RE2 regex; a match on the command line rejects the process
    max-processes: 5                   # required; the slot count, up to 10000. 0 disables the target
    rate: 99                           # sample rate in Hz, at least 1
    rotate: 60s                        # detach an attach this old when a process is waiting; 0 never rotates
    scan-interval: 1s                  # at least 100ms
    tags: {}                           # static and dynamic tags of this target, over the global ones
    entrypoints: []                    # keep only samples whose entry script matches; see Entry points
    tag-entrypoint: false              # add entrypoint=<script path> to every sample
    keep-entrypoint-name: true         # keep the script path in the root frame
    phpspy-args: []                    # extra phpspy flags, verbatim; see phpspy compatibility
```

The command line a matcher sees is `/proc/<pid>/cmdline` with its NUL separators replaced by
spaces and the padding a process leaves after rewriting its title removed, so a php-fpm worker
reads `php-fpm: pool www` and a queue listener `php console.php queue:listen --memory=512`.
`comm` is the kernel's 15-character process name, which is what `pgrep -x` matched before; a
longer `comm` matcher could never match and is rejected. `exe` is the path the process was
started from, even after the binary on disk was replaced (the kernel's ` (deleted)` mark is
dropped). An empty or null matcher value is an error.

A target with `max-processes: 0` is disabled: gospy logs that at startup and starts nothing for
it, so `GOSPY_THREADS_FPM=0` still means "no fpm profiling on this host". Its matchers still
claim the processes they select, so those do not fall through to a broader target below it:
switching a target off never moves its processes into another series. Remove the target to let
them through. Disabling every target is allowed; gospy then runs, logs a warning, and profiles
nothing.

A process that leaves a target's matched set while attached, because it exited or because its
command line now belongs to another target, is detached on the next scan, so no process is
traced by two targets at once.

### Recipes

The four classes a typical PHP box has. Order matters: `php` processes that are neither
listeners nor crons fall through to `cli`.

```yaml
targets:
  # php-fpm workers only: the master never executes PHP, its title is "php-fpm: master process".
  - name: fpm
    match: { comm: php-fpm, cmdline: '^php-fpm: pool ' }
    max-processes: ${GOSPY_THREADS_FPM:-5}
    rate: ${GOSPY_RATE_FPM:-10}
    rotate: 30s
    tags:
      source: fpm
      uri: '{{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'
    entrypoints: [index.php]
    phpspy-args: [--max-depth=-1, --peek-global=server.REQUEST_URI, -c]

  # Long-running queue listeners: few slots, a long rotation, tagged by their script.
  - name: queue
    match: { comm: php, cmdline: 'console\.php queue:listen' }
    max-processes: 3
    rate: 10
    rotate: 60s
    tags: { source: queue }
    tag-entrypoint: true

  # Cron jobs are short: a fast scan so they are seen at all, and a slot pre-empted for them.
  - name: cron
    match: { comm: php, cmdline: '/cronjobs/' }
    max-processes: 2
    rate: 10
    scan-interval: 500ms
    tags: { source: cron }
    tag-entrypoint: true

  # Everything else that is php: a trickle.
  - name: cli
    match: { comm: php }
    max-processes: 1
    rate: 10
    tags: { source: cli }
    tag-entrypoint: true
```

Two enabled targets whose static tag sets (global plus their own) are identical would land in
one Pyroscope series; gospy warns about that at startup, naming both.

## Tags

Every batch is sent to Pyroscope as `<app>{<static tags>,<dynamic tags>}`, so each distinct tag
set becomes its own series. The tags of a sample are the global `tags` with the target's `tags`
laid over them (a target key wins over a global one), then the dynamic tags of the request, then
the entry point when `tag-entrypoint` is on.

### Static tags

```yaml
tags:
  env: production
  host: ${HOSTNAME}
```

A key may contain `A-Z a-z 0-9 _ .`. A static value must not contain `{`, `}`, `=`, `,`, `"`,
whitespace or control characters. gospy refuses to start on such a value rather than send a broken
name. A value is always text, however YAML would have typed it: `version: 1.0` is the tag
`version=1.0`.

### Dynamic tags

A dynamic tag takes its value from each sample's meta lines. phpspy prints them under the stack as
`# <meta key> = <value>`, and the dynamic tag names the meta key in double quotes:

```yaml
tags:
  uri: '{{ "glopeek server.REQUEST_URI" }}'
```

The two accepted forms, exactly:

- `{{ "meta key" }}`: the value as phpspy printed it.
- `{{ "meta key" "regex" "replacement" }}`: the value rewritten with Go's
  [`regexp.ReplaceAllString`](https://pkg.go.dev/regexp#Regexp.ReplaceAllString), so
  `$1` and `${name}` refer to capture groups. The replacement may be empty (`""`), which deletes
  whatever the regex matches.

Blanks between the braces and the quoted strings are optional. Anything else inside the braces
(two quoted strings, four or more, or text outside the quotes) is an error. A value that opens with
`{{` is always read as a dynamic tag, so an unterminated `{{`, a missing `}}` or a malformed body
stops gospy at startup instead of turning into a static tag.

Inside a quoted string a backslash escapes the next character, so `\"` is a literal quote. To pass
a backslash to the regex, double it: `\\?` reaches the regex as `\?`, a literal question mark.
Single-quote the whole value in YAML so that YAML passes backslashes unchanged; `$1` needs no
escaping because gospy only expands `${…}`.

Examples:

```yaml
tags:
  # URI without the query string: /orders/42?utm_source=mail -> /orders/42
  uri: '{{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'

  # The same with an empty replacement
  uri: '{{ "glopeek server.REQUEST_URI" "\\?.*$" "" }}'

  # Numeric ids collapsed into a route: /orders/42 -> /orders/:id
  route: '{{ "glopeek server.REQUEST_URI" "^/orders/[0-9]+(\\?.*)?$" "/orders/:id" }}'

  # Request method
  method: '{{ "glopeek server.REQUEST_METHOD" }}'
```

A dynamic value is sanitised after the regex runs: `,` becomes `͵` (U+0375, which looks like a
comma), and `{`, `}`, `=`, `"`, whitespace and control characters become `_`. A value can therefore
never break the series name. A sample that has no matching meta line simply lacks that tag. Several
tags may read the same meta key.

Dynamic tags are per target, so only the target that reads `$_SERVER` pays for `--peek-global`:
put the `uri` tag and its `--peek-global` on `fpm` and leave the CLI targets with `tag-entrypoint`.

### Where the meta lines come from

The meta key is whatever phpspy prints between `# ` and ` = `. These `phpspy-args` produce the
meta lines a dynamic tag can use:

| phpspy flag | Meta key | Example tag |
| --- | --- | --- |
| `--peek-global=server.REQUEST_URI` (`-g`) | `glopeek server.REQUEST_URI` | `uri: '{{ "glopeek server.REQUEST_URI" }}'` |
| `--peek-global=server.HTTP_HOST` | `glopeek server.HTTP_HOST` | `vhost: '{{ "glopeek server.HTTP_HOST" }}'` |
| `--request-info=u` / `=p` / `=q` (`-r`) | `uri`, `path`, `qstring` | `script: '{{ "path" }}'` |
| `--peek-var=var@file:line` (`-e`) | `varpeek var@file:line` | `tenant: '{{ "varpeek tenant@/app/src/Kernel.php:42" }}'` |

`--peek-global` reads the PHP superglobal, and PHP fills `$_SERVER` lazily (`auto_globals_jit`):
a request whose script has not touched `$_SERVER` yet has no `glopeek` line, and its samples carry
no `uri` tag. Most frameworks read `$_SERVER` during bootstrap, so this mainly affects samples
taken before that point.

A dynamic URI tag creates one series per distinct value. Collapse ids with a regex (see `route`
above) rather than tagging raw URIs on an application with unbounded URLs. gospy also caps what it
holds (see [Delivery to Pyroscope](#delivery-to-pyroscope)).

## Entry points

The entry point is the script that started the request, taken from the outermost frame of the
stack (`/var/www/public/index.php`). By default every sample is kept.

```yaml
entrypoints: [index.php, '/var/www/bin/**/*.php']
```

A pattern without `*`, `?` or `[` matches a path that is equal to it or ends in `/<pattern>`, so
`index.php` matches `/var/www/public/index.php`. A pattern with one of those characters is a
[doublestar](https://github.com/bmatcuk/doublestar) glob matched against the full path. Samples
whose entry point matches no pattern are discarded before they are counted anywhere.

`tag-entrypoint` adds the entry point as the `entrypoint` tag, and `keep-entrypoint-name`
decides whether it also stays in the root frame of the stack (`<main> /var/www/public/index.php`
against a bare `<main>`, which merges the same code reached through different scripts). Both are
per target: the `fpm` target keeps `index.php` only, the `cron` target tags by script.

## Slot policy

A target's `max-processes` is its number of **slots**: attaches it holds at once. On every
`scan-interval`, per target:

1. The candidates are the matched processes that hold no slot and sit in no failure hold (see
   [Failed attaches](#failed-attaches)). A process is identified by its PID **and** start time, so
   a reused PID is a new process with no history.
2. Free slots are handed out to candidates ordered by: never traced first, then the oldest
   last-traced time first, ties broken at random.
3. If no slot is free and a candidate has never been traced, the oldest attach that is at least
   **5 seconds** old is pre-empted for it. At most one pre-emption per target per scan.
4. Otherwise, if `rotate` is set, a candidate is waiting, and the oldest attach is older than
   `rotate`, that attach is detached. At most one rotation per target per scan, and a target with
   nobody waiting never rotates: a target with fewer processes than slots is not re-attached (and
   `objdump` not re-run) for nothing.
5. A detach is `SIGTERM` to the phpspy child, which has no handler and dies at once; what it
   already wrote is still parsed, so no sample is lost at rotation and there is no gap. The slot
   frees when the child is gone, and the next scan fills it.

The 5-second floor and the one-detach-per-scan limits are constants, not configuration: a cron
job that appears while every slot is busy with listeners gets one within a scan, while a php-fpm
reload does not turn into an attach storm.

### Attach cost

Every attach re-runs phpspy's setup: it shells out to `awk`, `grep`, `objdump -p` and two to
four `objdump -Tt` passes over the PHP binary. Budget roughly 100 to 300 ms of CPU per attach.
Keep `rotate` at **10 seconds or more**; below that a target spends more time in `objdump` than
in sampling. The default is 60 seconds; 30 seconds is a sane floor for a large php-fpm pool.

Scanning is cheap by comparison: one read of `/proc` for a thousand processes costs a few
milliseconds and runs once per the shortest `scan-interval`. A `uid` or `exe` matcher reads one
more file per process per scan; the other matchers need none.

## Failed attaches

An attach ends in one of three ways:

- **phpspy exits 0**: the process it traced ended. The slot is freed once a scan that started after
  the exit confirms the process is gone; nothing is held against it. A process that such a scan
  still lists did not end, so that exit counts as a failed attach instead of a
  reason to run phpspy on it again every scan.
- **phpspy exits non-zero**, or its output cannot be read: a failed attach. The process enters a
  hold of 30 seconds, then 60 seconds after a second consecutive failure. After three consecutive
  failures the process is not retried until it exits. Its PID may then be reused: a new start
  time is a new process.
- **Silent denial**: an attach that has produced no trace block for 15 seconds while phpspy kept
  reporting `copy_proc_mem` failures on stderr, at least a second's worth of samples of them
  since its last trace block, is killed and counted as a failed attach. That is phpspy on a
  process it may not trace (`ptrace_scope`, a missing capability): it never exits by itself, it
  prints a failure per sample. The same evidence counts when a rotation or a pre-emption ends
  the attach before the 15 seconds are up, so a denied host shows up as failed attaches and
  holds whatever the rotation period. An attach that is merely quiet, an idle worker with no
  errors or with a handful of transient read errors, is left alone.

phpspy's stderr is logged as `phpspy stderr` with the target name and the PID. The first line of
each kind per attach, compared without its addresses and sizes, goes to `warn`, so the reason for
a failed attach shows at the default verbosity and can be traced to the process that caused it;
repeats go to `debug`. Reads of memory the process was changing or had just released
(`Bad address`, `raddr is NULL`, `No such process`) are routine when sampling a live process: they
are logged at `debug` only and counted in `read_errors` of the [statistics](#statistics).

A failure in one target never stops another target or gospy: a quiet cron target or a
`ptrace_scope` problem shows up in the [statistics](#statistics), not as an exit. The one
exception is a phpspy that cannot be started at all: the binary is missing, cannot be executed
or has lost its interpreter. gospy checks the path at startup, checks again on the first attach
that fails to start, and exits non-zero either way, so a broken image fails fast under
supervisord instead of looking healthy. A start that fails for lack of resources (no free PID
under a pids limit, no memory, no file descriptors) is an ordinary failed attach: the process is
held and retried.

## Delivery to Pyroscope

**Batching.** Samples are grouped by tag set and sample rate and cut into a batch every
`batch-interval` (`5s`). A batch holds each distinct stack with its count and the time range it
covers, and is sent as one `folded`-format request whose `sampleRate` is the `rate` of the target
that produced it, so a 10 Hz CLI target and a 25 Hz fpm target both convert to correct CPU time.
`pyroscope.workers` requests run at once.

**Memory caps.** gospy holds at most 1,000 tag sets and 10,000 distinct stacks per tag set in the
current window, and at most 64 cut batches waiting for a worker. A tag set that reaches the stack
cap is cut early. Samples past these caps are dropped and counted as `dropped_samples`, so a
runaway dynamic tag or a Pyroscope outage costs samples, not memory.

**Rate limiting.** `rate-mb` limits the request bodies gospy uploads, and `rate-burst-mb` sets
how much may go at once. A batch larger than the burst is paced burst by burst, never dropped.
`rate-mb: 0` removes the limit, and the burst is then ignored. One limit covers every target.

**Retries.** A request is tried up to 5 times, waiting 500ms, then 1s, 2s and 4s between attempts
(never more than 30s). Network errors, timeouts, `429` and `5xx` are retried. A `Retry-After`
header, in seconds or as an HTTP date, replaces the computed wait, still capped at 30s. Other
`4xx` responses are final: the request is not repeated. A `3xx` is not followed either, so the
token never travels to a host you did not configure, and it counts as a final error.

**Failures are dropped.** When the last attempt fails, gospy logs an error and drops the batch. It
does not spool to disk. The statistics line counts it under `failed_requests` and in `errors`.

**Shutdown.** On the first `SIGTERM` or `SIGINT` gospy signals every phpspy child at once
(`SIGTERM` to each process group, `SIGKILL` after 2s), parses what they still had in their pipes,
cuts every open batch and keeps sending for up to `drain-timeout` (`10s`). When the timeout
passes, requests in flight are cancelled, the batches still queued are counted as dropped, and
gospy exits 0. A second `SIGTERM` or `SIGINT` during the drain abandons it at once, and gospy
exits 1.

**Delivery is at most once past the retries.** gospy holds a batch only while it retries it.
Nothing is spooled or sent again later, so a batch is lost for good when its retries run out, when
its samples exceed a memory cap, when the drain times out, or when gospy is killed. Losses show in
the statistics line and in the error logs. Within the retry window a batch can be stored twice: if
Pyroscope accepted an attempt but the response never arrived (a timeout, for example), the retry
sends it again. Size `drain-timeout` below your orchestrator's stop grace period (Docker's default
is 10s, Kubernetes' 30s) so that the drain can finish before `SIGKILL`.

## phpspy compatibility

gospy runs `<phpspy> -p <pid> -H <rate>` followed by the target's `phpspy-args` verbatim, once
per attach, and reads phpspy's stdout and stderr as one stream. It validates `phpspy-args` with a copy of phpspy's own
option table, written against **phpspy 0.7.0**, the latest phpspy release; the container example
below runs that version. The table also knows `-q`/`--quiet` from phpspy's unreleased `master`.
phpspy 0.7.0 supports PHP 7.0 to 8.4.

**Flags gospy manages are rejected** in short, long, abbreviated and clustered form, because
the rate and the target set have exactly one source of truth: `-p`/`--pid`, `-P`/`--pgrep`,
`-T`/`--threads`, `-H`/`--rate-hz`, `-s`/`--sleep-ns`, `-i`/`--time-limit-ms`, `-l`/`--limit`,
`-o`/`--output`, `-j`/`--event-handler`, `-t`/`--top`, `-1`/`--single-line`, `-v`/`--version`,
`-h`/`--help` and `-q`/`--quiet`, which would silence the errors gospy reads to detect a denied
attach. phpspy reads its options with `getopt_long`, so `--rate=50` means `--rate-hz` and is
rejected like it, an ambiguous abbreviation such as `--p` is an error, and a letter phpspy does
not know inside a cluster (`-Zt`) does not hide what follows it. A bare word is rejected too:
phpspy stops reading options at the first one and would ignore every flag after it, so the
value of a flag gospy does not know has to be written inline (`--flag=value`). Every error names
the target and the flag.

Everything else is passed through untouched. Useful flags are `--php-version` (`-V`) when
phpspy guesses wrong, `--max-depth` (`-n`) for deep stacks, `--continue-on-error` (`-c`) to keep
a trace whose peeked global is missing, `--peek-global` (`-g`), `--request-info` (`-r`) and
`--peek-var` (`-e`) for [tag sources](#where-the-meta-lines-come-from), and `--filter` (`-f`) and
`--filter-negate` (`-F`) to filter stacks. In `-p` mode one thread writes one trace block per
`write()`, so `-J m` is unnecessary.

gospy passes `--buffer-size=1048576` unless `phpspy-args` sets `--buffer-size` (`-b`): phpspy's
default of 4096 bytes cuts a real application's `--max-depth=-1` stacks short. A stack phpspy cut
short, on a full buffer or, under `-c`, on a failed read in the middle of the stack, has lost its
outer frames and meta lines; gospy drops it and counts it in `partial_traces` of the
[statistics](#statistics). A read that fails on the very first frame emits no block, so its
diagnostic marks the next, complete block partial: the count errs high, never a wrong sample.

phpspy's unreleased `master` drops the short forms `-j`, `-J`, `-x`, `-a` and `-w`; on a phpspy
built from `master`, write the long form. gospy understands both.

### Runtime dependencies

phpspy shells out to `objdump` and `strings` (binutils), `awk` and `grep` when it attaches, so
those must be installed next to it. `pgrep` is no longer needed.

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

gospy, phpspy and php-fpm share one container, with one gospy process for every PHP process
class in it. The example below is an official `php:8.3-fpm` image with phpspy 0.7.0 built from
source, gospy copied from its image, the configuration file baked in, and supervisord running
php-fpm and gospy side by side.

`Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1
ARG PHP_VERSION=8.3
ARG GOSPY_VERSION=0.13.0

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
# phpspy shells out to objdump and strings (binutils) when it attaches; supervisord runs the pair.
RUN apt-get update \
    && apt-get install -y --no-install-recommends binutils supervisor \
    && rm -rf /var/lib/apt/lists/*
COPY --from=phpspy /usr/src/phpspy/phpspy /usr/local/bin/phpspy
COPY --from=gospy /gospy /usr/local/bin/gospy
COPY gospy.yaml /etc/gospy/gospy.yaml
COPY supervisord.conf /etc/supervisor/conf.d/app.conf
CMD ["supervisord", "-n", "-c", "/etc/supervisor/supervisord.conf"]
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

`gospy.yaml`, with everything per host coming from the environment:

```yaml
pyroscope:
  url: ${PYROSCOPE_URL}
app: ${GOSPY_APP}
tags:
  env: ${APP_ENV:-production}
  host: ${HOSTNAME}

targets:
  - name: fpm
    match: { comm: php-fpm, cmdline: '^php-fpm: pool ' }
    max-processes: ${GOSPY_THREADS_FPM:-5}
    rate: ${GOSPY_RATE_FPM:-10}
    rotate: 30s
    tags:
      source: fpm
      uri: '{{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'
    entrypoints: [index.php]
    phpspy-args: [--max-depth=-1, --peek-global=server.REQUEST_URI, -c]

  - name: cli
    match: { comm: php }
    max-processes: ${GOSPY_THREADS_CLI:-1}
    rate: 10
    tags: { source: cli }
    tag-entrypoint: true
```

`supervisord.conf`, one program each. gospy is not the parent of php-fpm here, which is what
lets it profile the workers; `stopwaitsecs` leaves room for `drain-timeout`:

```ini
[program:php-fpm]
command=php-fpm
autorestart=true
stopsignal=QUIT
stopwaitsecs=30
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
stderr_logfile=/dev/stderr
stderr_logfile_maxbytes=0

[program:gospy]
command=gospy
autorestart=true
stopsignal=TERM
stopwaitsecs=15
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
stderr_logfile=/dev/stderr
stderr_logfile_maxbytes=0
```

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
      GOSPY_THREADS_FPM: 5
      # Taken from the shell or an .env file next to this file, never written here.
      GOSPY_PYROSCOPE_AUTH: ${GOSPY_PYROSCOPE_AUTH:?set GOSPY_PYROSCOPE_AUTH}
    stop_grace_period: 30s
```

```bash
GOSPY_PYROSCOPE_AUTH='<ingest token>' docker compose up -d --build
```

`cap_add: [SYS_PTRACE]` is what lets phpspy attach. Docker's default seccomp profile allows
`ptrace` once the capability is granted, so no `seccomp:unconfined` is needed. Without the
capability every read of a worker fails with `Operation not permitted`, which gospy logs at
`warn` as `phpspy stderr` with the target and PID, ends within 15 seconds, and counts under
`failed_attaches` and then `held`. `stop_grace_period` leaves room for supervisord's `stopwaitsecs` and gospy's
`drain-timeout` before Docker sends `SIGKILL`.

On Kubernetes the equivalent is `securityContext.capabilities.add: ["SYS_PTRACE"]` on the
container that runs php-fpm and gospy, and `GOSPY_PYROSCOPE_AUTH` from a Secret.

## Operations

### Exit codes

| Exit code | When |
| --- | --- |
| `0` | gospy was stopped by `SIGTERM`/`SIGINT` and the drain finished or timed out. |
| `1` | The configuration could not be read or is invalid, phpspy could not be started, or a second signal aborted the drain. |

A phpspy that exits, whatever its code, never ends gospy: it ends one attach.

### Logging

gospy logs JSON lines to stderr through [zerolog](https://github.com/rs/zerolog). `time` is a unix
timestamp in seconds, durations such as `rotate` are in milliseconds, and `instance` carries
`instance-name`. The default level is `info`, `-v` adds `debug` (one line per attach and detach,
which is useful when a target does not pick up what you expect) and `-vv` adds `trace` (one line
per parsed stack, which is useful only for debugging). The token never appears in logs: the
startup line prints it as `***`.

```json
{"level":"info","instance":"gospy","config":"/etc/gospy/gospy.yaml","time":1790324831,"message":"configuration loaded"}
{"level":"info","instance":"gospy","pyroscope_url":"https://pyroscope.example.com","pyroscope_auth":"***","app_name":"my-app","phpspy":"/usr/local/bin/phpspy","targets":2,"version":"v0.13.0","time":1790324831,"message":"gospy started"}
{"level":"info","instance":"gospy","target":"fpm","max_processes":5,"rate":10,"rotate":30000,"scan_interval":1000,"tags":["env=production","host=web-01","source=fpm","uri={{ \"glopeek server.REQUEST_URI\" \"^([^?]+)\\\\?.*$\" \"$1\" }}"],"phpspy_args":["--max-depth=-1","--peek-global=server.REQUEST_URI","-c"],"time":1790324831,"message":"target enabled"}
{"level":"warn","instance":"gospy","target":"cron","time":1790324831,"message":"target disabled: max-processes is 0, its processes are claimed but not profiled"}
```

### Statistics

Every `stats-interval` gospy logs one `info` line per enabled target and one for Pyroscope
delivery. The target line is always emitted, so a target that finds nothing is visible:

```json
{"level":"info","instance":"gospy","target":"fpm","matched":64,"attached":5,"rotations":2,"preemptions":0,"failed_attaches":0,"held":0,"partial_traces":0,"filtered_traces":12,"read_errors":31,"time":1790324841,"message":"target statistics"}
```

| Field | Meaning |
| --- | --- |
| `matched` | Processes the last scan sorted into this target. |
| `attached` | Attaches held right now, detaches in flight and exits waiting for a later scan included. |
| `rotations` | Attaches detached in the interval because they reached `rotate` while a process waited. |
| `preemptions` | Attaches detached in the interval to hand a slot to a process never traced before. |
| `failed_attaches` | Attaches that ended in a failure in the interval, an exit 0 on a process still running included. |
| `held` | Matched processes sitting in a failure hold, retired ones included. |
| `partial_traces` | Traces dropped in the interval because phpspy cut the stack short, see [phpspy compatibility](#phpspy-compatibility). |
| `filtered_traces` | Traces dropped in the interval because their entry point is not in `entrypoints`. |
| `read_errors` | phpspy's routine failed reads of a live process in the interval, logged at `debug` only. |

The Pyroscope line covers the interval and is skipped when nothing was sent or dropped in it:

```json
{"level":"info","instance":"gospy","total_requests":12,"total_bytes":48213,"success_requests":11,"retried_attempts":3,"failed_requests":1,"dropped_samples":0,"errors":{"http code: 503, response isn't json: upstream unavailable":1},"time":1790324841,"message":"pyroscope sending statistics"}
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

A final Pyroscope line is flushed on a clean shutdown, drain timeout included, but not when a
second signal aborts the drain.

## Migrating from 0.12

0.12 took a command line of flags followed by phpspy's own command line, and supervised one
phpspy in pgrep mode with `--restart`. 0.13 takes a configuration file and discovers processes
itself. Only `--config`, `-v`/`-vv`, `--version` and `--help` remain on the command line; a
removed flag is now an error.

| 0.12 flag | 0.13 file key |
| --- | --- |
| `--pyroscope` | `pyroscope.url` |
| `--pyroscope-auth` | removed; `GOSPY_PYROSCOPE_AUTH` is the only source |
| `--pyroscope-timeout`, `--pyroscope-workers` | `pyroscope.timeout`, `pyroscope.workers` |
| `--rate-mb`, `--rate-burst-mb` | `pyroscope.rate-mb`, `pyroscope.rate-burst-mb` |
| `--app` | `app` |
| `--tag=key=value` | `tags: { key: value }`, globally or per target |
| `--tag-entrypoint`, `--keep-entrypoint-name`, `--entrypoint` | `tag-entrypoint`, `keep-entrypoint-name`, `entrypoints` per target |
| `--instance-name`, `--batch-interval`, `--stats-interval`, `--drain-timeout` | `instance-name`, `batch-interval`, `stats-interval`, `drain-timeout` |
| `--restart` | removed; there is no long-lived phpspy to restart |
| `phpspy -P '-x php-fpm' -T 8` | a target with `match: { comm: php-fpm, cmdline: '^php-fpm: pool ' }` and `max-processes: 8` |
| `phpspy -H 99` | `rate: 99` per target |
| `phpspy -b 65536 -J m` | unnecessary in `-p` mode: gospy sets a 1 MiB buffer, and a `-b` in `phpspy-args` overrides it |
| `phpspy -c -g server.REQUEST_URI` | `phpspy-args: [-c, --peek-global=server.REQUEST_URI]` per target |
| `--time-limit-ms` and a restart to rotate workers | `rotate` per target |

Two gospy processes for two process classes become two targets in one file. The `uri` tag and
`--peek-global` move to the `fpm` target alone. The old `-P 'php-fpm'` matched the php-fpm
master too; the recipe above excludes it because it never executes PHP.

## Security

- **Privileges**: phpspy needs `ptrace` on php-fpm (see [Privileges](#privileges)). Grant
  `CAP_SYS_PTRACE` rather than full root where you can, and keep the container free of untrusted
  input.
- **Token**: `GOSPY_PYROSCOPE_AUTH` is the only place it is read from, so the file carries no
  secret and can be baked into an image. Use an ingest-only token and an `https://` URL. gospy
  warns when a token goes over plain `http://`, masks it in logs, and does not follow redirects,
  so the token only reaches the host you configured.
- **Delivery**: at most once past the retries. A batch that Pyroscope does not accept within 5
  attempts is dropped, never spooled to disk or sent later (see
  [Delivery to Pyroscope](#delivery-to-pyroscope)).

gospy opens no listening socket and writes nothing to disk. The full privilege model, what gospy
does on the host, and how to report a vulnerability privately are in [SECURITY.md](SECURITY.md).

## Performance

There is no current measurement of gospy's overhead. phpspy's cost depends on the rate and on the
number of attaches, and each attach starts with the `objdump` passes described under
[Attach cost](#attach-cost); a scan of `/proc` costs a few milliseconds per thousand processes.
If you measure gospy, please share the numbers in an issue together with the versions and the
method you used.

## Contributing

Bug reports, questions and pull requests are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for
setup, checks and conventions, and the [code of conduct](CODE_OF_CONDUCT.md).

Changelog: see [Releases](https://github.com/hakastein/gospy/releases).

gospy is released under the [MIT License](LICENSE).
