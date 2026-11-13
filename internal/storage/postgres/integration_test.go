// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// This file needs a real, reachable Postgres and is excluded from a plain
// "go test ./..." by the build tag above; "task test:integration" is the
// only Taskfile target that passes "-tags=integration". DATABASE_URL still
// gets checked at runtime too, and t.Skip()s without it, so running this
// binary by hand with the tag but no database gives a clear skip instead
// of a confusing dial failure.
package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres"
)

// testAppRole is a role this test creates on the target Postgres if it is
// not already there, and then runs every assertion below through. It is
// deliberately NOSUPERUSER and NOBYPASSRLS: a Postgres superuser bypasses
// row level security regardless of FORCE ROW LEVEL SECURITY, so running
// these assertions as the admin connection from DATABASE_URL (ordinarily
// the postgres image's default superuser, which CI also uses to run the
// migrations beforehand) would prove nothing about row level security at
// all. testAppPassword and testEncryptionKey are fixed, obviously-fake
// values that only ever reach a disposable test Postgres (a CI service
// container, or a developer's own local one); neither is a credential
// for anything real.
const (
	testAppRole     = "calbridge_test_app"
	testAppPassword = "calbridge-test-only-not-a-real-secret"
)

// testEncryptionKey is 32 bytes, matching the AES-256 key length
// postgres.NewStore requires; its content is arbitrary and never used
// outside this test binary.
var testEncryptionKey = []byte("01234567890123456789012345678901"[:32])

