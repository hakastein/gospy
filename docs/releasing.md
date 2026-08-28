# Releasing and release notes

Release notes are generated, not written by hand. This page explains what to put on a pull
request so it lands in the right section of the next release.

## Cutting a release

Push an annotated tag matching `vX.Y.Z` to `main`:

```bash
git tag -a v0.12.0 -m "v0.12.0"
git push origin v0.12.0
```

That triggers [`.github/workflows/release.yml`](../.github/workflows/release.yml), which builds
the binary, assembles the changelog from every pull request merged since the previous tag,
publishes a GitHub release and attaches `vX.Y.Z.tar.gz`.

No tag, no release — merging to `main` on its own only refreshes the coverage badge.

## How a pull request is categorised

The changelog is built by
[`mikepenz/release-changelog-builder-action`](https://github.com/mikepenz/release-changelog-builder-action)
using [`.github/configuration.json`](../.github/configuration.json). A pull request reaches a
section in one of two ways.

**By label** — the labels applied to the merged PR.

**By title** — if the title follows [Conventional Commits](https://www.conventionalcommits.org/)
(`type: subject`, optionally `type(scope): subject`), the type is extracted and treated as a
label. So `fix: flush final trace on eof` is categorised as a fix even with no labels at all.

Labels and titles are additive: either is enough, both together are fine.

## Sections

Listed in precedence order. A PR is **consumed** by the first section it matches, so it appears
exactly once even when it carries several labels.

| Section | Labels | Conventional type |
| --- | --- | --- |
| 💥 Breaking Changes | `breaking` | — |
| 🚀 Features | `feature`, `enhancement` | `feat` |
| 🐛 Fixes | `fix`, `bug` | `fix` |
| ⚡ Performance | `performance` | `perf` |
| ♻️ Refactoring | `refactor` | `refactor` |
| 🧪 Tests | `test` | `test` |
| 📚 Documentation | `documentation` | `docs` |
| 🏗️ Build & CI | `ci/cd` | `ci`, `build` |
| 🧹 Chores | `chore` | `chore`, `style`, `revert` |
| 📦 Uncategorized | anything unmatched | — |

Precedence matters when a PR is genuinely several things at once. A refactor that also fixes a
bug and adds tests, labelled `refactor` + `fix` + `test`, is published under **🐛 Fixes** —
readers scan release notes for fixes first. Label honestly and let precedence decide placement;
do not drop labels to force a section.

Anything landing in **📦 Uncategorized** is a signal that the label vocabulary or the title
convention was missed.

## Breaking changes

The conventional-commit `!` marker (`feat!:`) is **not** picked up — the extractor captures only
the type. Apply the `breaking` label by hand whenever a change alters the CLI surface, removes a
flag, or changes existing default behaviour.

## Keeping a PR out of the notes

Apply `ignore`, `duplicate` or `wontfix`. These take precedence over every category.

## Available labels

`breaking`, `feature`, `enhancement`, `fix`, `bug`, `performance`, `refactor`, `test`,
`documentation`, `ci/cd`, `chore`, plus the excluding `duplicate` and `wontfix`.

Adding a new label means adding it to `.github/configuration.json` too, otherwise PRs carrying it
fall through to Uncategorized.

## Checking the output before tagging

Render the notes for an unreleased range without publishing anything:

```bash
gh api repos/hakastein/gospy/releases/generate-notes \
  -f tag_name=v0.12.0 -f previous_tag_name=v0.11.0 --jq .body
```

That uses GitHub's own generator rather than the action, so the grouping differs — it is a check
that every PR is labelled, not a preview of the final layout. For the exact layout, read the
release the workflow publishes and adjust `.github/configuration.json` if a section is wrong.
