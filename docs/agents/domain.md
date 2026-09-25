# Domain docs

Where this repository keeps its shared vocabulary and its recorded decisions, and how an agent is
expected to use them.

## Read before exploring

- **[`CONTEXT.md`](../../CONTEXT.md)** at the repo root — the glossary. One context, one file;
  there is no context map and no per-package glossary.
- **[`docs/adr/`](../adr/)** — architecture decision records. Read the ones that touch the area
  you are about to change. Today that is
  [`0001-single-ingest-front-door.md`](../adr/0001-single-ingest-front-door.md), which explains
  why `internal/pyroscope` exposes `Config` / `StartIngest` / `In()` / `Wait()` and keeps
  everything else unexported.

## Use the glossary's vocabulary

When your output names a domain concept — an issue title, a refactor proposal, a hypothesis, a
test name, an identifier — use the term as `CONTEXT.md` defines it, and avoid the synonyms it
lists under _Avoid_ (`collapsed stack`, `chunk`, `sender`, `restart mode`, …).

If the concept you need is not in the glossary, that is a signal: either you are inventing
language the project does not use, in which case reconsider, or there is a real gap worth naming
and adding.

## Adding to the glossary or the ADRs

Add a term to `CONTEXT.md` when a concept has earned a name that is used across packages, not for
every type that exists. Keep the existing shape: bold term, one-sentence definition, an optional
_Avoid_ line for the synonyms it displaces.

Write an ADR when a decision closes off alternatives someone would otherwise reopen — the options
considered and why they lost matter more than the option chosen. Number it in sequence
(`0002-…`) and keep it short.

Implementation plans are not documentation. They belong in the issue or the pull request that
carries the work, not in `docs/`.

## Flag ADR conflicts

If what you are proposing contradicts an existing ADR, say so explicitly instead of quietly
overriding it:

> _Contradicts ADR-0001 (single front door for Pyroscope ingest) — but worth reopening because…_
