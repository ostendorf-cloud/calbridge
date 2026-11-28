# internal/storage

`internal/storage` defines the domain types and the repository interfaces the layers above call.
Nothing here imports a provider SDK or generated sqlc code directly.

## internal/storage/postgres

- The only implementation. It wires sqlc-generated queries into the repository interfaces.
- `queries/` holds the SQL. `sqlcgen/` holds the committed generated code.
- The split matches solobill's `internal/storage` and `internal/storage/postgres`.
- Every user-owned table is reached only through `Store.WithUser`. It scopes a transaction to
  one user via a transaction-local Postgres setting.
- Row level security, not this package, is what actually enforces that scope.
- `crypto.go` encrypts and decrypts `connections.credentials` and `event_mapping.encrypted_details`
  at the database boundary. A caller above this package never holds or passes ciphertext.

## SECURITY DEFINER lookups

- Lookups that cannot be reached under the asking user's own scope go through a
  `SECURITY DEFINER` function, not a widened policy.
- They are: a local account by email, a sign-in identity by `(provider, subject)`, and the
  addresses on the other side of a grant (`grant_counterparties`), and the ids of the users who
  own an edge (`edge_owners`), which the scheduled reconcile walks.
- Each is revoked from `PUBLIC` in `db/migrations`. The explicit grant to the application role
  decides who may call it.
- Each nulls in its own body whatever its callers have no business with.
- `grant_counterparties` returns `SETOF users`, the only shape sqlc can resolve. It returns no
  password hash and no TOTP secret in it.

## Sharing

- `sharing.go` is the one file holding everything that reaches across from one user to another.
- That is the calendars a grant shares with this user, the people a grant names, and the
  address-to-id lookup that creates one.
- A calendar read across a grant comes back as `storage.SharedCalendar`.
- It deliberately does not embed `storage.Calendar`. The policy admits the owner's
  `external_id`, the provider's own identifier, and nothing above the database is to have a
  field to put it in.

## storagetest

- `internal/storage/storagetest` is an in-memory implementation of the same repository
  interfaces, for a test of a package above the storage layer.
- It imitates the scoping row level security enforces. A repository method called outside a
  `WithUser` transaction fails there too.
