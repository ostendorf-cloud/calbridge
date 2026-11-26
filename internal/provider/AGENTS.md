# internal/provider

`internal/provider` defines the one connector interface every provider implements.

- List calendars and read changes since a token.
- Create and delete a calendar it owns.
- Create, update and delete an event it owns.

## Registry and capabilities

- A connector registers itself into a registry under a provider id.
- It declares its capabilities: push notifications, native recurrence rules, calendar creation,
  attendees, floating times.
- Adding Google Calendar, Microsoft Graph or a CalDAV profile is a new package and one line of
  wiring. It is never an edit to a switch statement in the engine.
- A connector also declares whether it writes one overridden occurrence and whether it rewrites
  content on write. It answers what a write turns an event into where it does.

## One normalised model

- Connectors speak one normalised event type and never leak a provider's own model.
- Everything provider-shaped stays inside its package.

## Event id space

- A connector owns the event id space.
- A provider's own identifier becomes an `Event.ID` through `EventIDOf`. It escapes the
  separator a derived occurrence id is joined with.
- `NativeIDOf` is the one way back.
- So a plain event id can never read as an occurrence of another event, whatever a server names
  a resource.
- A caller that has the event reads `Recurrence.MasterID`. One that has only an id cuts it with
  `CutInstanceID`.

## caldav

- `internal/provider/caldav` is the generic CalDAV connector, driven by one profile per server.
- iCloud is the first profile. The package is the only connector so far.

## providertest

- `internal/providertest` holds the in-memory connector every consumer of `internal/provider`
  tests against.
- It is not a test file. `internal/provider`'s own tests and `internal/sync`'s both import it.
- A fake written once per consumer is one contract read twice, and the readings drift.
