# cmd/calbridge-landing

`cmd/calbridge-landing` is the public landing server on `calbridge.de`, issue #44.

- `internal/landingconfig` loads its own, much smaller configuration: no database, no session,
  no sign-in provider.
- `internal/landingapi` builds its handler.
- It is served with govue's `httpserver` through the same two-listener shape as `cmd/calbridge`.
  The public listener serves the site. The admin listener serves liveness, readiness and metrics.
- It is a static build with no session, no CSRF protector and no database connection at all.
- So a correction to the privacy policy's text never redeploys the application.
- The frontend is `web-landing/`. See `web-landing/AGENTS.md`.

## App link

- Every outbound link to the application reads `internal/landingconfig.Config.AppURL`.
- `internal/landingapi`'s `newStaticHandler` substitutes it into the served `index.html` in
  place of a placeholder.
- It is the same mechanism `internal/httpapi`'s `newSPAHandler` uses for `config.publicSiteUrl`.
- Neither reads a host fixed at image build time.
- Both substitute the value escaped for the attribute it lands in.
- Both refuse a build whose `index.html` carries no placeholder. A dropped meta tag would
  otherwise remove the link from every deployment silently.
- `PUBLIC_SITE_URL` and `APP_URL` are checked to the same strictness on both sides, credentials
  in the URL included.

## Image

- `Dockerfile.landing` builds this binary's own image, separate from the application's
  `Dockerfile`, so the two are released independently.
- Its `COPY` list names only the two landing packages. That is a guard as much as a copy.
- The import and bundle guards live in `internal/depsync/AGENTS.md`.
- The privacy policy and imprint are a draft. See `docs/legal-review.md`.
