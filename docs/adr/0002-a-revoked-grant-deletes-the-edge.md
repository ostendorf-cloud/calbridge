# 2. A revoked grant deletes the edge it covered

- Status: accepted
- Date: 2026-11-14
- Concept: `docs/concepts/calbridge.md`
- Related: `docs/adr/0001-overwrite-hand-edited-mirrored-events.md`

## Context

- A user grants another user access to one of their calendars, and the grantee builds an edge the
  grant bounds.
- The `edges` trigger in `db/migrations` refuses such an edge at write time and never checks it
  again, so revoking the grant leaves an edge pointing into a calendar nobody shares any more.
- Issue #9 hands continuous enforcement to `internal/sync`: a revoked grant stops the edges it
  covered and removes what they wrote, the same way deleting an edge does.
- The schema has no column for an edge that exists but does not run.
- The grantee can no longer see the calendar the edge names, because row level security scopes
  `calendars` to its owner.

## Decision

- Revoking a grant deletes the edges it covered, after removing every event they wrote.
- An edge the grant no longer covers cannot run again: the same trigger refuses to recreate or
  update it while no grant exists.
- Keeping the row would leave the grantee holding a reference to a calendar they may no longer
  enumerate, which is the cross-user existence oracle the schema closes everywhere else.
- A stopped state would need a column, a migration and a user interface to resume from, none of
  which exist, and a dead row with no way to act on it is worse than none.
- The issue asks for the same treatment a deleted edge gets, and a deleted edge takes its row.

## Consequences

- The grantee loses their edge configuration, with no warning and no way to undo it.
- Re-granting access does not bring the edge back; the grantee builds it again.
- A grant narrowed rather than revoked has the same effect, because an edge copying more than its
  grant allows is refused the same way.
- A grant revoked by mistake and restored a minute later still costs the edge and every copy it
  wrote, which are then recreated from the source on the next pass.
- The engine never leaves an edge that silently writes nothing, which is the failure mode a
  stopped state would introduce.

## Alternatives

- Keep the row and mark it stopped: needs a schema change this issue does not own, and leaves a
  reference to a calendar the grantee cannot see.
- Keep the row and let it fail on every pass: turns a revocation into a permanent error in every
  report, and the copies would have to be removed anyway.
- Stop at removing the copies and leave the edge: the next pass would copy them all back, because
  nothing else refuses the edge at run time.
- Narrow the edge to the grant's own level instead of deleting it: changes what the grantee asked
  for without telling them, and has no answer at all when the grant is gone entirely.
