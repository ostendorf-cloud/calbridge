# Deliberate gaps

What the code does not do yet, on purpose.

## No route creates a provider connection

- The connector registry and the sync engine are wired into the running service.
- No route creates a provider connection yet, so no calendar credentials are stored.
- The engine therefore has nothing to run on, and no event is written into a provider calendar.
- The landing server's privacy policy says this in its own "What is not in operation yet"
  section, rather than describing the design as if it ran.
- Keep the two together when a connection route lands.

## A refused credential is remembered in memory

- The mirror service records which edges the provider turned away, and the graph API shows it as
  `credential_refused`.
- The record is lost on restart and is rebuilt by the next pass.
- Deleting an edge answers `credential_refused` while its credential is refused, and the edge
  stays. No route exists yet to reconnect an account, so that edge cannot be removed meanwhile.
- An edge whose stored credential cannot be decoded, or whose provider is not registered, cannot
  be deleted either, because the binder cannot build its endpoint and the delete stops before
  the engine runs.

## The teardown of a shared managed calendar

- `internal/sync`'s teardown removes a managed target calendar at the provider.
- It does so even when a second edge writes into the same calendar.
- That is the engine's contract and out of scope for the wiring. See `internal/sync/AGENTS.md`.
