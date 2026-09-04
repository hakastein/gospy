# Contributing to gospy

gospy runs [phpspy](https://github.com/adsr/phpspy), folds its output into stacks and ships them
to [Pyroscope](https://pyroscope.io/). It is a small Go program with a narrow job, and the bar for
a change is that someone can still understand it a year from now.

Bug reports, questions and patches are all welcome.

## Before you start

For anything larger than a typo or an obvious one-line fix, open an issue first. Agreeing on the
shape of a change in a ticket is cheaper than redoing a pull request.

## Getting set up

You need Go 1.23.3 or newer and `make`.

```bash
git clone https://github.com/hakastein/gospy.git
cd gospy
make build   # builds ./gospy
make test    # runs the suite over ./cmd/... and ./internal/...
```

The tests are hermetic: none of them need phpspy, a PHP process or a Pyroscope server. Running
gospy for real does need phpspy and the privileges it requires to attach to a running process —
see [SECURITY.md](SECURITY.md) for the privilege model and the [README](README.md) for flags and
container examples.

### Checks

| Command | What it does |
| --- | --- |
| `make build` | Builds the binary |
| `make test` | Clears the test cache and runs everything under `cmd/` and `internal/` |
| `make vet` | `go vet` over the same packages |
| `make fmt` | `go fmt` — run it before you commit |
| `make coverage` | Writes `coverage.out`; `make coverage-html` renders it |
| `make bench` | Benchmarks with `-race` |

Pull request CI runs `make test`. Static analysis is JetBrains Qodana with the
`qodana.recommended` profile ([`qodana.yaml`](qodana.yaml)) — there is no local `make lint`
target, so `make fmt` and `make vet` are the lint you can run yourself.

If one of these commands fails in a way that looks unrelated to your change, say so in the issue
or pull request rather than working around it.

## Proposing a change

### Branches

Never commit to `main`. Branch as `<type>/<slug>`, with a lowercase, hyphenated slug:

```
fix/parser-drops-final-trace
feature/pyroscope-auth-from-env
docs/community-files
```

The type is picked once and reused for the commit prefix and the pull request label, because the
release notes are generated from merged pull requests:

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

### Commits and pull request titles

Write [Conventional Commits](https://www.conventionalcommits.org/): `type: subject` or
`type(scope): subject`, subject in the imperative with no trailing period. The body explains
**why** — the diff already says what.

The pull request title must be a conventional-commit line too. The changelog builder reads the
type out of it, so a correctly titled pull request is categorised even if labelling is forgotten;
a free-form title with no labels lands in 📦 Uncategorized.

### Labels

Apply every label that honestly describes the change. A refactor that also fixes a bug and adds
tests gets `refactor`, `fix` and `test`; section precedence decides where it is published, so
there is no reason to drop a label to steer the outcome. Apply `breaking` by hand for anything
that changes the CLI surface, removes a flag or alters an existing default — the `!` marker in a
conventional title is not detected.

The full vocabulary, the precedence order and the machinery behind it are in
[`docs/releasing.md`](docs/releasing.md); the same conventions written out for agents are in
[`docs/agents/workflow.md`](docs/agents/workflow.md).

### Pull requests: say what you verified

Run `make test` and `make build` before opening a pull request, and state in the body what you
actually ran and what you did not cover. "Tested manually against a live php-fpm, no automated
test for the restart path" is a useful sentence; silence is not. The pull request template asks
for exactly this.

Keep a pull request to one coherent change. Unrelated cleanups belong in their own branch.

## What a good issue contains

- What you did, what you expected and what happened instead.
- The gospy version (`gospy --version`) and the full command line, with the auth token redacted.
- The phpspy version and how it is invoked, if the problem involves parsing or process handling.
- Where it runs: host, container, PID namespace shared with php-fpm, and so on.
- Relevant log output — run with `-v` or `-vv` for more detail.

Title the issue the same way as a pull request, so the ticket and the change that closes it read
alike. Issue templates for bugs and features are offered when you open one.

## Tests

The rules that matter when you add a test:

- **Isolated package.** Tests live in `package <name>_test`, never in the package they exercise.
  If something can only be tested from inside the package, treat that as a design problem in the
  production code rather than a reason to cross the boundary.
- **testify.** Assert with `github.com/stretchr/testify/require`.
- **Table-driven** cases with `t.Run()` subtests where there is more than one input worth naming.
- **Fixtures** go in a `testdata` directory next to the test.
- **Behaviour, not internals.** Assert observable behaviour and contracts, not private helpers or
  exact log lines. Do not write tests whose point is to check a log message.
- Cover the edge cases and the invalid input, not just the happy path.

## Code style

Standard Go: `gofmt`, errors checked and returned early, imports grouped stdlib / external /
internal, exported types and functions documented with comments that start with the element name,
logging through zerolog. Comments and identifiers are in English. Do not add comments describing
how the code used to look or how you changed it.

Follow the conventions already in the file you are editing rather than importing new ones.

## Domain vocabulary

[`CONTEXT.md`](CONTEXT.md) is the glossary — sample, trace block, folded stack, entry point,
static and dynamic tags, batch, ingest, restart policy. Use those words in issues, commits and
identifiers, and avoid the synonyms it lists. Decisions with lasting consequences are recorded as
ADRs in [`docs/adr/`](docs/adr/).

## Coding agents

[`AGENTS.md`](AGENTS.md) is the same set of rules written for AI coding agents, with the
supporting detail in [`docs/agents/`](docs/agents/). If you change a convention here, change it
there too — they are meant to say the same thing.

## Code of conduct

Participation in this project is covered by the [Code of Conduct](CODE_OF_CONDUCT.md). Security
problems go through [SECURITY.md](SECURITY.md), not a public issue.
