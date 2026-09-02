# gospy

gospy bridges phpspy's stack sampling to Pyroscope: it parses phpspy output into samples, aggregates them by tag, and ships batches to Pyroscope's ingest endpoint.

## Language

**Sample**:
One observed stack occurrence at a point in time, with its tags.

**Trace block**:
The unit of phpspy stdout: numbered frame lines plus `#`-prefixed meta lines, terminated by a blank line.

**Folded stack**:
A semicolon-joined call chain (`main;handler;query`) — the wire format Pyroscope ingests.
_Avoid_: collapsed stack, stack string

**Entry point**:
The script that started the request, taken from the outermost frame. Samples can be filtered by entry-point glob patterns.

**Static tag**:
A fixed `key=value` attached to every sample for the whole run.

**Dynamic tag**:
A tag extracted per request from a trace block's meta line by key, with an optional regex rewrite.

**Batch**:
All folded stacks collected for one tag set over an interval, with its time range — the unit shipped to Pyroscope in a single request.
_Avoid_: chunk, tag group

**Ingest**:
The pipeline stage that ships batches to Pyroscope: transport, pacing, concurrency, and send statistics are its internal concern.
_Avoid_: sender, uploader, worker pool

**Restart policy**:
The rule for whether a finished profiler session is started again: always, on error, on success, or never.
_Avoid_: restart mode, restart strategy
