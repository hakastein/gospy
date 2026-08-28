# Change workflow

What to do when making a change in this repository: how to name the branch, how to write the
commit and pull request, and which labels to apply.

These are not cosmetic conventions. The release notes are generated from merged pull requests, so
a wrong title or a missing label silently puts the change in the wrong section of the next
release. See [`docs/releasing.md`](../releasing.md) for the machinery.

## Branches

Never commit to `main`. Branch as `<type>/<slug>`, where `<slug>` is lowercase and hyphenated:

```
refactor/lifecycle-runtime
fix/stats-channel-use-pointers
test/obfuscation
```

The type is the same word used for the commit prefix and the label, so it is chosen once:

| Branch prefix | Commit prefix | PR label | Release section |
| --- | --- | --- | --- |
| `feature/` | `feat:` | `feature` | 🚀 Features |
| `fix/` | `fix:` | `fix` | 🐛 Fixes |
| `refactor/` | `refactor:` | `refactor` | ♻️ Refactoring |
| `test/` | `test:` | `test` | 🧪 Tests |
| `perf/` | `perf:` | `performance` | ⚡ Performance |
| `docs/` | `docs:` | `documentation` | 📚 Documentation |
| `ci/` | `ci:` | `ci/cd` | 🏗️ Build & CI |
| `other/` | `chore:` | `chore` | 🧹 Chores |

## Commits

Write [Conventional Commits](https://www.conventionalcommits.org/): `type: subject`, or
`type(scope): subject`. Subject in the imperative, no trailing period.

The body explains **why**, not what — the diff already says what. State the failure the change
fixes, or the constraint that forced the approach.

## Pull requests

**The title must be a conventional-commit line.** The changelog builder extracts the type from
it, so `refactor: own runtime lifecycle in internal/app` is categorised even if labelling is
forgotten. A free-form title with no labels lands in 📦 Uncategorized.

**Apply every label that honestly describes the change.** A refactor that also fixes a bug and
adds tests gets `refactor`, `fix` and `test`. The change is published once, in the
highest-precedence section it matches — precedence resolves placement, so there is no reason to
drop a label to steer the outcome.

Apply `breaking` by hand for anything that changes the CLI surface, removes a flag, or alters an
existing default. The `!` marker in a conventional title is **not** detected.

The full label vocabulary and section precedence live in [`docs/releasing.md`](../releasing.md).
It is the single source of truth; adding a label there without adding it to
`.github/configuration.json` sends PRs to Uncategorized.

Before opening a PR, run the checks in [`AGENTS.md`](../../AGENTS.md) — `make test` and
`make build` at minimum. State the verification you actually ran in the PR body, and say plainly
what you did not cover.

## Issues

Title issues the same way as pull requests, so a ticket and the PR that closes it read alike.

Label an issue with the type it will become (`fix`, `feature`, `refactor`, …) plus a triage
label from [`triage-labels.md`](./triage-labels.md). Mechanics of creating, reading and closing
issues are in [`issue-tracker.md`](./issue-tracker.md).

## Releases

Releases are cut by pushing a `vX.Y.Z` tag; nothing is published by merging to `main` alone.
The procedure and how to preview the notes are in [`docs/releasing.md`](../releasing.md).
