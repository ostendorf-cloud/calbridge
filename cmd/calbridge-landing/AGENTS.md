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

## Operator

- The imprint and the privacy policy name a real operator. The repository holds none of it.
- `internal/landingconfig` reads `OPERATOR_NAME`, `OPERATOR_STREET`, `OPERATOR_POSTAL_CODE`,
  `OPERATOR_CITY` and `OPERATOR_EMAIL`. They are required.
- It reads `OPERATOR_COUNTRY`, `OPERATOR_PHONE`, `OPERATOR_REPRESENTATIVE`, `OPERATOR_REGISTER`,
  `OPERATOR_VAT_ID`, `OPERATOR_CONTENT_RESPONSIBLE` and `OPERATOR_DATA_PROTECTION`. They are
  optional, and an empty one leaves its line out of the pages.
- Startup refuses a missing, bracketed, `todo`-like, over-long or control-character value.
- `newStaticHandler` substitutes the operator as JSON, escaped for the attribute, into the
  `calbridge-operator` meta tag of `index.html`. `web-landing/src/operator.ts` reads it back.
- It refuses a build whose `index.html` lacks either placeholder.

## Routes

- `language.HandleLegal` from govue answers the short paths `/imprint`, `/privacy`, `/terms` and
  the German aliases with a 302 to `/de/...` or `/en/...`. The order is cookie, then
  `Accept-Language`, then English.
- The pages are `/`, `/pricing` and `/{de,en}/{imprint,privacy,terms}`. They answer 200 with
  the shell.
- Any other path answers 404 with the same shell, so the client router shows the not found
  view. A missing `/assets/*` file answers a plain 404.
- `spaPages` in `internal/landingapi/static.go` is the list. `web-landing/src/router/index.test.ts`
  pins the matching list on the client.

## Image

- `Dockerfile.landing` builds this binary's own image, separate from the application's
  `Dockerfile`, so the two are released independently.
- Its `COPY` list names only the two landing packages. That is a guard as much as a copy.
- The import and bundle guards live in `internal/depsync/AGENTS.md`.
- The privacy policy and imprint are a draft. See `docs/legal-review.md`.
