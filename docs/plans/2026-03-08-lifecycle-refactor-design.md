# Lifecycle Refactor Design

**Date:** 2026-03-08

**Status:** Approved

## Goal

Refactor the current `gospy` runtime so the existing `phpspy -> parse -> aggregate -> pyroscope` flow has a single, explicit lifecycle owner, predictable shutdown behavior, and tighter package boundaries without adding speculative abstractions for future features.

## Current Problems

1. Runtime shutdown is not owned by one component, which causes the process to hang after a normal profiler exit.
2. The profiler type is resolved in two separate packages, which can produce inconsistent behavior for the same CLI input.
3. Parser output can block forever on a full channel, so cancellation does not reliably stop the pipeline.
4. The parser drops the final trace if the input ends without an empty separator line.
5. Statistics mix successful sends with error buckets.
6. `internal/pyroscope` tests depend on `httptest.NewServer`, which makes the package harder to test in constrained environments.

## Design Constraints

- Keep the existing product behavior and CLI surface.
- Do not add framework-style abstractions for future profilers or PID managers.
- Introduce new types only where they solve current lifecycle, consistency, or testability issues.
- Follow Go guidance for explicit context propagation, cancellation-aware pipelines, and consumer-oriented interfaces.

## Chosen Architecture

### `cmd/gospy`

`cmd/gospy` remains responsible for:

- CLI flags
- config extraction
- logger setup
- handing a concrete config to the runtime package

It stops owning goroutine orchestration directly.

### `internal/app`

Add a new runtime package with a single entry point:

```go
func Run(ctx context.Context, cfg Config) error
```

`internal/app` becomes the only place that:

- wires the pipeline together
- owns signal handling and cancellation
- applies restart policy
- waits for all goroutines to finish
- returns the final error or `nil`

Implementation should use `errgroup.WithContext` so cancellation and error propagation are coordinated in one place.

### `internal/phpspy`

`internal/phpspy` remains the concrete integration for `phpspy`, but the split between `internal/profiler` and `internal/parser` is removed from the orchestration path.

`phpspy` should own:

- process start
- process wait
- profiler-specific validation
- parser implementation for phpspy output

The runtime should resolve the profiler type once, not once for process launch and once again for parsing.

### `internal/pyroscope`

Keep the package focused on:

- payload construction
- HTTP client behavior
- send worker behavior
- statistics aggregation

Background components should expose explicit run semantics rather than fire-and-forget startup with hidden goroutine ownership.

### Domain Packages

Keep these packages as domain helpers for now:

- `internal/collector`
- `internal/tag`
- `internal/transform`
- `internal/validator`

They already have narrow roles. The refactor should reuse them and avoid unnecessary churn.

## Data Flow

The runtime pipeline stays conceptually the same:

1. Start `phpspy`
2. Parse stdout into `collector.Sample`
3. Aggregate samples by tag
4. Send aggregated data to Pyroscope
5. Periodically log send statistics

The difference is that each stage must now obey cancellation and shutdown rules explicitly.

## Lifecycle Rules

1. If the profiler exits and restart policy says "stop", the application must return cleanly.
2. Every send to a channel in the pipeline must respect `ctx.Done()`.
3. EOF from the profiler must flush the currently accumulated trace before exit.
4. Background workers must be started and awaited by the runtime owner, not abandoned as detached goroutines.
5. Statistics should count only real errors as errors.

## Package Boundary Decisions

### Remove orchestration duplication

The current `internal/profiler` and `internal/parser` packages act as parallel registries for the same concrete source. That split is not giving useful modularity and is causing correctness issues. The runtime should instead bind one concrete source once.

### Avoid speculative interfaces

Do not introduce generic interfaces such as `TargetSelector` or multi-profiler registries yet. They are not required to fix the current behavior and would add indirection without current value.

### Keep interfaces at consumption points only

Where an interface is still useful, it should be introduced by the package that consumes the dependency, not by the implementation package.

## Testing Strategy

### Runtime and lifecycle tests

Add focused tests around the new runtime package for:

- clean exit when profiler finishes without restart
- restart behavior for each supported policy
- cancellation propagation through the pipeline

### Parser tests

Extend parser tests to cover:

- flush on EOF without trailing blank line
- cancellation while downstream blocks

### Pyroscope tests

Rewrite most client tests around a custom `http.RoundTripper` to verify request construction and response handling without a real listener.

Keep integration-style HTTP tests only if they validate something transport-specific that unit seams cannot cover.

## Non-Goals

- No support for new profilers
- No PID-manager integration
- No new user-facing CLI features
- No broad package renaming beyond what is necessary for correctness and readability
