# Legal review

- The landing server's imprint, privacy policy and terms pages are a draft written from the code.
- They are not legal advice.
- Issues #44 and #66 require a person to review them before the site is published.
- The task list does not automate that review.

## Texts that need the owner's review

- The terms of use in `web-landing/src/i18n/locales/{de,en}.json` under `terms`. The text is new.
  It has no dispute resolution section and no jurisdiction clause.
- The liability clause of the terms. It states the statutory minimum and nothing else.
- The privacy sections on calendar access, refresh tokens, retention, revocation and cookies.
  They describe calendar permissions that no connection requests today.
- The Google Limited Use statement under `privacy.googleLimitedUse`. Google checks it during
  verification. The promise that no person reads the data needs the owner's confirmation.
- The retention text. It sets no period beyond the life of the account.
- The revocation text under `privacy.revocation`. It says no calendar account can be connected
  yet. Only `DELETE /api/v1/me` removes stored credentials once one can. `DELETE /api/v1/edges/{id}`
  deletes the edge row only and leaves the credentials of the calendar account. No route deletes
  a single calendar account. Reword the text when one exists.
- The hosting section under `privacy.hosting`. It names no provider and states that a proxy or
  ingress log can hold the IP address of a request.

## Decision on the terms

- The owner decided on 2026-11-28 that every site that becomes a product has terms.
- `Terms` stays on in govue's `language.HandleLegal`, so `/terms` and `/agb` are both served.
- This replaces the line of issue #66 that `/agb` is not used.
- The pricing page says nothing is sold today. The terms are therefore the terms of use of the
  service. They carry no payment term, no dispute resolution section and no jurisdiction clause.
- Add a payment section to the terms before a price is introduced.
- The owner reviews the terms text in `web-landing/src/i18n/locales/{de,en}.json` under `terms`.
- This document and the pull request record the decision of 2026-11-28.
- The decision covers terms for every product, the `/agb` route and `Terms` staying on.

## Facts the repository does not hold

- The names of the Google and Microsoft calendar scopes. They are not stated on the privacy page
  until the connection that requests them exists.
- The operator data. The deployment supplies it through `OPERATOR_*`, see
  `cmd/calbridge-landing/AGENTS.md`. The Helm chart sets it under `landing.config.operator`.
- The hosting provider of the landing server and the application. The privacy page names none.
  The owner supplies the provider, and the privacy text then names it.
- The access logs of the ingress or proxy in front of the landing server. The landing server's
  own access log records no client address, so only that proxy log can hold the IP address of a
  request. The owner supplies whether it logs the address, for how long, and who can read it.
  The privacy text must then state the retention.
