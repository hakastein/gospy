# Single front door for Pyroscope ingest

The runner used to assemble the send path from five parts (client, metadata, limiter, pool, stats) and own their channel protocols and drain ordering. A design-it-twice pass compared a minimal blocking function, a functional-options protocol, and a caller-first handle; we chose the caller-first handle — `Config` / `StartIngest` / `In()` / `Wait()` — with everything else unexported.

## Considered Options

- **A consumer port between collector and ingest** (the former `TagData` interface) — rejected: exactly one adapter ever existed, so the seam was hypothetical. Ingest consumes `*collector.TagCollection` concretely; re-cut the port only when a second data source is real.
- **Functional options with a stats observer** — rejected as speculative: the only consumer of send statistics is the module's own log.
- **A `Sender` port for transport** — rejected: `http.RoundTripper` injected via `Config.Transport` already gives a real seam with two adapters (production transport, test capture).

## Consequences

- Send statistics are observable only through the logger passed in `Config`; that logger's level also gates collection (formerly a hidden `zerolog.GlobalLevel()` read in the runner).
- Rate limiting is trusted to `golang.org/x/time/rate`; tests assert delivery through the surface, not timing.
