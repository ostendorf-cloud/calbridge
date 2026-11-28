# web-landing

`web-landing/` is the landing server's own Vue SPA on govue.

- It is a separate package from `web/`, with its own `pnpm-lock.yaml`.
- Routes: the home page (the problem and the three detail levels), pricing (privately operated,
  no prices yet), and the imprint, the privacy policy and the terms under `/de/` and `/en/`.
  govue's `legalRoutes` builds those. Any other path shows the not found view.
- The operator in the imprint and the privacy policy comes from `src/operator.ts`, which reads
  the `calbridge-operator` meta tag the server fills. See `cmd/calbridge-landing/AGENTS.md`.
- The page texts are site content in `src/i18n/locales/`. The owner reviews them, see
  `docs/legal-review.md`.
- German and English i18n, like `web/`, through govue's language layer. The choice lives in the
  `govue-language` cookie, which the server reads too.
- The same generated theme, kept as its own copy under `web-landing/src/theme/` rather than an
  import across the two packages.
- `web-landing/eslint.config.ts` refuses govue's `createClient` and any import reaching into
  `web/`.
- The app link and the server side live in `cmd/calbridge-landing/AGENTS.md`.
