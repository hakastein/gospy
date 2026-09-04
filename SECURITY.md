# Security Policy

## Supported versions

Only the latest release is supported. Fixes are published as a new release rather than backported
to older tags — see the [Releases](https://github.com/hakastein/gospy/releases) page.

## Reporting a vulnerability

Report privately through GitHub, not in a public issue: open the repository's
**[Security](https://github.com/hakastein/gospy/security)** tab and choose **Report a
vulnerability**, or go straight to
[the advisory form](https://github.com/hakastein/gospy/security/advisories/new).

Please include the gospy version, how it was invoked (with any auth token redacted), the
environment it ran in, and what an attacker gains. If you have a reproduction, include it.

This is a small project maintained in spare time; expect an acknowledgement within a few days
rather than within hours. You will be credited in the advisory unless you ask not to be.

## Privilege model

gospy is a wrapper around a profiler that reads another process's memory, so it necessarily runs
with more privilege than an ordinary service. Understand this before deploying it:

- **phpspy attaches with `ptrace`.** To read stacks out of a running PHP process it must be able
  to `ptrace` that process, which in practice means gospy runs as **root** or with
  **`CAP_SYS_PTRACE`**, and the kernel's `kernel.yama.ptrace_scope` must be permissive enough to
  allow the attach (`0`, or a configuration that grants the tracer the right). Under Docker this
  usually means `--cap-add=SYS_PTRACE` and, depending on the host, a relaxed seccomp profile.
- **It usually shares a namespace with the target.** To see php-fpm's PIDs, gospy is typically run
  inside the same container as php-fpm or in the same PID namespace (`--pid=container:php-fpm`).
  Anything that can execute code in gospy's context is therefore adjacent to your PHP workers.
- **The Pyroscope token is passed on the command line.** `--pyroscope-auth` is a CLI flag, so the
  token is visible to anything that can read the process command line — `ps`, `/proc/<pid>/cmdline`,
  shell history, container inspect output, orchestrator manifests. Treat it as exposed to every
  local user who can see the process, and prefer a token scoped to ingest only. Reading the token
  from an environment variable is planned; it is not available yet.
- **gospy runs the profiler you tell it to.** It executes phpspy as a child process with the
  arguments you supply. Whoever controls those arguments controls a command line executed with
  gospy's privileges.

Grant the narrowest thing that works: prefer `CAP_SYS_PTRACE` on a dedicated non-root user over
running the whole thing as root, and do not expose the container gospy shares to untrusted input.

## What gospy does and does not do on the box

It does:

- spawn the configured profiler (phpspy) as a child process and read its stdout;
- make outbound HTTPS requests to the Pyroscope URL you configure, and to nothing else;
- write logs to stderr.

It does not:

- open any listening socket — there is no HTTP server, no admin port, no metrics endpoint;
- write profile data, spool files or caches to disk;
- read configuration from a file, a network location or any path other than the command line;
- talk to any host you have not configured.

Everything it collects is in memory: parsed samples, the folded stacks aggregated per tag set, and
a bounded cache of entry points. That data is stack traces of your application, plus whatever your
dynamic tags extract from phpspy's meta lines — treat it as production data and point gospy at a
Pyroscope instance you trust.

## Out of scope

- Vulnerabilities in [phpspy](https://github.com/adsr/phpspy) itself — report those upstream.
- Vulnerabilities in Pyroscope or in the Go toolchain and dependencies; if a dependency advisory
  affects gospy, a normal issue or pull request bumping it is the right channel.
- The fact that gospy needs `ptrace` privileges, or that a host with `ptrace_scope=0` lets
  privileged processes read other processes' memory. That is the design described above, not a
  flaw in gospy.
