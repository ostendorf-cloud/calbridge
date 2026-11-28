# calbridge

Selective calendar mirroring across providers, one detail level per edge.

## Problem

- Six calendars live in three accounts: four in iCloud (Privat, Veranstaltungstechnik, Notes,
  Study), one Google calendar belonging to a second person, one Outlook M365 work calendar.
- No calendar shows all of them, so a conflict between a work meeting and a private appointment is
  only visible by checking two clients.
- Work colleagues must see that a time slot is taken, and must not see what the private appointment
  is.
- The second person should see the details of Privat, Veranstaltungstechnik and Study, and only that
  a work event exists.
- The private calendar should carry the exact work events and the second person's events.
- No provider offers a per-recipient detail level: Google and iCloud share a whole calendar at one
  permission level, Outlook cannot subscribe to iCloud at all.
- The current workaround is typing duplicate placeholder events by hand, which drifts out of date
  within a day and silently hides conflicts.

## Goals

- A sync edge copies events from one source calendar into one target calendar in one direction, at a
  detail level the edge fixes.
- Detail levels are presets (full copy, title and time, busy block) with a per-field override for
  cases a preset misses.
- An edge writes either into a calendar calbridge creates and owns, or into an existing calendar the
  user selects.
- Google Calendar, Microsoft 365 and iCloud are readable and writable.
- A fourth provider is added by writing one package against the connector interface, or by adding a
  CalDAV profile, without changing the sync engine, the database schema or the UI.
- A user grants another user access to one of their calendars at a maximum detail level; the grantee
  builds edges that the grant bounds.
- Anyone can create an account themselves, with a password or with a provider sign-in, without an
  invite and without an account in anyone's identity provider.
- An AI agent can read the sync topology and set up an edge or a grant through an MCP server, within
  the same authorisation rules a person gets.
- A change reaches its targets within a minute where the provider pushes a notification, and within
  the reconcile interval otherwise.
- Deleting an edge removes every event it wrote, and leaves nothing behind in the target account.
- The six calendars above run on it, with the three detail levels the problem section names.

## Non goals

- Not a calendar client: no browsing, creating or editing of real events in calbridge.
- No scheduling features: no free/busy negotiation, no booking links, no meeting polls.
- No bidirectional edge: direction is a property of the edge, and two opposing edges are a loop the
  engine must detect rather than a supported mode.
- No providers beyond the three named: the CalDAV connector is generic, but iCloud is the only
  profile shipped and tested in the first version, and Exchange on-premises is out entirely.
- No billing, no plans with real prices, no checkout: the public landing page states today that
  calbridge is privately operated and has no prices yet, and carries no payment processing.
- No mobile application; the web UI is responsive and that is all.

## Approach

- Go backend and Vue SPA on govue, laid out like solobill: `cmd/calbridge`, `internal/`, `web/`,
  `charts/calbridge`, plus a `docker-compose.yaml` for local work.
- Postgres with dbmate migrations and sqlc, row level security per tenant, enforced by a database
  role that is `NOSUPERUSER NOBYPASSRLS`.
- calbridge owns identity: a local account with password and TOTP, or sign in with Google,
  Microsoft or Apple through govue's `oidc`. There is no dependency on an external identity
  provider, which is what lets a stranger sign up rather than be invited into one cluster.
- Signing in with a provider asks for identity scopes only. Connecting that provider's calendar is
  a separate consent step, because the calendar scopes are sensitive and asking for them at sign-up
  costs both trust and Google's verification.
- Apple is offered as a sign-in, which grants identity and nothing else; an iCloud calendar is
  still connected separately with an Apple ID and an app-specific password.
- A provider identity used to sign in is never matched against a provider account someone else
  connected as a calendar. The collision creates a separate account, so controlling a mailbox never
  becomes a way into another user's calbridge account.
- `internal/provider` defines one connector interface: list calendars, read changes since a token,
  create and delete a calendar it owns, and create, update and delete an event it owns. Deleting a
  calendar is its own operation, because removing a managed calendar's events is not the same as
  removing the calendar, and an edge that created one has to be able to take it away again.
- A connector registers itself into a registry under a provider id, so adding one is a new package
  and one import, never an edit to a switch statement somewhere in the engine.
- Every connector declares its capabilities (push notifications, native recurrence rules, calendar
  creation, attendees, floating times, writing one overridden occurrence, and rewriting content on
  write); the engine reads that declaration and picks its strategy, so a provider's gaps are known
  at registration rather than discovered at runtime.
- One connector answers once for every connection it serves. A connector standing in front of
  several servers, as the CalDAV one does, declares the intersection, and a server too different for
  that is a second connector under its own id rather than an answer that varies per call.
