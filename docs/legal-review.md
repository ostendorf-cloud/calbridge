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

## Facts the repository does not hold

- The names of the Google and Microsoft calendar scopes. They are not stated on the privacy page
  until the connection that requests them exists.
- The operator data. The deployment supplies it through `OPERATOR_*`, see
  `cmd/calbridge-landing/AGENTS.md`.
