# web-landing

`web-landing/` is the landing server's own Vue SPA on govue.

- It is a separate package from `web/`, with its own `pnpm-lock.yaml`.
- Routes: the home page (the problem and the three detail levels), pricing (privately operated,
  no prices yet), the imprint and the privacy policy.
- German and English i18n, like `web/`.
- The same generated theme, kept as its own copy under `web-landing/src/theme/` rather than an
  import across the two packages.
- `web-landing/eslint.config.ts` refuses govue's `createClient` and any import reaching into
  `web/`.
- The app link and the server side live in `cmd/calbridge-landing/AGENTS.md`.