// connectionRawRow mirrors the connections table's own column order and
// names, used only to decode a literal, deliberately unscoped "SELECT *
// FROM connections" below: that query has to stay exactly that, with no
// column list and no WHERE clause, to prove what it is meant to prove,
// so the test needs a destination shaped like the whole table rather
// than like one of its own queries.
type connectionRawRow struct {
	ID          uuid.UUID `db:"id"`
	UserID      uuid.UUID `db:"user_id"`
	Provider    string    `db:"provider"`
	DisplayName string    `db:"display_name"`
	Credentials []byte    `db:"credentials"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// requireDatabaseURL returns DATABASE_URL, or stops the test: t.Skip when
// CI is unset, the local-development case where no Postgres is expected
// to be reachable, and t.Fatal when CI is set, so a job whose own env:
// block lost DATABASE_URL reports a failure instead of reporting green
// having proven nothing. GitHub Actions sets CI=true on every runner by
// default, independent of checks.yaml's own "integration" job env: block,
// which is what actually carries DATABASE_URL.
func requireDatabaseURL(t *testing.T) string {
	t.Helper()

	adminURL := os.Getenv("DATABASE_URL")
	if adminURL != "" {
		return adminURL
	}

	if os.Getenv("CI") != "" {
		t.Fatal("DATABASE_URL not set while CI is set; the integration job lost its own env: block")
	}

	t.Skip("DATABASE_URL not set; skipping the Postgres-backed test")

	return ""
}

// TestRowLevelSecurity is the single most important test in this
// milestone: it proves that Postgres row level security, not application
// code, is what actually keeps one user's rows out of another user's
// reach, including when a query does not even try to scope itself.
func TestRowLevelSecurity(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	restrictedURL := setUpTestAppRole(ctx, t, adminPool, adminURL)

	restrictedPool, err := postgres.NewPool(ctx, restrictedURL)
	if err != nil {
		t.Fatalf("open restricted pool as %s: %v", testAppRole, err)
	}
	t.Cleanup(restrictedPool.Close)

	store, err := postgres.NewStore(restrictedPool, testEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	userA, err := store.Users().Create(ctx, storage.User{Email: "a-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}

	userB, err := store.Users().Create(ctx, storage.User{Email: "b-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}

	var connectionA, connectionB storage.Connection

	if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
		var err error
		connectionA, err = store.Connections().Create(ctx, storage.Connection{
			UserID:      userA.ID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: "A's Google account",
			Credentials: []byte("user-a-refresh-token"),
		})

		return err
	}); err != nil {
		t.Fatalf("create connection under user A: %v", err)
	}

	if err := store.WithUser(ctx, userB.ID, func(ctx context.Context) error {
		var err error
		connectionB, err = store.Connections().Create(ctx, storage.Connection{
			UserID:      userB.ID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: "B's Google account",
			Credentials: []byte("user-b-refresh-token"),
		})

		return err
	}); err != nil {
		t.Fatalf("create connection under user B: %v", err)
	}

	t.Run("user A sees only its own connection through the repository", func(t *testing.T) {
		var connections []storage.Connection

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			var err error
			connections, err = store.Connections().List(ctx)

			return err
		}); err != nil {
			t.Fatalf("list connections as user A: %v", err)
		}

		assertOnlyConnection(t, connections, connectionA.ID)
	})

	t.Run("user B sees only its own connection through the repository", func(t *testing.T) {
		var connections []storage.Connection

		if err := store.WithUser(ctx, userB.ID, func(ctx context.Context) error {
			var err error
			connections, err = store.Connections().List(ctx)

			return err
		}); err != nil {
			t.Fatalf("list connections as user B: %v", err)
		}

		assertOnlyConnection(t, connections, connectionB.ID)
	})

	t.Run("credentials round-trip decrypted and never touch the database in clear", func(t *testing.T) {
		var raw []byte

		if err := adminPool.QueryRow(ctx, `SELECT credentials FROM connections WHERE id = $1`, connectionA.ID).Scan(&raw); err != nil {
			t.Fatalf("read raw credentials as admin: %v", err)
		}

		if string(raw) == "user-a-refresh-token" {
			t.Fatal("connections.credentials holds the refresh token in clear, want it encrypted at rest")
		}

		var found storage.Connection

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			var err error
			found, err = store.Connections().FindByID(ctx, connectionA.ID)

			return err
		}); err != nil {
			t.Fatalf("find connection as user A: %v", err)
		}

		if string(found.Credentials) != "user-a-refresh-token" {
			t.Errorf("decrypted credentials = %q, want %q", found.Credentials, "user-a-refresh-token")
		}
	})

	t.Run("an unscoped SELECT star on a transaction scoped to user B still sees only user B", func(t *testing.T) {
		tx, err := restrictedPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userB.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		rows, err := tx.Query(ctx, `SELECT * FROM connections`)
		if err != nil {
			t.Fatalf("SELECT * FROM connections: %v", err)
		}

		ids := connectionIDs(t, rows)

		if len(ids) != 1 || ids[0] != connectionB.ID {
			t.Fatalf("SELECT * FROM connections while scoped to user B = %v, want exactly [%v]", ids, connectionB.ID)
		}
	})

	t.Run("no user set at all returns zero rows, not every row and not an error", func(t *testing.T) {
		tx, err := restrictedPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		// Deliberately no set_config call on this transaction at all: this
		// is the "a connection never scoped by WithUser" case.

		rows, err := tx.Query(ctx, `SELECT * FROM connections`)
		if err != nil {
			t.Fatalf("SELECT * FROM connections with no user set: %v", err)
		}

		ids := connectionIDs(t, rows)

		if len(ids) != 0 {
			t.Fatalf("SELECT * FROM connections with no user set = %v, want none", ids)
		}
	})

	t.Run("a user-scoped transaction that commits, then an unscoped transaction on the same connection, still sees zero rows", func(t *testing.T) {
		// This is the case that actually failed against a real Postgres in
		// solobill's own equivalent test: a physical connection, not a
		// fresh one, held onto across two transactions via a single
		// pool.Acquire, exactly what a connection pool hands an unrelated
		// later request once it gives this connection back. set_config(...,
		// true) only reverts its *value* at COMMIT; the custom parameter
		// it created keeps existing for the rest of the session, as an
		// empty string, not as "unset". So the second, deliberately
		// unscoped transaction below must still see zero rows and no
		// error, not every row and not a cast failure on "".
		conn, err := restrictedPool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		defer conn.Release()

		scoped, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin scoped transaction: %v", err)
		}

		if _, err := scoped.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userB.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		if _, err := scoped.Exec(ctx, `SELECT 1`); err != nil {
			t.Fatalf("exercise the scoped transaction: %v", err)
		}

		if err := scoped.Commit(ctx); err != nil {
			t.Fatalf("commit scoped transaction: %v", err)
		}

		unscoped, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin unscoped transaction on the same connection: %v", err)
		}
		defer func() { _ = unscoped.Rollback(ctx) }()

		// Deliberately no set_config call on this second transaction: the
		// connection already carries app.user_id from the one above.

		rows, err := unscoped.Query(ctx, `SELECT * FROM connections`)
		if err != nil {
			t.Fatalf("SELECT * FROM connections on a reused connection with no user set: %v", err)
		}

		ids := connectionIDs(t, rows)

		if len(ids) != 0 {
			t.Fatalf("SELECT * FROM connections on a reused connection with no user set = %v, want none", ids)
		}
	})

	t.Run("the admin connection, a superuser that bypasses row level security, still sees both connections", func(t *testing.T) {
		rows, err := adminPool.Query(ctx, `SELECT * FROM connections WHERE id = ANY($1)`, []uuid.UUID{connectionA.ID, connectionB.ID})
		if err != nil {
			t.Fatalf("admin SELECT: %v", err)
		}

		ids := connectionIDs(t, rows)

		if len(ids) != 2 {
			t.Fatalf("admin SELECT found %d of the 2 connections, want 2: "+
				"either the inserts above did not actually happen, or row level security "+
				"is unexpectedly also filtering a superuser connection", len(ids))
		}
	})

	t.Run("an INSERT scoped to user B with user_id set to user A is rejected", func(t *testing.T) {
		// The WITH CHECK half of the policy, exercised on INSERT: a new
		// row whose user_id does not match the scope the transaction was
		// set_config'd to must be refused, not silently written under
		// the wrong user.
		tx, err := restrictedPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userB.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO connections (user_id, provider, credentials) VALUES ($1, 'google', 'x')`,
			userA.ID)
		if err == nil {
			t.Fatal("INSERT with user_id = user A while scoped to user B succeeded, want it rejected by WITH CHECK")
		}
	})

	t.Run("an UPDATE scoped to user A moving a row's user_id to user B either keeps the original user or is rejected", func(t *testing.T) {
		// The WITH CHECK half of the policy, exercised on UPDATE this
		// time: the new row version (user_id = user B) is checked
		// against the scope the transaction is set_config'd to (user A),
		// so the row can never actually move to another user.
		tx, err := restrictedPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userA.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		var gotUserID uuid.UUID

		err = tx.QueryRow(ctx,
			`UPDATE connections SET user_id = $1 WHERE id = $2 RETURNING user_id`,
			userB.ID, connectionA.ID,
		).Scan(&gotUserID)
		if err != nil {
			// Rejected outright: WITH CHECK doing its job, the preferred
			// and expected outcome.
			return
		}

		if gotUserID != userA.ID {
			t.Fatalf("UPDATE moved connection A's user_id to %v, want it to stay %v or be rejected entirely", gotUserID, userA.ID)
		}
	})

	t.Run("FindByID for another user's connection id returns not found, not the real row", func(t *testing.T) {
		err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			_, err := store.Connections().FindByID(ctx, connectionB.ID)

			return err
		})
		if err == nil {
			t.Fatal("FindByID(connectionB.ID) while scoped to user A succeeded, want storage.ErrNotFound")
		}
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("FindByID(connectionB.ID) while scoped to user A: err = %v, want errors.Is(err, storage.ErrNotFound)", err)
		}
	})

	t.Run("an unscoped SELECT on users returns zero rows", func(t *testing.T) {
		// The control first: as a superuser, which bypasses row level
		// security, both rows are unambiguously there. Without it, the
		// restricted role seeing nothing below would prove only that
		// nothing had been written.
		requireUsersExist(ctx, t, adminPool, userA.ID, userB.ID)

		ids := visibleUserIDs(ctx, t, restrictedPool, uuid.Nil)

		if len(ids) != 0 {
			t.Fatalf("SELECT id FROM users with no user set = %v, want none: "+
				"users_self_isolation should hide every row with no scope set", ids)
		}
	})

	t.Run("signup needs no user scope of its own and the row it creates is still policied", func(t *testing.T) {
		// The regression this subtest exists for. Create runs with no
		// app.user_id set by its caller, and its INSERT carries a
		// RETURNING clause, which is a read: the new row has to satisfy
		// the table's SELECT policy as well as its INSERT WITH CHECK, and
		// a FOR INSERT policy takes no part in that SELECT check. Create
		// therefore generates the new id and scopes its own transaction
		// to it. Everything below asserts that this scoping is real
		// rather than an exemption.
		email := "signup-" + uuid.NewString() + "@example.com"

		created, err := store.Users().Create(ctx, storage.User{Email: email, Status: storage.UserStatusActive})
		if err != nil {
			t.Fatalf("signup with no user scope set: %v", err)
		}

		if created.ID == uuid.Nil {
			t.Fatal("signup returned the zero id, so INSERT ... RETURNING never read the new row back")
		}

		requireUsersExist(ctx, t, adminPool, created.ID)

		t.Run("the new row stays invisible to the restricted role with no scope set", func(t *testing.T) {
			ids := visibleUserIDs(ctx, t, restrictedPool, uuid.Nil)

			if len(ids) != 0 {
				t.Fatalf("SELECT id FROM users with no user set = %v, want none: signup must not have opened the table", ids)
			}
		})

		t.Run("the new row is visible to the restricted role scoped to the new user itself", func(t *testing.T) {
			ids := visibleUserIDs(ctx, t, restrictedPool, created.ID)

			if len(ids) != 1 || ids[0] != created.ID {
				t.Fatalf("SELECT id FROM users scoped to the new user = %v, want exactly [%v]: "+
					"users_self_isolation must scope by the row's own id, not refuse or admit everything", ids, created.ID)
			}
		})

		t.Run("an INSERT naming an id other than the scope is refused by the policy itself", func(t *testing.T) {
			// What stops signup's scoping from becoming a way to write an
			// arbitrary row: the id the transaction is scoped to is the
			// only id it may insert. A permissive FOR INSERT policy beside
			// users_self_isolation would let this through, so this subtest
			// is what keeps one from being reintroduced.
			tx, err := restrictedPool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, created.ID.String()); err != nil {
				t.Fatalf("set_config: %v", err)
			}

			_, err = tx.Exec(ctx,
				`INSERT INTO users (id, email, status) VALUES ($1, $2, 'active')`,
				uuid.New(), "smuggled-"+uuid.NewString()+"@example.com")

			requirePgError(t, err, pgInsufficientPrivilege, `new row violates row-level security policy`)
		})
	})

	t.Run("FindByEmail still resolves a user with no user scope set, through users_find_by_email", func(t *testing.T) {
		found, err := store.Users().FindByEmail(ctx, userA.Email)
		if err != nil {
			t.Fatalf("FindByEmail(%q) with no user scope set: %v", userA.Email, err)
		}

		if found.ID != userA.ID {
			t.Fatalf("FindByEmail(%q) = user %v, want %v", userA.Email, found.ID, userA.ID)
		}
	})

	t.Run("user A cannot read or update user B's row by id", func(t *testing.T) {
		err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			_, err := store.Users().FindByID(ctx, userB.ID)

			return err
		})
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("FindByID(userB.ID) while scoped to user A: err = %v, want errors.Is(err, storage.ErrNotFound)", err)
		}

		err = store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			_, err := store.Users().Update(ctx, storage.User{ID: userB.ID, Status: storage.UserStatusDisabled})

			return err
		})
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Update(userB.ID) while scoped to user A: err = %v, want errors.Is(err, storage.ErrNotFound)", err)
		}

		// Both assertions above run through the repository, whose own
		// queries carry "WHERE id = $1". They would pass unchanged against
		// a policy quietly rewritten to USING (true): the query's own
		// predicate, not the policy, would be doing the work. The two
		// below are what actually name the policy as the mechanism. The
		// SELECT carries no predicate at all, so with several users in the
		// table it returns exactly what row level security admits; the
		// UPDATE names user B explicitly and must still change nothing,
		// checked against the superuser's own view of the row.
		requireUsersExist(ctx, t, adminPool, userA.ID, userB.ID)

		ids := visibleUserIDs(ctx, t, restrictedPool, userA.ID)

		if len(ids) != 1 || ids[0] != userA.ID {
			t.Fatalf("SELECT id FROM users scoped to user A = %v, want exactly [%v]", ids, userA.ID)
		}

		tx, err := restrictedPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userA.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		tag, err := tx.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, userB.ID)
		if err != nil {
			t.Fatalf("UPDATE users WHERE id = user B while scoped to user A: %v", err)
		}

		if tag.RowsAffected() != 0 {
			t.Fatalf("UPDATE on user B's row while scoped to user A touched %d row(s), want 0", tag.RowsAffected())
		}

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}

		var status string
		if err := adminPool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, userB.ID).Scan(&status); err != nil {
			t.Fatalf("admin read of user B's status: %v", err)
		}

		if status != string(storage.UserStatusActive) {
			t.Fatalf("user B's status = %q after user A tried to disable it, want %q",
				status, storage.UserStatusActive)
		}
	})

	t.Run("a calendar created under user A is invisible while scoped to user B", func(t *testing.T) {
		var calendar storage.Calendar

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			created, err := store.Calendars().Create(ctx, storage.Calendar{
				UserID:       userA.ID,
				ConnectionID: connectionA.ID,
				ExternalID:   "external-" + uuid.NewString(),
				Name:         "Privat",
			})
			if err != nil {
				return err
			}

			calendar = created

			return nil
		}); err != nil {
			t.Fatalf("create calendar under user A: %v", err)
		}

		err := store.WithUser(ctx, userB.ID, func(ctx context.Context) error {
			_, err := store.Calendars().FindByID(ctx, calendar.ID)

			return err
		})
		if err == nil {
			t.Fatal("FindByID found user A's calendar while scoped to user B, want storage.ErrNotFound")
		}
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("err = %v, want errors.Is(err, storage.ErrNotFound)", err)
		}
	})

	t.Run("an edge created under user A is invisible while scoped to user B", func(t *testing.T) {
		var edge storage.Edge

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			source, err := store.Calendars().Create(ctx, storage.Calendar{
				UserID: userA.ID, ConnectionID: connectionA.ID,
				ExternalID: "ext-src-" + uuid.NewString(), Name: "Source",
			})
			if err != nil {
				return err
			}

			target, err := store.Calendars().Create(ctx, storage.Calendar{
				UserID: userA.ID, ConnectionID: connectionA.ID,
				ExternalID: "ext-dst-" + uuid.NewString(), Name: "Target", Managed: true,
			})
			if err != nil {
				return err
			}

			created, err := store.Edges().Create(ctx, storage.Edge{
				UserID: userA.ID, SourceCalendarID: source.ID, TargetCalendarID: target.ID,
				DetailLevel: storage.DetailLevelBusyBlock,
			})
			if err != nil {
				return err
			}

			edge = created

			return nil
		}); err != nil {
			t.Fatalf("create edge under user A: %v", err)
		}

		err := store.WithUser(ctx, userB.ID, func(ctx context.Context) error {
			_, err := store.Edges().FindByID(ctx, edge.ID)

			return err
		})
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("FindByID(edge.ID) while scoped to user B: err = %v, want errors.Is(err, storage.ErrNotFound)", err)
		}
	})

	t.Run("a calendar grant is visible to its owner and its grantee, and to neither a third user", func(t *testing.T) {
		userC, err := store.Users().Create(ctx, storage.User{Email: "c-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
		if err != nil {
			t.Fatalf("create user C: %v", err)
		}

		var sharedCalendar storage.Calendar

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			created, err := store.Calendars().Create(ctx, storage.Calendar{
				UserID: userA.ID, ConnectionID: connectionA.ID,
				ExternalID: "ext-shared-" + uuid.NewString(), Name: "Shared with B",
			})

			sharedCalendar = created

			return err
		}); err != nil {
			t.Fatalf("create calendar to grant: %v", err)
		}

		var grant storage.CalendarGrant

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			created, err := store.CalendarGrants().Create(ctx, storage.CalendarGrant{
				OwnerUserID: userA.ID, GranteeUserID: userB.ID,
				CalendarID: sharedCalendar.ID, MaxDetailLevel: storage.DetailLevelTitleTime,
			})

			grant = created

			return err
		}); err != nil {
			t.Fatalf("create grant: %v", err)
		}

		if err := store.WithUser(ctx, userA.ID, func(ctx context.Context) error {
			_, err := store.CalendarGrants().FindByID(ctx, grant.ID)

			return err
		}); err != nil {
			t.Fatalf("owner cannot see its own grant: %v", err)
		}

		if err := store.WithUser(ctx, userB.ID, func(ctx context.Context) error {
			_, err := store.CalendarGrants().FindByID(ctx, grant.ID)

			return err
		}); err != nil {
			t.Fatalf("grantee cannot see the grant made to it: %v", err)
		}

		err = store.WithUser(ctx, userC.ID, func(ctx context.Context) error {
			_, err := store.CalendarGrants().FindByID(ctx, grant.ID)

			return err
		})
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("third user saw a grant naming neither as owner nor grantee: err = %v, want storage.ErrNotFound", err)
		}
	})

	t.Run("an edge may reference a calendar its own user does not own only where a matching grant exists", func(t *testing.T) {
		grantor, err := store.Users().Create(ctx, storage.User{Email: "grantor-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
		if err != nil {
			t.Fatalf("create grantor: %v", err)
		}

		grantee, err := store.Users().Create(ctx, storage.User{Email: "grantee-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
		if err != nil {
			t.Fatalf("create grantee: %v", err)
		}

		stranger, err := store.Users().Create(ctx, storage.User{Email: "stranger-" + uuid.NewString() + "@example.com", Status: storage.UserStatusActive})
		if err != nil {
			t.Fatalf("create stranger: %v", err)
		}

		var sharedCalendar storage.Calendar

		if err := store.WithUser(ctx, grantor.ID, func(ctx context.Context) error {
			conn, err := store.Connections().Create(ctx, storage.Connection{
				UserID: grantor.ID, Provider: storage.ConnectionProviderGoogle,
				DisplayName: "grantor's Google account", Credentials: []byte("grantor-refresh-token"),
			})
			if err != nil {
				return err
			}

			created, err := store.Calendars().Create(ctx, storage.Calendar{
				UserID: grantor.ID, ConnectionID: conn.ID,
				ExternalID: "ext-shared-" + uuid.NewString(), Name: "Shared",
			})

			sharedCalendar = created

			return err
		}); err != nil {
			t.Fatalf("set up grantor's calendar: %v", err)
		}

		if err := store.WithUser(ctx, grantor.ID, func(ctx context.Context) error {
			_, err := store.CalendarGrants().Create(ctx, storage.CalendarGrant{
				OwnerUserID: grantor.ID, GranteeUserID: grantee.ID,
				CalendarID: sharedCalendar.ID, MaxDetailLevel: storage.DetailLevelTitleTime,
			})

			return err
		}); err != nil {
			t.Fatalf("grant the shared calendar to the grantee: %v", err)
		}

		ownCalendarFor := func(t *testing.T, userID uuid.UUID) storage.Calendar {
			t.Helper()

			var calendar storage.Calendar

			if err := store.WithUser(ctx, userID, func(ctx context.Context) error {
				conn, err := store.Connections().Create(ctx, storage.Connection{
					UserID: userID, Provider: storage.ConnectionProviderGoogle,
					DisplayName: "own Google account", Credentials: []byte("own-refresh-token"),
				})
				if err != nil {
					return err
				}

				created, err := store.Calendars().Create(ctx, storage.Calendar{
					UserID: userID, ConnectionID: conn.ID,
					ExternalID: "ext-own-" + uuid.NewString(), Name: "Own", Managed: true,
				})

				calendar = created

				return err
			}); err != nil {
				t.Fatalf("set up a calendar of userID's own: %v", err)
			}

			return calendar
		}

		t.Run("the grantee builds an edge from the shared calendar into its own", func(t *testing.T) {
			target := ownCalendarFor(t, grantee.ID)

			err := store.WithUser(ctx, grantee.ID, func(ctx context.Context) error {
				_, err := store.Edges().Create(ctx, storage.Edge{
					UserID: grantee.ID, SourceCalendarID: sharedCalendar.ID, TargetCalendarID: target.ID,
					DetailLevel: storage.DetailLevelTitleTime,
				})

				return err
			})
			if err != nil {
				t.Fatalf("grantee building an edge over a calendar actually shared with it: %v", err)
			}
		})

		t.Run("a stranger with no grant cannot build an edge over the same calendar", func(t *testing.T) {
			// "Some error came back" would not prove this: the plain
			// foreign key, the row level security policy on edges and the
			// trigger all refuse a write with an error, and only the
			// trigger refuses this particular one for the right reason.
			// The SQLSTATE and the message are what name the mechanism.
			target := ownCalendarFor(t, stranger.ID)

			err := store.WithUser(ctx, stranger.ID, func(ctx context.Context) error {
				_, err := store.Edges().Create(ctx, storage.Edge{
					UserID: stranger.ID, SourceCalendarID: sharedCalendar.ID, TargetCalendarID: target.ID,
					DetailLevel: storage.DetailLevelTitleTime,
				})

				return err
			})

			requirePgError(t, err, pgRaiseException, edgesSourceCalendarRefusal)
		})

		t.Run("a calendar id that exists under nobody is refused identically, closing the oracle", func(t *testing.T) {
			// The point of the trigger firing before the foreign key: a
			// guessed calendar UUID that exists under another user and one
			// that exists nowhere at all must be indistinguishable from
			// outside. A foreign key violation here (SQLSTATE 23503, a
			// different message) would mean the two cases fail differently
			// and the existence oracle is open again.
			target := ownCalendarFor(t, stranger.ID)

			err := store.WithUser(ctx, stranger.ID, func(ctx context.Context) error {
				_, err := store.Edges().Create(ctx, storage.Edge{
					UserID: stranger.ID, SourceCalendarID: uuid.New(), TargetCalendarID: target.ID,
					DetailLevel: storage.DetailLevelTitleTime,
				})

				return err
			})

			requirePgError(t, err, pgRaiseException, edgesSourceCalendarRefusal)
		})
	})

	t.Run("the table owner is still scoped by row level security because of FORCE", func(t *testing.T) {
		// Every role exercised above is either DATABASE_URL's own role, a
		// superuser that bypasses row level security unconditionally
		// (FORCE included; see the subtest above), or testAppRole,
		// NOSUPERUSER NOBYPASSRLS but not the connections table's owner,
		// so ENABLE alone would already keep it scoped regardless of
		// FORCE. Neither actually exercises what FORCE itself adds:
		// binding the table's own OWNER to the policy too (ENABLE alone
		// exempts the owner). This subtest makes testOwnerRole, which is
		// NOSUPERUSER NOBYPASSRLS, own connections for its duration
		// (restored via t.Cleanup) and proves the policy still applies
		// to it.
		ownerURL := setUpTestOwnerRole(ctx, t, adminPool, adminURL)
		makeTemporaryOwner(ctx, t, adminPool, "connections", testOwnerRole)

		ownerPool, err := postgres.NewPool(ctx, ownerURL)
		if err != nil {
			t.Fatalf("open pool as %s: %v", testOwnerRole, err)
		}
		defer ownerPool.Close()

		tx, err := ownerPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userB.ID.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}

		rows, err := tx.Query(ctx, `SELECT * FROM connections`)
		if err != nil {
			t.Fatalf("SELECT * FROM connections as the table owner, scoped to user B: %v", err)
		}

		ids := connectionIDs(t, rows)

		if len(ids) != 1 || ids[0] != connectionB.ID {
			t.Fatalf("SELECT * FROM connections as the table owner, scoped to user B = %v, want exactly [%v]; "+
				"if every row comes back instead, FORCE ROW LEVEL SECURITY is not actually binding the table owner",
				ids, connectionB.ID)
		}
	})
}

// testOwnerRole and testOwnerPassword name a second disposable test role,
// alongside testAppRole: NOSUPERUSER NOBYPASSRLS like testAppRole, but
// this one is temporarily made the owner of a table (see
// makeTemporaryOwner), which testAppRole never is. testOwnerPassword is a
// fixed, obviously-fake value, the same way testAppPassword is.
const (
	testOwnerRole     = "calbridge_test_owner"
	testOwnerPassword = "calbridge-test-only-not-a-real-secret-owner"
)

// setUpTestOwnerRole creates testOwnerRole if it does not exist yet, the
// same idempotent pattern setUpTestAppRole uses, and returns a connection
// string for it. It grants no table privileges: ownership, which
// makeTemporaryOwner confers separately, already carries every privilege
// on the table it owns.
func setUpTestOwnerRole(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, adminURL string) string {
	t.Helper()

	createRole := `
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '` + testOwnerRole + `') THEN
        CREATE ROLE ` + testOwnerRole + ` LOGIN PASSWORD '` + testOwnerPassword + `' NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;`

	if _, err := adminPool.Exec(ctx, createRole); err != nil {
		t.Fatalf("create %s role: %v", testOwnerRole, err)
	}

	ownerURL, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}

	ownerURL.User = url.UserPassword(testOwnerRole, testOwnerPassword)

	return ownerURL.String()
}

// makeTemporaryOwner makes role the owner of tableName for the rest of
// the calling test, restoring whichever role owned it before via
// t.Cleanup. This is the only way to actually exercise FORCE ROW LEVEL
// SECURITY: it is specifically the clause that binds a table's OWNER to
// its own policy, and nothing else this suite connects as is both
// non-superuser and the table's owner at the same time.
func makeTemporaryOwner(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, tableName, role string) {
	t.Helper()

	var originalOwner string

	if err := adminPool.QueryRow(ctx,
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE relname = $1`, tableName,
	).Scan(&originalOwner); err != nil {
		t.Fatalf("read current owner of %s: %v", tableName, err)
	}

	if _, err := adminPool.Exec(ctx, `ALTER TABLE `+tableName+` OWNER TO `+role); err != nil {
		t.Fatalf("make %s own %s: %v", role, tableName, err)
	}

	t.Cleanup(func() {
		if _, err := adminPool.Exec(context.Background(), `ALTER TABLE `+tableName+` OWNER TO `+originalOwner); err != nil {
			t.Errorf("restore %s as owner of %s: %v", originalOwner, tableName, err)
		}
	})
}

// TestEveryUserScopedTableEnforcesRowLevelSecurity introspects the schema
// itself, rather than naming identities, connections, calendars, edges,
// calendar_grants, event_mapping and users by hand, so it catches a
// future table that references users but forgets
// ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY or CREATE POLICY:
// exactly the three properties the targeted tests above assume rather
// than verify generically. "User scoped" is defined here as "has at
// least one foreign key column referencing users(id)", which catches
// both the plain user_id columns most tables carry and calendar_grants'
// owner_user_id/grantee_user_id pair, rather than assuming every such
// column is literally named user_id. users itself holds no such foreign
// key (a row there IS a user, not something owned by one), so it can
// never be found this way; the query's second branch adds it by name
// instead, which is the only table this introspection ever needs to name
// directly. It runs as DATABASE_URL's own admin/superuser role: querying
// pg_class, pg_policies and information_schema needs no special
// privilege, and this test is about the schema's shape, not about who
// can read which row, so the restricted role TestRowLevelSecurity's own
// tests exercise is beside the point here.
func TestEveryUserScopedTableEnforcesRowLevelSecurity(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	rows, err := adminPool.Query(ctx, `
SELECT relname, relrowsecurity, relforcerowsecurity, has_policy FROM (
    SELECT DISTINCT
        c.relname,
        c.relrowsecurity,
        c.relforcerowsecurity,
        EXISTS (
            SELECT 1 FROM pg_policies p
            WHERE p.schemaname = 'public' AND p.tablename = c.relname
        ) AS has_policy
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN information_schema.table_constraints tc
        ON tc.table_schema = 'public' AND tc.table_name = c.relname AND tc.constraint_type = 'FOREIGN KEY'
    JOIN information_schema.constraint_column_usage ccu
        ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
    WHERE n.nspname = 'public'
      AND c.relkind = 'r'
      AND ccu.table_name = 'users'
      AND c.relname <> 'users'

    UNION ALL

    SELECT
        c.relname,
        c.relrowsecurity,
        c.relforcerowsecurity,
        EXISTS (
            SELECT 1 FROM pg_policies p
            WHERE p.schemaname = 'public' AND p.tablename = c.relname
        ) AS has_policy
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname = 'users'
) t
ORDER BY relname`)
	if err != nil {
		t.Fatalf("introspect user-scoped tables: %v", err)
	}

	type userScopedTable struct {
		Name        string `db:"relname"`
		RowSecurity bool   `db:"relrowsecurity"`
		ForceRLS    bool   `db:"relforcerowsecurity"`
		HasPolicy   bool   `db:"has_policy"`
	}

	tables, err := pgx.CollectRows(rows, pgx.RowToStructByName[userScopedTable])
	if err != nil {
		t.Fatalf("scan user-scoped tables: %v", err)
	}

	if len(tables) == 0 {
		t.Fatal("introspection found no table referencing users at all; " +
			"either the query above is broken, or db/migrations has not actually been applied")
	}

	for _, table := range tables {
		t.Run(table.Name, func(t *testing.T) {
			if !table.RowSecurity {
				t.Errorf("table %q references users but ROW LEVEL SECURITY is not enabled", table.Name)
			}
			if !table.ForceRLS {
				t.Errorf("table %q references users but FORCE ROW LEVEL SECURITY is not set", table.Name)
			}
			if !table.HasPolicy {
				t.Errorf("table %q references users but carries no row level security policy at all", table.Name)
			}
		})
	}
}

// TestIdentitiesNeverResolveThroughConnections proves, at the schema
// level, that identities (a provider identity used to sign in) and
// connections (a provider account connected as a calendar) are genuinely
// separate: no foreign key, in either direction, connects the two
// tables, so no query can follow a column from one straight into the
// other. db/migrations records the reasoning; this test is what actually
// enforces it stays true as the schema evolves. internal/auth, a later
// step, is what will add the lookup that resolves a sign-in identity to
// a user; it is deliberately absent from this milestone (see the issue's
// "no login" scope), so there is no application code to exercise here
// beyond the schema's own shape and the two repositories' own, disjoint
// result types.
func TestIdentitiesNeverResolveThroughConnections(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	var count int

	if err := adminPool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.table_constraints tc
JOIN information_schema.constraint_column_usage ccu
    ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
WHERE tc.table_schema = 'public'
  AND tc.constraint_type = 'FOREIGN KEY'
  AND (
    (tc.table_name = 'identities' AND ccu.table_name = 'connections')
    OR (tc.table_name = 'connections' AND ccu.table_name = 'identities')
  )`).Scan(&count); err != nil {
		t.Fatalf("introspect identities/connections foreign keys: %v", err)
	}

	if count != 0 {
		t.Fatalf("found %d foreign key(s) between identities and connections, want 0: "+
			"a provider identity used to sign in must never be resolvable through a connection, or the reverse", count)
	}

	// The second half of the same rule, now that there is a lookup to
	// check: identities_find_by_provider_subject is the one query that
	// resolves a sign-in before any user scope exists, and it is
	// SECURITY DEFINER, so whatever it reads it reads with no row level
	// security in the way at all. It must therefore read identities and
	// nothing else; a join to connections added here would be invisible
	// to every other test in this file.
	var body string

	if err := adminPool.QueryRow(ctx, `
SELECT prosrc
FROM pg_proc
WHERE proname = 'identities_find_by_provider_subject'`).Scan(&body); err != nil {
		t.Fatalf("read identities_find_by_provider_subject's body: %v", err)
	}

	for _, forbidden := range []string{"connections", "calendars", "edges", "calendar_grants", "event_mapping"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("identities_find_by_provider_subject names %q; it runs SECURITY DEFINER and may read "+
				"identities and nothing else", forbidden)
		}
	}
}

