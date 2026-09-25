# Own process discovery, one phpspy per PID

gospy used to wrap a single phpspy in pgrep mode (`-P`) and supervise it with a restart policy.
We replaced that with process discovery in gospy itself: a configuration file declares targets,
gospy scans `/proc`, plans which processes hold each target's slots, and runs one `phpspy -p
<pid>` per attach. The `supervisor` package, the passthrough command line and `--restart` went
with it.

## Why pgrep mode lost

The decision follows from how phpspy behaves, verified in its source (0.7.0 and master):

- **Threads pin their process.** In `-P` mode a worker thread keeps a PID until that process
  dies. On a php-fpm pool with hundreds of workers and `-T 5`, the same five workers are profiled
  forever. The only way to see others was to kill phpspy on a timer (`--time-limit-ms`), which
  lost in-flight traces, re-ran `objdump` on every restart and left a gap every minute.
- **`-l` and `-i` are global,** so rate, thread count and lifetime cannot differ per process
  class, and each class needed a gospy of its own with duplicated flags and competing upload
  limits.
- **pgrep runs every two seconds** with the `-P` value pasted into `sh -c`, discovered PIDs sit in
  a LIFO of size `-T`, and a PID whose attach failed is retried with a full `objdump` every scan
  forever.
- **One PHP version per run:** the first version detected applies to every later PID.
- **`-p` does what we need.** It takes exactly one PID, exits 0 when the process dies, exits 1 on
  a setup failure, has no signal handlers (SIGTERM ends it at once), and writes one trace block
  per `write()` from a single thread, so interlacing is not a concern.

## Considered options

- **Keep pgrep mode and rotate by restarting phpspy** — rejected: that is the production
  workaround this replaces, with its lost traces and its gap at every restart.
- **Feed pgrep mode a curated PID list** (`-P` with a script that prints the PIDs gospy chose)
  — rejected: phpspy still pins threads, still applies one rate and one lifetime to all, and
  still retries failed attaches forever; gospy would own discovery without owning the attach.
- **Watch fork/exec events (netlink proc connector) instead of polling** — deferred: polling
  `/proc` at one scan per second costs a few milliseconds on a box with a thousand processes,
  and it needs no extra capability.
- **Keep the passthrough mode behind a flag** — rejected: two runtimes to reason about, and the
  option table would have to stay complete for a mode nobody should run.

## Consequences

- Every attach re-runs phpspy's `objdump` passes on the PHP binary, roughly 100 to 300 ms of
  CPU. Rotation below 10 seconds spends more time attaching than sampling; the default is 60.
- The planner is a pure state machine with an injected clock and random source, so the slot
  policy is tested without sleeping; the scanner reads a procfs root, so it is tested against a
  fixture tree.
- The runtime accepts a process source alongside the transport as its only test seams
  (ADR-0001 stands: one ingest front door, transport injection).
- The command line shrank to `--config`, verbosity, `--version` and `--help`; every other
  setting is a file key, with `${VAR}` expansion so per-host values still come from the
  environment.
