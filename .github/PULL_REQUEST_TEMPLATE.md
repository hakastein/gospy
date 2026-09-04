<!--
The pull request TITLE must be a Conventional Commit line, e.g.
  fix: flush the final trace when phpspy exits without a blank line
The release notes are generated from the title and the labels, so both matter.
See CONTRIBUTING.md and docs/releasing.md.
-->

## What and why

<!-- What changes, and the reason for it. The diff already says what; explain why. -->

Closes #

## Labels

<!--
Apply every label that honestly describes the change: feature, fix, refactor, test,
performance, documentation, ci/cd, chore. Add `breaking` by hand if this changes the CLI
surface, removes a flag, or alters an existing default.
-->

- [ ] Labels applied
- [ ] `breaking` applied, or this change is not breaking

## Verification

What I ran:

- [ ] `make build`
- [ ] `make test`
- [ ] `make vet`
- [ ] Other (describe below)

<!-- Paste or summarise the result. Manual testing counts — say what you ran it against. -->

What I did **not** verify:

<!-- Be specific: an untested code path, a platform you could not try, a scenario you could not reproduce. This is the most useful part of the description. -->

## Notes for the reviewer

<!-- Anything worth knowing: a decision you were unsure about, a follow-up you deliberately left out. -->
