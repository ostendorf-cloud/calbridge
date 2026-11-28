# internal/config

`internal/config` loads calbridge's configuration from the environment once, at startup, through
govue's `envconf`.

- The database connection, the encryption key and each sign-in provider's client secret follow
  `envconf`'s `SecretOrFile` and `RequiredSecretOrFile` `_FILE` suffix convention.
- The rest is server, logging, session, rate limit and provider sign-in configuration.
- The two rate limit budgets are described in `internal/httpapi/AGENTS.md`.
- `REGISTRATION_CLOSED` is a boolean and defaults to false, so registration is open. See
  `internal/auth/AGENTS.md`.
