# gospy

gospy discovers PHP processes, runs one phpspy per process to sample their stacks, aggregates
the samples by tag, and ships batches to Pyroscope's ingest endpoint.

## Language

**Sample**:
One observed stack occurrence at a point in time, with its tags and the rate it was sampled at.

**Trace block**:
The unit of phpspy stdout: numbered frame lines plus `#`-prefixed meta lines, terminated by a blank line.

**Folded stack**:
A semicolon-joined call chain (`main;handler;query`) — the wire format Pyroscope ingests.
_Avoid_: collapsed stack, stack string

**Entry point**:
The script that started the request, taken from the outermost frame. Samples can be filtered by entry-point glob patterns.

**Static tag**:
A fixed `key=value` attached to every sample of a target for the whole run.

**Dynamic tag**:
A tag extracted per request from a trace block's meta line by key, with an optional regex rewrite.

**Batch**:
All folded stacks collected for one tag set at one sample rate over an interval, with its time range — the unit shipped to Pyroscope in a single request.
_Avoid_: chunk, tag group

**Ingest**:
The pipeline stage that ships batches to Pyroscope: transport, pacing, retrying, concurrency, and send statistics are its internal concern.
_Avoid_: sender, uploader, worker pool

**Target**:
A named group of processes selected by matchers, with its own slots, sample rate, scan interval, rotation period, tags, entry-point filters and phpspy arguments.
_Avoid_: profile, job, instance

**Scan**:
One read of the process table, classified into targets. Each target examines the process table on its own interval.
_Avoid_: poll, discovery run

**Slot**:
One concurrent attach a target may hold. `max-processes` is the slot count.
_Avoid_: thread, worker

**Attach**:
One `phpspy -p <pid>` process and its lifetime, from start to exit or detach.
_Avoid_: session, tracer

**Rotation**:
Detaching an attach that reached its age limit to hand the slot to a waiting process.
_Avoid_: restart, cycling