// TestASignInLookupNeverFindsAnotherUsersCalendarConnection is the same
// security boundary internal/auth's own test holds down, asserted here
// against the real schema: the pre-login lookup is given a provider
// account that exists only as somebody's calendar connection, and it
// must find nothing. internal/auth then creates a separate account,
// which is the outcome this makes possible.
func TestASignInLookupNeverFindsAnotherUsersCalendarConnection(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	store := restrictedStore(ctx, t, adminPool, adminURL)

	subject := "google-account-" + uuid.NewString()

	owner, err := store.Users().Create(ctx, storage.User{
		Email:  "owner-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create the owner: %v", err)
	}

	if err := store.WithUser(ctx, owner.ID, func(ctx context.Context) error {
		_, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      owner.ID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: subject,
			Credentials: []byte("the connected calendar's refresh token"),
		})

		return err
	}); err != nil {
		t.Fatalf("create the calendar connection: %v", err)
	}

	_, err = store.Identities().FindByProviderSubject(ctx, storage.IdentityProviderGoogle, subject)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("FindByProviderSubject() error = %v, want storage.ErrNotFound: a calendar connection must "+
			"never resolve a sign-in", err)
	}
}

// TestIdentityLookupResolvesASignInBeforeAnyUserScopeExists covers the
// lookup's own job: an identity written under one user is found by
// (provider, subject) with no scope set at all, which is the state every
// sign-in starts from, and a subject nobody registered is not found.
func TestIdentityLookupResolvesASignInBeforeAnyUserScopeExists(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	store := restrictedStore(ctx, t, adminPool, adminURL)

	user, err := store.Users().Create(ctx, storage.User{
		Email:  "signin-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create the user: %v", err)
	}

	subject := "apple-subject-" + uuid.NewString()

	var created storage.Identity

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		var err error
		created, err = store.Identities().Create(ctx, storage.Identity{
			UserID:   user.ID,
			Provider: storage.IdentityProviderApple,
			Subject:  subject,
		})

		return err
	}); err != nil {
		t.Fatalf("create the identity: %v", err)
	}

	found, err := store.Identities().FindByProviderSubject(ctx, storage.IdentityProviderApple, subject)
	if err != nil {
		t.Fatalf("FindByProviderSubject() error = %v, want the identity back with no user scope set", err)
	}

	if found.ID != created.ID || found.UserID != user.ID {
		t.Fatalf("FindByProviderSubject() = %+v, want identity %s of user %s", found, created.ID, user.ID)
	}

	// The same subject at another provider is a different person.
	if _, err := store.Identities().FindByProviderSubject(ctx, storage.IdentityProviderGoogle, subject); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("FindByProviderSubject() at another provider error = %v, want storage.ErrNotFound", err)
	}
}

