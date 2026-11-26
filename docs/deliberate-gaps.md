# Deliberate gaps

What the code does not do yet, on purpose.

## Provisional calendar ids

- A calendar calbridge is asked to create as an edge's target is written with a provisional
  `external_id`: `calbridge-pending:` plus a uuid.
- No connector is wired into the service yet, and `calendars.external_id` is `NOT NULL`.
- A connector's own calendar creation replaces it with the provider's id.
- See `provisionalExternalIDPrefix` in `internal/httpapi/graph.go`.

## Revoking a grant

- Revoking a grant removes the grant row.
- Tearing down the edges it covered, and taking back the events they wrote, is
  `internal/sync`'s `EnforceGrants`. It runs when the engine is wired.
- The edges belong to the grantee and are out of the owner's scope, so no API handler could do
  it even in principle.
- See `docs/adr/0002-a-revoked-grant-deletes-the-edge.md`.

## No connector in the running service

- No connector is wired into the running service and no route creates a provider connection.
- So no calendar credentials are stored and no event is written into a provider calendar yet.
- Nothing outside `internal/sync` calls `EnforceGrants`, `DeleteEdge` or `Teardown` either.
- So deleting an edge or revoking a grant removes the row and nothing more.
- The landing server's privacy policy says all of this in its own "What is not in operation yet"
  section, rather than describing the design as if it ran.
- Keep the two together when the engine is wired.
