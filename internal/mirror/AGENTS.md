# internal/mirror

`internal/mirror` runs the sync engine inside the service. `internal/sync` decides what an edge
copies. This package decides when that happens and what the engine reaches a provider with.

## What it owns

- `Binder` is the `sync.Binder` the engine is built with. It resolves an edge to its two
  endpoints and decrypts the stored credential through the connector's own decoder.
- A shared source is read in the owner's scope, after the edge owner's own scope confirmed the
  grant. The schema withholds that read from the grantee on purpose. Never widen the policy.
- A source the user can no longer reach binds to the zero endpoint, not an error. The engine's
  teardown needs only the target.
- `Service` is the trigger, the per-user lock and the record of refused credentials.

## Triggers

- A scheduled pass every `RECONCILE_INTERVAL`, and one at start. This is the floor. It needs no
  notification to have arrived.
- A queued pass over one edge after the API creates it.
- A queued `EnforceGrants` after the API revokes a grant. It runs here because the covered edges
  belong to the grantee, outside the owner's scope.
- Edge deletion runs inside the request. The caller is owed the answer whether the copies went.
- A full queue drops the trigger and logs it. The scheduled pass covers it.

## Rules

- One lock per user covers a pass, a delete and a grant teardown. The engine leaves two passes
  over one edge to its caller.
- The lock is a one slot channel, not a mutex. A waiter stops when its context ends, so a delete
  behind a long pass answers at its deadline.
- A provider call never runs inside an open database scope. The binder opens short scopes and
  leaves them before any provider is called.
- `CreateCalendar` runs before the calendar row exists, so the row carries the provider's own
  id from the start. The application role may not update `calendars`.
- After a delete or a teardown, the service deletes the managed calendar rows no edge names.
  The engine removes the calendar at the provider and does not own the table.
- A refused credential is held in memory per edge. A restart forgets it until the next pass.