- A connector that does not store an event exactly as written says so and answers what a write
  turns it into. Without that the engine compares a copy against what it wrote, reads the
  difference as a hand edit, and rewrites every mirrored event on every pass while the sync looks
  like it is working.
- Free/busy is deliberately not a capability: no part of the design reads it, and negotiating
  free/busy is a non goal. Floating times is one, because neither Google nor Graph can store a time
  without a zone, so anchoring a floating source time is a decision the engine must make knowingly.
- Connectors speak one normalised event type and never leak a provider's own model; everything
  provider-shaped, including id formats, timezone handling and quirks, stays inside its package.
- Three implementations: Google Calendar API, Microsoft Graph, and a generic CalDAV connector driven
  by provider profiles, of which iCloud is the first.
- A CalDAV profile carries the endpoint, the discovery and authentication style and the server's
  known quirks, so Nextcloud, Fastmail or Radicale are a profile entry rather than new code.
- Credentials are per connection and typed by the connector: OAuth refresh tokens for Google and
  Microsoft, an Apple ID with an app-specific password for iCloud, all encrypted at rest with a key
  the process reads from the environment.
- `internal/sync` turns a source change into target operations by applying the edge's field mask,
  and is the only package that knows what a detail level means.
- The mapping table holds edge id, source event id, target event id, start, end and a content hash;
  any copied title or body is encrypted at rest, so the database alone does not read as a calendar.
- Every written event carries a marker naming calbridge and the edge, and a connector never reports
  an event it wrote as a source change, which is what breaks mirror loops.
- Recurrence copies the source rule when source and target agree on it, and expands to concrete
  instances inside a rolling window when they do not.
- Change detection combines Google `events.watch` channels and Graph change-notification
  subscriptions on one public webhook endpoint, CalDAV `sync-collection` tokens for iCloud, and a
  scheduled full reconcile for all three, because channels expire and notifications are lossy.
- The UI is a Vue Flow canvas: calendars are nodes grouped by account, an edge is drawn by dragging
  one onto another, and the detail level is edited on the edge.
- The domain is split: `calbridge.de` is the public landing page (what calbridge does, pricing,
  the imprint and the privacy policy), and `app.calbridge.de` is the application itself.
  `calbridge.de` is what Google's brand and domain validation checks, linked from the OAuth
  consent screen. The landing page is its own static build, `cmd/calbridge-landing` (see
  `internal/landingapi` and `internal/landingconfig`), with its own container and its own
  `charts/calbridge` deployment, so a correction to the privacy policy's text never redeploys
  the application. It carries no session, no CSRF protector and no database connection, and
  links to `app.calbridge.de`; the application links back to `calbridge.de` in turn, both
  through a value each deployment configures rather than a host fixed in either build.
- The legal pages have fixed paths, and the OAuth consent screens link to them:
  `https://calbridge.de/en/privacy`, `https://calbridge.de/en/terms` and
  `https://calbridge.de/en/imprint`, with the same three under `/de/`. The short paths
  `/privacy`, `/terms` and `/imprint`, and the German aliases `/datenschutz`, `/agb` and
  `/impressum`, redirect from the server to the page of the visitor's language. The operator named
  on those pages comes from the deployment's configuration, never from the repository.
- The terms exist although nothing is sold today. The owner decided on 2026-11-28 that every site
  that becomes a product has terms, so `/terms` and `/agb` are both served, and this replaces the
  line of issue #66 that `/agb` is not used. The terms page holds the terms of use of the
  service. It has no dispute resolution section, and the owner reviews it before release, see
  `docs/legal-review.md`.
- A grant is its own record (owner, grantee, calendar, maximum detail level) and is checked on every
  edge write, so revoking it stops the sync without the grantee's cooperation.
- A separate MCP server, its own binary and deployment, lets an AI agent read the sync topology and
  create or change edges and grants on a user's behalf, so setting up a share can be asked for
  rather than clicked.
- The MCP server cannot create, change or delete a provider connection, so no agent ever handles a
  credential or reaches a calendar the user has not already connected by hand.
- It authenticates either by OAuth against calbridge's own accounts, for a client that speaks the
  MCP authorisation flow, or by a scoped API token the user mints and revokes in the UI, shown once
  and stored hashed.
- It calls the same authorisation rules as the web API rather than its own copy, so a grant bounds
  an agent exactly as it bounds a person.

## Alternatives

- Managed sync providers such as Nylas or Cronofy: fastest to integrate, but every event passes a
  third party, which is the problem this tool exists to avoid, and pricing is per connected account.
