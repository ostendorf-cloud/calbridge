# web

`web/` holds the Vue SPA on govue.

- A login shell and an authenticated shell prove the session and CSRF contract end to end.
- German and English i18n.
- The theme is generated from `web/src/theme/seed.json` through govue's own generator.

## Login

- `LoginView` carries sign-in with both factors, and sign-up with its TOTP enrolment.
- It shows a button per configured sign-in provider, with Apple marked as granting identity
  only.
- `LoginView` reads `registration_open` from the provider list. A closed instance shows a notice
  in place of the sign-up switch. The backend refuses a closed sign-up on its own.
- `LoginView` and the authenticated shell both carry a "back to the public site" link.
- It reads `config.publicSiteUrl` and renders no link where that is empty. See
  `internal/httpapi/AGENTS.md`.

## Canvas

- The Vue Flow canvas draws calendars as nodes grouped by account.
- The calendars shared with this account are nodes of their own.
- An edge is drawn by dragging one node onto another. The detail level is edited on the edge.
- Every action on the canvas is reachable without a pointer drag.
- That is not a refinement. An edge that can only be created by dragging cannot be created from
  a keyboard, and drawing edges is the product.

## Grants

- The grant screen is the ledger of what was shared out and in, and where a share is revoked.
