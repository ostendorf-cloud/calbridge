# internal/auth

`internal/auth` holds login.

- Local accounts: a password hashed with Argon2id and a TOTP second factor.
- Sign in with Google, Microsoft and Apple through govue's `oidc` package, asking for identity
  scopes only.
- calbridge owns identity and depends on no external identity provider.
- Both paths produce one `Identity`, so nothing above branches on login method.

## Provider identity versus calendar account

- A provider identity used to sign in is never matched against a provider account another user
  connected as a calendar. That collision creates a separate account.
- The rule is structural. The `Store` interface this package defines exposes no connection
  repository, so no code here can reach one.

## Argon2id concurrency

- How many Argon2id derivations may run at once is capped here, against the chart's own memory
  limit.
- `internal/depsync` fails if the two stop agreeing.
