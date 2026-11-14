-- users carries row level security scoped to a row's own id (see
-- db/migrations). GetUserByEmail is the only query here that runs
-- against the pool directly rather than inside a WithUser transaction:
-- the email lookup has to work before any user id is known at all, which
-- is exactly what its users_find_by_email call, a SECURITY DEFINER
-- function, is for. Every other query here, CreateUser included, runs
-- inside a WithUser transaction like every other repository.

-- CreateUser takes the new user's id as a parameter instead of leaving
-- it to the column's DEFAULT: signup scopes its own transaction to the
-- user it is about to create, so the id has to exist before the INSERT
-- does. RETURNING reads the new row back, and a row read back has to
-- satisfy the table's SELECT policy too, which is what that scope
-- satisfies; see db/migrations.
-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, totp_secret, totp_last_counter, status)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT *
FROM users_find_by_email(sqlc.arg(email));

-- GetUserIDByEmail resolves the address a grant names into the user id
-- the grant row stores. It goes through the same users_find_by_email
-- the login lookup does, rather than through a second SECURITY DEFINER
-- function: the question is the same question, and the projection to id
-- alone means password_hash and totp_secret stay inside the database
-- rather than travelling to Go for a grant to be created.
--
-- That an unknown address can be told apart from a known one here is
-- deliberate and discloses nothing new: signup already answers "an
-- account already exists for this email address" (see internal/httpapi's
-- signUpError), so whether an address has an account is already known to
-- anyone who can reach the signup route. Closing it here and leaving it
-- open there would buy nothing and would leave a person unable to tell a
-- mistyped address from a successful share.
-- name: GetUserIDByEmail :one
SELECT id
FROM users_find_by_email(sqlc.arg(email));

-- AdvanceUserTOTPCounter is what a successful login writes, and the only
-- thing it writes. It is deliberately not UpdateUser below: a login
-- holds a snapshot of the row taken before a roughly 0.3 second Argon2id
-- verify, and writing password_hash, totp_secret and status back from
-- that snapshot would undo whatever changed them in the meantime.
--
-- The WHERE clause is the replay check itself, not an optimisation. RFC
-- 6238 section 5.2 requires an already validated one-time password to be
-- refused on a second use, and checking the counter in Go before writing
-- cannot enforce that: two logins racing with the same captured code
-- read the same value on two connections and both pass. Here the
-- database compares against whatever the row holds at the moment of the
-- write, so exactly one of them changes a row and the other is told it
-- changed none.
-- name: AdvanceUserTOTPCounter :execrows
UPDATE users
SET totp_last_counter = $2,
    updated_at = now()
WHERE id = $1
  AND (totp_last_counter IS NULL OR totp_last_counter < $2);

-- name: UpdateUser :one
UPDATE users
SET password_hash = $2,
    totp_secret = $3,
    totp_last_counter = $4,
    status = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;
