# internal/httpapi

`internal/httpapi` builds the HTTP handler.

- The admin listener serves liveness, readiness and metrics.
- The public listener serves signup, login, logout, `GET` and `DELETE /api/v1/me`, the sign-in
  provider list and a pair of routes per configured provider.
- It also serves the SPA fallback for the embedded Vue build.
- Every account decision belongs to `internal/auth`. This package decodes a request, calls that
  package and turns the answer into JSON or a session cookie.
- `graph.go` carries the sync graph API the canvas is a view of: `GET /api/v1/graph`, the edge
  routes and the grant routes.
- A later issue adds the webhook endpoint Google and Microsoft Graph push change notifications to.

## Rules in graph.go

The file's own doc comment writes them out.

- Every handler reaches the store only through `inUserScope`. Row level security, not a
  conditional, bounds a read.
- A refusal for a row that does not exist and one for a row the caller may not see are the
  identical answer.
- `internal/sync` stays the only package that decides what a detail level means, through
  `MaskFor`, `CheckGrants` and `CheckLoop`.

## The mirror

- `Deps.Mirror` is required. `NewHandler` refuses a nil one.
- A handler never calls a provider inside `Store.WithUser`. An edge into a calendar calbridge
  creates takes two scopes with `Mirror.CreateCalendar` between them.
- The calendar row is written once, with the provider's own id. The application role has no
  `UPDATE` on `calendars`.
- A failed second scope discards the provider calendar again.
- Deleting an edge runs the teardown before the row goes. Revoking a grant queues the teardown of
  the edges it covered.
- A handler never logs or returns a raw mirror error. The framework logs what a handler returns, so
  the error names the step and `provider.Reason` and drops the provider's text.
- A refused credential answers `502 credential_refused` and shows as `credential_refused` on the
  edge in `GET /api/v1/graph`.

## Public site link

- `newSPAHandler` substitutes `config.publicSiteUrl` into the served `index.html` in place of a
  placeholder.
- It refuses a build whose `index.html` carries no placeholder. See
  `cmd/calbridge-landing/AGENTS.md` for the landing side of the same mechanism.
- The value is escaped for the attribute it lands in.
- `PUBLIC_SITE_URL` and `APP_URL` are checked to the same strictness, credentials in the URL
  included.

## Closed registration

- `Deps.RegistrationClosed` closes both ways in, `POST /api/v1/auth/signup` and a first sign-in
  through a provider.
- A refusal is a 403 with the code `permission_denied`.
- Both paths build it through `registrationClosedError`.
- `GET /api/v1/auth/providers` carries `registration_open`, so the login screen can say so.
- An existing account still signs in.

## Rate limits

- There are two budgets, not one.
- The routes a request reaches with no session carry one.
- `POST /api/v1/grants` carries its own, so an ordinary share cannot spend what a login needs.
