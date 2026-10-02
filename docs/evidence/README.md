# PR evidence retention

Keep readable summaries and READMEs, small authored probes/capture scripts,
and screenshots referenced by those summaries. Use `docs/evidence/` for
non-GUI evidence and `apps/gui/artifacts/<task-id>/` for browser captures;
the retention policy is the same in both places. Existing evidence need not
move directories. Small title-sort summaries and probe source, such as
`task-title-sort.md` and `task-title-sort-probe.go.txt`, follow this convention.

Raw network captures and verbose plan JSON may be committed for PR review.
After merge, prune these generated snapshots from the current tree in a
reviewed cleanup, and link the retained summary to the exact commit containing
them. They remain retrievable from Git history. Keep referenced screenshots;
if a screenshot is removed, update its README in the same change. This policy
adds no test, inventory guard, size threshold, or automatic cleanup check.

Each summary should name the source commit, task/PR, fixture/environment,
what was measured and its limits, and any retained reproduction command.
Capture with disposable data and loopback services, without operator data or
model CLIs. Evidence describes the capture moment, not current runtime or
production performance; current authored source remains authoritative.

## Archived snapshots

The following raw captures were pruned after their PRs merged. The associated
summaries, reproduction sources and referenced PNGs remain in the tree.

| Evidence | PR | Immutable capture |
| --- | --- | --- |
| `docs/evidence/list-index-plans-20261001.json` | [#195](https://github.com/hollis-labs/torque/pull/195) | [a22bcfa](https://github.com/hollis-labs/torque/blob/a22bcfa9860bc12d59681389d0959e2c9915aaf6/docs/evidence/list-index-plans-20261001.json) |
| `apps/gui/artifacts/CW-20261001-0574/network.json` | [#209](https://github.com/hollis-labs/torque/pull/209) | [073b369](https://github.com/hollis-labs/torque/blob/073b36974c2837963556fb41af195323e220dda0/apps/gui/artifacts/CW-20261001-0574/network.json) |
| `apps/gui/artifacts/CW-20261001-0575/network.json` | [#202](https://github.com/hollis-labs/torque/pull/202) | [a2ed29b](https://github.com/hollis-labs/torque/blob/a2ed29b68e5e190f75c9a08c65535d41ebcedba2/apps/gui/artifacts/CW-20261001-0575/network.json) |
| `apps/gui/artifacts/CW-20261001-0576/network.json` | [#207](https://github.com/hollis-labs/torque/pull/207) | [bced88a](https://github.com/hollis-labs/torque/blob/bced88a6cb5071af64af6788cc087ec6cbc3bf85/apps/gui/artifacts/CW-20261001-0576/network.json) |
| `apps/gui/artifacts/CW-20261001-0578/network.json` | [#212](https://github.com/hollis-labs/torque/pull/212) | [82a3527](https://github.com/hollis-labs/torque/blob/82a352760898e3db21c5bbc39a4ce87ca7a8fa88/apps/gui/artifacts/CW-20261001-0578/network.json) |

Retrieve a snapshot without restoring it to the working tree:

```sh
git show a22bcfa9860bc12d59681389d0959e2c9915aaf6:docs/evidence/list-index-plans-20261001.json > /tmp/list-index-plans-20261001.json
```

[The index summary](../list-index-plans.md) and `scripts/audit-list-indexes.py`
retain the synthetic fixture methodology and reproduction path. GUI summaries
record whether capture used intercepted fixtures, live isolated handlers, or
response replay. Retained capture scripts may generate a fresh `network.json`;
apply the same review/retention convention to that output.