// TestDeleteAccountRemovesEverythingTheUserOwns runs the deletion
// against the real schema, where the order the rows go in is what the
// foreign keys actually enforce, and checks through a connection that
// bypasses row level security that nothing is left behind.
func TestDeleteAccountRemovesEverythingTheUserOwns(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	store := restrictedStore(ctx, t, adminPool, adminURL)

	user, err := store.Users().Create(ctx, storage.User{
		Email:  "leaving-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create the user: %v", err)
	}

	staying, err := store.Users().Create(ctx, storage.User{
		Email:  "staying-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create the second user: %v", err)
	}

	// Everything this account owns, one row per user-owned table, built
	// in the order the foreign keys require.
	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		if _, err := store.Identities().Create(ctx, storage.Identity{
			UserID:   user.ID,
			Provider: storage.IdentityProviderGoogle,
			Subject:  "google-subject-" + uuid.NewString(),
		}); err != nil {
			return err
		}

		connection, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      user.ID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: "a connected account",
			Credentials: []byte("a refresh token"),
		})
		if err != nil {
			return err
		}

		source, err := store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: connection.ID,
			ExternalID:   "source-" + uuid.NewString(),
			Name:         "Source",
		})
		if err != nil {
			return err
		}

		target, err := store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: connection.ID,
			ExternalID:   "target-" + uuid.NewString(),
			Name:         "Target",
			Managed:      true,
		})
		if err != nil {
			return err
		}

		edge, err := store.Edges().Create(ctx, storage.Edge{
			UserID:           user.ID,
			SourceCalendarID: source.ID,
			TargetCalendarID: target.ID,
			DetailLevel:      storage.DetailLevelBusyBlock,
		})
		if err != nil {
			return err
		}

		if _, err := store.CalendarGrants().Create(ctx, storage.CalendarGrant{
			OwnerUserID:    user.ID,
			GranteeUserID:  staying.ID,
			CalendarID:     source.ID,
			MaxDetailLevel: storage.DetailLevelBusyBlock,
		}); err != nil {
			return err
		}

		_, err = store.EventMappings().Create(ctx, storage.EventMapping{
			UserID:        user.ID,
			EdgeID:        edge.ID,
			SourceEventID: "source-event",
			TargetEventID: "target-event",
			StartsAt:      time.Now().UTC().Truncate(time.Second),
			EndsAt:        time.Now().UTC().Add(time.Hour).Truncate(time.Second),
			ContentHash:   "a-content-hash",
			Details:       []byte("a copied title"),
		})

		return err
	}); err != nil {
		t.Fatalf("seed the account's rows: %v", err)
	}

	if err := store.Users().DeleteAccount(ctx, user.ID); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}

	tables := []string{"identities", "connections", "calendars", "edges", "event_mapping"}
	for _, table := range tables {
		var left int

		// Through the admin pool, which bypasses row level security: an
		// empty result read as the deleted user would prove nothing.
		if err := adminPool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE user_id = $1`, user.ID).Scan(&left); err != nil {
			t.Fatalf("count the remaining %s rows: %v", table, err)
		}

		if left != 0 {
			t.Errorf("%s left %d row(s) behind, want 0", table, left)
		}
	}

	var grantsLeft int
	if err := adminPool.QueryRow(ctx,
		`SELECT count(*) FROM calendar_grants WHERE owner_user_id = $1 OR grantee_user_id = $1`,
		user.ID).Scan(&grantsLeft); err != nil {
		t.Fatalf("count the remaining grants: %v", err)
	}

	if grantsLeft != 0 {
		t.Errorf("calendar_grants left %d row(s) behind, want 0", grantsLeft)
	}

	var usersLeft int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, user.ID).Scan(&usersLeft); err != nil {
		t.Fatalf("count the remaining user rows: %v", err)
	}

	if usersLeft != 0 {
		t.Errorf("users left %d row(s) behind, want 0", usersLeft)
	}

	requireUsersExist(ctx, t, adminPool, staying.ID)

	// Deleting again reports that there is nothing left to delete, rather
	// than reporting success for a row it did not touch.
	if err := store.Users().DeleteAccount(ctx, user.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("second DeleteAccount() error = %v, want storage.ErrNotFound", err)
	}
}

// restrictedStore builds a Store on a pool connected as testAppRole, the
// NOSUPERUSER NOBYPASSRLS role every assertion in this file has to run
// through for row level security to mean anything.
func restrictedStore(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, adminURL string) *postgres.Store {
	t.Helper()

	restrictedPool, err := postgres.NewPool(ctx, setUpTestAppRole(ctx, t, adminPool, adminURL))
	if err != nil {
		t.Fatalf("open restricted pool as %s: %v", testAppRole, err)
	}
	t.Cleanup(restrictedPool.Close)

	store, err := postgres.NewStore(restrictedPool, testEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	return store
}

// setUpTestAppRole creates testAppRole on the database adminURL points
// at, if it does not exist yet, grants it exactly the privileges the
// application needs on the user-scoped tables, and returns a connection
// string for it. Role creation has no "IF NOT EXISTS" in Postgres, so
// this checks pg_roles itself to stay safe to run repeatedly against the
// same, persistent database.
func setUpTestAppRole(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, adminURL string) string {
	t.Helper()

	createRole := `
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '` + testAppRole + `') THEN
        CREATE ROLE ` + testAppRole + ` LOGIN PASSWORD '` + testAppPassword + `' NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;`

	if _, err := adminPool.Exec(ctx, createRole); err != nil {
		t.Fatalf("create %s role: %v", testAppRole, err)
	}

	// Mirrors docker-compose.yaml's "grant-app-role" service: every table
	// gets exactly the privileges its own repository in
	// internal/storage/postgres actually uses. users gets DELETE because
	// userRepository.DeleteAccount removes the row itself, and the two
	// SECURITY DEFINER lookup functions get EXECUTE because the two
	// pre-login lookups go through them.
	grant := `