- Subscribed ICS feeds: no credentials, but read only, delayed by minutes to hours, and Outlook
  refreshes subscriptions on a schedule nobody controls.
- Native provider sharing: whole calendars at one permission level, no per-recipient detail, and
  Outlook cannot consume an iCloud calendar.
- vdirsyncer or a CalDAV script: works between CalDAV servers, has no OAuth providers, no field
  masks, no sharing and no UI.
- A generated virtual calendar served per subscriber instead of written events: no duplicated data
  at all, but it cannot write into a work calendar and inherits the ICS refresh problem above.
- Copying the recurrence rule in all cases: far fewer API calls, but Google, Graph and CalDAV
  disagree on exceptions, detached instances and timezone handling, so the fallback to expanded
  instances is kept.

## Risks

- Google expires refresh tokens after seven days for any OAuth app whose consent screen is in
  testing; the calendar scopes are sensitive, so the app must pass Google's verification before the
  sync survives a week.
- That verification needs a public homepage on an owned and verified domain, a privacy policy on the
  same domain linked from the consent screen, and a demo video, and it takes up to ten days, so a
  private tool still needs a public face and a domain before its Google connector is durable.
- The work M365 tenant has approved the app registration and `Calendars.ReadWrite` consent, so that
  path is clear; the approval covers this one tenant and says nothing about any other organisation a
  future user belongs to.
- Apple has no OAuth and no public calendar API; an app-specific password is entered by hand, grants
  access to the whole iCloud account, and breaks silently when the user revokes it.
- iCloud `sync-collection` pages its results and can truncate a response, so the CalDAV connector
  has to handle partial answers and invalidated tokens rather than assume one round trip.
- Open signup turns calbridge from one person's tool into a service holding strangers' calendar
  credentials, which raises the bar on abuse handling, account recovery and the encryption key's
  custody well above what a private deployment needs.
- An MCP server that can create grants is a path from a compromised agent or a leaked token to a
  shared calendar. Keeping connections out of its reach, scoping and expiring its tokens, and
  logging every grant it creates are what keep that bounded.

## Open questions

- Which Google Cloud project hosts the OAuth client? Next step: create it when the Google connector
  is built, since verification is submitted against a working consent screen and not before.
- Where does the encryption key come from in the cluster: an environment variable from a sealed
  secret, or OpenBao? Next step: decide with the existing deployment pattern, promote to an ADR.
- How are all-day events and floating times normalised between the three providers, given each
  represents them differently?
- What reconcile interval do the providers' rate limits actually permit for six calendars, and how
  does that scale per connected account?
- Does a grant travel with the calendar when the grantee builds a second edge from the mirrored
  copy, or does the mirror end the chain?

## Decisions to record as ADRs

- A mirrored event edited by hand in the target is overwritten on the next reconcile, recorded in
  `docs/adr/0001-overwrite-hand-edited-mirrored-events.md`. A single overridden occurrence is the
  exception where the target keeps its overrides inside the master's own object: there is no id such
  a provider accepts for it, so the engine leaves it to the master's object. The master's next write
  replaces that object and the override with it, and that write follows a changed source or a hand
  edit of the master itself, so an override on a master nobody touches again stands until the edge
  is deleted. Where the object holds overrides and no master at all, everything in it is the edge's
  own write, and both a reconcile and a teardown remove the object by the master's id.
- Apple is reached by CalDAV with an app-specific password, because no OAuth path exists.
- Event content is stored as a minimal mapping with an encrypted details cache, not as a full event
  cache.
- Mirrored events are written to a managed calendar by default, with an existing calendar as an
  explicit per-edge opt-in.
- Recurrence copies rules where possible and expands to instances otherwise.
- The sync graph is drawn with Vue Flow rather than a hand-rolled canvas.
- Connectors register themselves and declare capabilities, and CalDAV is generic with per-server
  profiles, so a provider is added without touching the engine.
- calbridge owns identity, with local accounts and provider sign-in, rather than depending on an
  external identity provider.
- A provider identity used for sign-in is never linked to a provider account someone else connected
  as a calendar.
- The MCP server is its own binary, may not touch connections, and authenticates by OAuth or a
  scoped API token.

## Delivery order

1. Repository scaffold, database, login, and an empty UI shell, following solobill.
2. The connector interface and the iCloud CalDAV connector, with a reconcile loop and polling only.
3. The Google and Microsoft connectors, then push notifications and the webhook endpoint.
4. The Vue Flow canvas, detail-level editing and cross-user grants.
5. The MCP server, once there are edges and grants for it to create.
