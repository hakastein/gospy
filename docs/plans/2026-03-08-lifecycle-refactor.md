# Lifecycle Refactor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor the current runtime so shutdown, restart behavior, parser backpressure, and Pyroscope tests are correct and explicit without adding speculative abstractions.

**Architecture:** Move lifecycle orchestration into a new `internal/app` runner, bind the current `phpspy` source only once, and make each pipeline stage cancellation-aware. Keep domain helpers small and concrete, and test HTTP client logic mostly through a custom `RoundTripper` seam.

**Tech Stack:** Go, `urfave/cli/v2`, `zerolog`, `golang.org/x/sync/errgroup`, `golang.org/x/time/rate`, `testify/require`

---

### Task 1: Create the runtime package boundary

**Files:**
- Create: `internal/app/run.go`
- Modify: `cmd/gospy/app.go`
- Test: `internal/app/run_test.go`

**Step 1: Write the failing runtime test**

Add a test in `internal/app/run_test.go` that models a profiler session ending normally with restart disabled and asserts `Run` returns instead of blocking.

**Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/app -run TestRunStopsAfterProfilerExit -v`

Expected: package or symbol missing.

**Step 3: Introduce `internal/app` with a concrete config and runner skeleton**

Create:

- `type Config struct { ... }`
- `func Run(ctx context.Context, cfg Config) error`

Move only orchestration responsibilities from `cmd/gospy/app.go` into this package. Keep CLI parsing in `cmd/gospy`.

**Step 4: Rewire the command entrypoint**

Update `cmd/gospy/app.go` so it:

- builds `app.Config`
- calls `app.Run(ctx, cfg)`
- no longer owns worker startup logic directly

**Step 5: Run the targeted test again**

Run: `go test ./internal/app -run TestRunStopsAfterProfilerExit -v`

Expected: test still fails, but now on behavior rather than missing package.

**Step 6: Commit**

```bash
git add cmd/gospy/app.go internal/app/run.go internal/app/run_test.go
git commit -m "refactor: add runtime runner package"
```

### Task 2: Replace ad hoc lifecycle control with coordinated cancellation

**Files:**
- Modify: `internal/app/run.go`
- Modify: `internal/supervisor/supervisor.go`
- Test: `internal/app/run_test.go`

**Step 1: Write failing lifecycle tests**

Add tests for:

- normal exit without restart returns `nil`
- cancellation stops all runtime goroutines
- restart policy triggers a new session only for the matching condition

**Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -run 'TestRun(StopsAfterProfilerExit|HonorsRestartPolicy|CancelsWorkers)' -v`

Expected: one or more failures due to current lifecycle behavior.

**Step 3: Implement coordinated runtime ownership**

Use `errgroup.WithContext` in `internal/app/run.go` to own:

- signal watcher
- sample ingestion subscriber
- stats aggregator
- pyroscope workers
- profiler supervision loop

Adjust `internal/supervisor/supervisor.go` so it returns an error or `nil` instead of hiding terminal behavior inside a detached loop.

**Step 4: Remove the unconditional wait on `ctx.Done()` after profiler completion**

Ensure the runtime exits when work is done and policy does not require restart.

**Step 5: Run the targeted tests again**

Run: `go test ./internal/app -run 'TestRun(StopsAfterProfilerExit|HonorsRestartPolicy|CancelsWorkers)' -v`

Expected: PASS

**Step 6: Commit**

```bash
git add internal/app/run.go internal/app/run_test.go internal/supervisor/supervisor.go
git commit -m "refactor: coordinate runtime shutdown with errgroup"
```

### Task 3: Resolve profiler type only once

**Files:**
- Modify: `internal/profiler/profiler.go`
- Modify: `internal/parser/parser.go`
- Modify: `internal/app/run.go`
- Test: `internal/app/run_test.go`

**Step 1: Write the failing regression test**

Add a test that passes a path such as `/usr/bin/phpspy` and asserts the runtime accepts it consistently for both launch and parse setup.

**Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/app -run TestRunAcceptsProfilerPath -v`

Expected: failure due to inconsistent profiler identification.

**Step 3: Collapse the duplicated profiler resolution**

Refactor so the runtime chooses the concrete `phpspy` integration once. After this change:

- either remove the runtime dependence on `internal/parser`
- or change the parser construction path so it consumes the already resolved profiler identity rather than the raw CLI string

Prefer the smallest change that eliminates double dispatch.

**Step 4: Run the targeted test again**

Run: `go test ./internal/app -run TestRunAcceptsProfilerPath -v`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/app/run.go internal/profiler/profiler.go internal/parser/parser.go internal/app/run_test.go
git commit -m "refactor: resolve profiler integration once"
```

### Task 4: Make parser output cancellation-aware

**Files:**
- Modify: `internal/phpspy/parser.go`
- Test: `internal/phpspy/parser_test.go`

**Step 1: Write the failing parser backpressure test**

Add a test that:

- uses a full or non-consuming output channel
- cancels the context
- asserts `Parse` returns instead of blocking forever

**Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/phpspy -run TestParserParseReturnsOnCancellationWhileChannelIsBlocked -v`

Expected: hang or failure.

**Step 3: Implement cancellation-aware sends**

Change the sample send path in `internal/phpspy/parser.go` to:

```go
select {
case foldedStacks <- sample:
case <-ctx.Done():
    return ctx.Err()
}
```

Refactor method signatures as needed so `processTrace` can return an error to `Parse`.

**Step 4: Run the targeted test again**

Run: `go test ./internal/phpspy -run TestParserParseReturnsOnCancellationWhileChannelIsBlocked -v`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/phpspy/parser.go internal/phpspy/parser_test.go
git commit -m "fix: make parser sends cancellation-aware"
```

### Task 5: Flush the final trace on EOF

**Files:**
- Modify: `internal/phpspy/parser.go`
- Test: `internal/phpspy/parser_test.go`

**Step 1: Write the failing EOF flush test**

Add a test where input ends immediately after the last frame without a blank separator and assert the final sample is emitted.

**Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/phpspy -run TestParserParseFlushesFinalTraceOnEOF -v`

Expected: failure because the final trace is dropped.

**Step 3: Flush before exiting on scanner termination**

On `scanner.Scan() == false`:

- record `scanner.Err()`
- if `currentTrace` is non-empty, process it once
- then return the scanner error if any

**Step 4: Run the targeted test again**

Run: `go test ./internal/phpspy -run TestParserParseFlushesFinalTraceOnEOF -v`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/phpspy/parser.go internal/phpspy/parser_test.go
git commit -m "fix: flush final parser trace on eof"
```

### Task 6: Correct stats aggregation semantics

**Files:**
- Modify: `internal/pyroscope/statistic.go`
- Create: `internal/pyroscope/statistic_test.go`

**Step 1: Write the failing stats test**

Add a test that feeds successful and failed request stats and verifies:

- successes do not create an error bucket
- failures are counted separately

**Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/pyroscope -run TestStatsAggregatorCountsOnlyRealErrors -v`

Expected: failure because `nil` is currently counted as an error key.

**Step 3: Implement the fix**

Update the aggregator so it only counts errors when `stat.Error != nil`. Prefer a stable log shape such as `map[string]int` keyed by `err.Error()`.

**Step 4: Run the targeted test again**

Run: `go test ./internal/pyroscope -run TestStatsAggregatorCountsOnlyRealErrors -v`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/pyroscope/statistic.go internal/pyroscope/statistic_test.go
git commit -m "fix: count only real pyroscope errors"
```

### Task 7: Rewrite Pyroscope client unit tests around `RoundTripper`

**Files:**
- Modify: `internal/pyroscope/client_test.go`

**Step 1: Replace server-based unit tests with transport-based tests**

Introduce a small test `RoundTripper` that captures the outgoing request and returns a controlled `http.Response` or error.

Cover:

- URL normalization and `/ingest` path handling
- headers
- auth
- request body contents
- JSON and non-JSON error responses
- transport error
- canceled context

**Step 2: Run the targeted client tests**

Run: `go test ./internal/pyroscope -run 'Test(NewClient|Client_Send)' -v`

Expected: PASS without opening a local listener.

**Step 3: Keep or drop integration tests deliberately**

If there is still one transport-level behavior that truly requires `httptest.NewServer`, keep exactly one narrow integration test and clearly separate it from the unit tests.

**Step 4: Commit**

```bash
git add internal/pyroscope/client_test.go
git commit -m "test: use roundtripper seam for pyroscope client"
```

### Task 8: Tighten verification and remove dead paths

**Files:**
- Modify: any touched files from previous tasks
- Test: `./...`

**Step 1: Remove obsolete orchestration code**

After the runtime package is in place and tests pass, remove code paths that are no longer needed, especially duplicated lifecycle glue in `cmd/gospy/app.go`.

**Step 2: Run formatting**

Run: `gofmt -w cmd/gospy/app.go internal/app/run.go internal/app/run_test.go internal/supervisor/supervisor.go internal/phpspy/parser.go internal/phpspy/parser_test.go internal/pyroscope/statistic.go internal/pyroscope/statistic_test.go internal/pyroscope/client_test.go`

**Step 3: Run focused package tests**

Run:

- `go test ./internal/app ./internal/phpspy ./internal/pyroscope -v`

Expected: PASS

**Step 4: Run the full test suite**

Run: `go test ./...`

Expected: PASS

**Step 5: Commit**

```bash
git add cmd/gospy/app.go internal/app/run.go internal/app/run_test.go internal/supervisor/supervisor.go internal/phpspy/parser.go internal/phpspy/parser_test.go internal/pyroscope/statistic.go internal/pyroscope/statistic_test.go internal/pyroscope/client_test.go
git commit -m "refactor: tighten runtime lifecycle and tests"
```