GRANT USAGE ON SCHEMA public TO ` + testAppRole + `;
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO ` + testAppRole + `;
GRANT EXECUTE ON FUNCTION users_find_by_email(text) TO ` + testAppRole + `;
GRANT EXECUTE ON FUNCTION identities_find_by_provider_subject(text, text) TO ` + testAppRole + `;
GRANT SELECT, INSERT, DELETE ON identities, calendars, calendar_grants TO ` + testAppRole + `;
GRANT SELECT, INSERT, UPDATE, DELETE ON connections, edges, event_mapping TO ` + testAppRole + `;`

	if _, err := adminPool.Exec(ctx, grant); err != nil {
		t.Fatalf("grant %s privileges: %v", testAppRole, err)
	}

	restricted, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}

	restricted.User = url.UserPassword(testAppRole, testAppPassword)

	return restricted.String()
}

// The SQLSTATEs the assertions above name a refusing mechanism by. See
// https://www.postgresql.org/docs/current/errcodes-appendix.html.
const (
	// pgInsufficientPrivilege is what a row level security policy raises
	// when it refuses a write outright, rather than filtering a row away.
	pgInsufficientPrivilege = "42501"
	// pgRaiseException is what a plpgsql RAISE EXCEPTION that names no
	// ERRCODE of its own carries, which is how
	// edges_check_calendar_access refuses.
	pgRaiseException = "P0001"
)

// edgesSourceCalendarRefusal is the message edges_check_calendar_access
// raises for a source calendar the edge's own user neither owns nor was
// granted. It is spelled out here, matching db/migrations, so a test can
// assert which check refused a write rather than only that one did.
const edgesSourceCalendarRefusal = "edges: source_calendar_id is not owned by or granted to this edge's user"

// requirePgError fails unless err carries a Postgres error with the given
// SQLSTATE and a message containing wantMessage. A test that only asserts
// "an error came back" cannot tell a trigger's refusal from a row level
// security refusal, a foreign key violation or a check constraint, so it
// keeps passing when the check it is about is removed and a different one
// happens to refuse in its place.
func requirePgError(t *testing.T, err error, code, wantMessage string) {
	t.Helper()

	if err == nil {
		t.Fatalf("got no error, want SQLSTATE %s with a message containing %q", code, wantMessage)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v, want a Postgres error with SQLSTATE %s", err, code)
	}

	if pgErr.Code != code {
		t.Fatalf("SQLSTATE = %s (%q), want %s: a different mechanism refused this write than the one under test",
			pgErr.Code, pgErr.Message, code)
	}

	if !strings.Contains(pgErr.Message, wantMessage) {
		t.Fatalf("message = %q, want it to contain %q", pgErr.Message, wantMessage)
	}
}

// requireUsersExist asserts, through a connection that bypasses row level
// security, that every id is really in the users table. It is the control
// every "the restricted role sees nothing" assertion needs: without it,
// an empty result proves only that nothing was ever written.
func requireUsersExist(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()

	var found int

	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = ANY($1)`, ids).Scan(&found); err != nil {
		t.Fatalf("admin count of users %v: %v", ids, err)
	}

	if found != len(ids) {
		t.Fatalf("admin found %d of the %d expected user rows; the assertions that follow would prove nothing", found, len(ids))
	}
}

