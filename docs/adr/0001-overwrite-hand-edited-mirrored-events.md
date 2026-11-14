# 1. Overwrite a mirrored event edited by hand in the target

- Status: accepted
- Date: 2026-11-13
- Concept: `docs/concepts/calbridge.md`

## Context

- An edge copies a source event into a target calendar and keeps the copy up to date.
- Nothing stops the person who owns the target calendar from editing a copy there.
- `docs/concepts/calbridge.md` leaves open what happens then: overwrite on the next reconcile, or
  detach and stop managing the copy.
- The engine already holds the hash of the content it wrote, so it can tell an edited copy from an
  untouched one on every pass.
- The problem this tool exists for is hand maintained duplicates that drift out of date and hide
  conflicts.

## Decision

- The next reconcile overwrites a mirrored event that was edited in the target.
- A copy is a projection of its source, and the source is the only place its content comes from.
- An edit that survived would leave an event claiming to mirror a calendar it no longer matches,
  which is the drift the tool removes.
- Detaching needs a per copy state and a way to re-attach, neither of which the mapping table or
  the UI has.
- Overwriting is what keeps the teardown promise: deleting an edge removes every event it wrote,
  including the ones somebody changed.
- An edit worth keeping belongs in the source calendar, where every edge carries it onward.

## Consequences

- An edit made in the target is lost, without warning, at the next reconcile.
- Someone who adds a note to a mirrored event sees it disappear and has no record of what it was.
- Each pass compares the copy in the target against the stored hash, which means reading the target
  calendar in full on every reconcile.
- A connector that normalises more than its capabilities declare makes every copy look edited, and
  the edge rewrites it on each pass.
- The marker on each copy is what makes the rule legible: an event carrying it announces that
  calbridge maintains it.

## Alternatives

- Detach on edit and stop managing the copy: leaves a stale event nobody updates, and the teardown
  promise would no longer hold for it.
- Ask the user on each conflict: there is no interface to ask through, and a sync that waits for an
  answer stops being a sync.
- Keep the edit and merge the rest field by field: the two sides have no common history, so a merge
  would be a guess with no way to check it.
- Write copies into a read only calendar: no provider offers one per calendar, and an edge may write
  into a calendar the user picked.
