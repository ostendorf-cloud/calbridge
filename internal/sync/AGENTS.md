# internal/sync

`internal/sync` turns a source change into target operations by applying an edge's field mask.
It is the only package meant to know what a detail level means.

## What it owns

- The mapping table: edge id, source event id, target event id, start, end, content hash.
- Recurrence expansion for the case where source and target disagree on a rule.
- The marker that stops calbridge's own writes from being copied onward as source changes.

## Reconcile and teardown

- The full reconcile reads both calendars from the zero sync token. It depends on no stored
  position and no delivered notification.
- The teardown of an edge removes every event it wrote, the edge row included. That holds for
  an edge whose grant was revoked too.
- A mirrored event edited by hand in the target is overwritten on the next reconcile.
- A revoked grant takes the edge with it. See `docs/adr/`.

## The one edit not overwritten

- A single overridden occurrence in a target that keeps its overrides inside the master's own
  object.
- No id such a provider accepts names it. The pass leaves it to the master's object instead of
  asking for a write the declared capabilities already ruled out.
- An object holding overrides and no master is removed by the master's id, on a reconcile as on
  a teardown.

## Not wired yet

See `docs/deliberate-gaps.md` for `EnforceGrants`, `DeleteEdge` and `Teardown`.
