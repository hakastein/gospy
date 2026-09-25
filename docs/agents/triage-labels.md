# Triage labels

Five triage labels describe where an issue stands. They are orthogonal to the type labels
(`fix`, `feature`, `refactor`, …) that drive the release notes: an issue normally carries one of
each.

| Label | Meaning |
| --- | --- |
| `needs-triage` | The maintainer still has to evaluate this issue |
| `needs-info` | Waiting on the reporter for more information |
| `ready-for-agent` | Fully specified; an agent can pick it up unattended |
| `ready-for-human` | Needs human judgement or access to implement |
| `wontfix` | Will not be actioned |

`needs-triage` is the default state of anything freshly filed. An issue leaves it for
`needs-info`, `ready-for-agent`, `ready-for-human` or `wontfix` — never for nothing.

`ready-for-agent` is a claim about the ticket, not about the difficulty: it means the observed
failure, the expected behaviour and the acceptance criteria are all written down, so no
clarifying question is needed to start. If you would have to ask the reporter anything, the label
is `needs-info`.

`wontfix` also excludes the issue, and any pull request carrying it, from the generated release
notes — see [`docs/releasing.md`](../releasing.md).

Apply and remove them with `gh issue edit <number> --add-label`/`--remove-label`; the mechanics
are in [`issue-tracker.md`](./issue-tracker.md), and the type labels are in
[`workflow.md`](./workflow.md).
