# Releasing and release notes

Release notes are generated, not written by hand. This page explains what to put on a pull
request so it lands in the right section of the next release.

## Cutting a release

Push an annotated tag matching `vX.Y.Z`:

```bash
git tag -a v0.12.0 -m "v0.12.0"
git push origin v0.12.0
```

**Tag a commit whose checks are green.** The tag may sit on any branch, but it should be on
`main` — that is what the published archives and image are taken to mean. Before building
anything, [`.github/workflows/release.yml`](../.github/workflows/release.yml) calls
[`pull-request.yml`](../.github/workflows/pull-request.yml) and runs the same Lint and Test jobs
a pull request goes through, against the tagged commit. A red gate stops the release, so a tag on
a broken commit publishes nothing rather than publishing a broken binary.

No tag, no release — merging to `main` on its own publishes nothing.

To retry a failed release, fix the problem, delete the tag locally and remotely, and push it
again at the new commit.

## What a release publishes

The build is driven by [`.goreleaser.yaml`](../.goreleaser.yaml).

| Artifact | Notes |
| --- | --- |
| `gospy_<version>_<os>_<arch>.tar.gz` | linux and darwin, amd64 and arm64. Each holds `gospy`, `LICENSE` and `README.md`. |
| `checksums.txt` | SHA-256 of every uploaded file, archives and SBOMs alike. |
| `gospy_<version>_<os>_<arch>.tar.gz.sbom.json` | SPDX bill of materials per archive, catalogued by syft. |
| `ghcr.io/hakastein/gospy:<version>` and `:latest` | Multi-arch (linux amd64 and arm64) manifest. `latest` is not moved by a prerelease tag. |

`<version>` is the tag without the leading `v`, so `v0.12.0` produces
`gospy_0.12.0_linux_amd64.tar.gz` and `ghcr.io/hakastein/gospy:0.12.0`. A tag carrying a
prerelease suffix (`v1.0.0-rc1`) is published as a GitHub prerelease automatically.

Archives and `checksums.txt` carry a [GitHub build
attestation](https://github.com/hakastein/gospy/attestations), so a download can be traced back
to the workflow run and commit that produced it:

```bash
gh attestation verify gospy_0.12.0_linux_amd64.tar.gz --repo hakastein/gospy
```

### The container image

gospy drives phpspy, and phpspy attaches to the PHP process with `ptrace`, so gospy has to run in
the same container as php-fpm. The published image therefore holds the binary and nothing else —
it is a source to copy from, not a runtime:

```dockerfile
COPY --from=ghcr.io/hakastein/gospy:0.12.0 /gospy /usr/local/bin/gospy
```

### Verifying the build locally

`make snapshot` runs the same goreleaser pipeline without publishing, and writes the archives,
checksums and SBOMs to `dist/`. It needs `goreleaser` and `syft` on `PATH`; add `--skip=sbom` by
hand if syft is missing.

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

`breaking`, `feature`, `feat`, `enhancement`, `fix`, `bug`, `performance`, `perf`, `refactor`,
`test`, `documentation`, `docs`, `ci/cd`, `ci`, `build`, `chore`, `style`, `revert`, plus the
excluding `ignore`, `duplicate` and `wontfix`.

The conventional-commit types (`feat`, `perf`, `docs`, `ci`, `build`, `style`, `revert`) are
matched as labels too, so applying `feat` to a pull request works exactly like `feature`.

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