// visibleUserIDs runs a deliberately unfiltered "SELECT id FROM users"
// through pool, in a transaction scoped to scope, or in one scoped to no
// user at all when scope is uuid.Nil. The query carries no predicate of
// its own on purpose: with several users in the table, whatever comes
// back is what the policy admitted and nothing else, so a policy quietly
// replaced with USING (true) fails this where a repository call, whose
// query filters by id itself, would still pass.
func visibleUserIDs(ctx context.Context, t *testing.T, pool *pgxpool.Pool, scope uuid.UUID) []uuid.UUID {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if scope != uuid.Nil {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, scope.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}
	}

	rows, err := tx.Query(ctx, `SELECT id FROM users`)
	if err != nil {
		t.Fatalf("SELECT id FROM users: %v", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("scan users: %v", err)
	}

	return ids
}

// connectionIDs decodes a literal "SELECT * FROM connections" result
// into the ids it carries. connectionRawRow's "db" tags, not column
// position, do the matching, so the test stays correct even if a later
// migration adds a column to connections.
func connectionIDs(t *testing.T, rows pgx.Rows) []uuid.UUID {
	t.Helper()

	records, err := pgx.CollectRows(rows, pgx.RowToStructByName[connectionRawRow])
	if err != nil {
		t.Fatalf("scan connections: %v", err)
	}

	ids := make([]uuid.UUID, len(records))
	for i, record := range records {
		ids[i] = record.ID
	}

	return ids
}

func assertOnlyConnection(t *testing.T, connections []storage.Connection, wantID uuid.UUID) {
	t.Helper()

	if len(connections) != 1 {
		t.Fatalf("got %d connections, want exactly 1", len(connections))
	}

	if connections[0].ID != wantID {
		t.Fatalf("got connection %v, want %v", connections[0].ID, wantID)
	}
}
