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
- The grantee can no longer resolve the calendar the edge names, because row level security scopes
  `calendars` to its owner, so the edge names an id nothing can be read from.

## Decision

- Revoking a grant deletes the edges it covered, after removing every event they wrote.
- There is no stopped state to put the edge in: it would need a column, a migration and a way to
  resume from it, and a dead row nobody can act on is worse than none.
- Issue #9 asks for the same treatment a deleted edge gets, and a deleted edge takes its row.
- An edge the grant no longer covers cannot run again either way: the engine's own coverage check
  refuses it on every pass, and the trigger on `edges` refuses to recreate or update it while no
  grant exists.
- The row left standing would read as a working edge in the topology the UI and the MCP server
  show, while writing nothing.

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
- Keep the row for a grantee who already knows the calendar id: the id is no longer the point,
  since the row would describe a sync that cannot happen.
- Stop at removing the copies and leave the edge: the next pass would copy them all back, because
  nothing else refuses the edge at run time.
- Narrow the edge to the grant's own level instead of deleting it: changes what the grantee asked
  for without telling them, and has no answer at all when the grant is gone entirely.
