// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

//go:build integration

// The engine's own mapping table behaviour needs a real Postgres: the
// unique index on (edge_id, source_event_id), the foreign key from
// event_mapping to edges, and the row level security that makes another
// user's calendar invisible are the database's own, and a fake
// repository proves none of them. The build tag keeps this out of a
// plain "go test ./..."; "task test:integration" is what passes it.
package sync_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// The role these tests run as. It is deliberately NOSUPERUSER and
// NOBYPASSRLS: a superuser bypasses row level security regardless of
// FORCE ROW LEVEL SECURITY, and the revocation test below would then
// fail rather than pass emptily. Engine.owns resolves the grantor's
// calendar through the grantee's own scope; with row level security
// bypassed that lookup succeeds, the coverage check never consults the
// grants at all, and nothing is torn down. The restricted role is what
// makes the grantee's view of the grantor's calendar the real one. It
// carries its own name rather than reusing
// internal/storage/postgres's, so the two packages never race to create
// the same role. The password and the key are fixed, obviously-fake
// values that only ever reach a disposable test Postgres.
const (
	syncTestRole     = "calbridge_sync_test_app"
	syncTestPassword = "calbridge-test-only-not-a-real-secret"
)

// catalogLockKey is the advisory lock key every statement that changes a
// role or a privilege takes first. A role name of its own is not enough:
// GRANT on a table rewrites that table's own pg_class row, and two roles
// granted on the same table at once make Postgres refuse with "tuple
// concurrently updated" (SQLSTATE XX000). "go test ./internal/..." runs
// the integration packages in parallel against one database, so the
// grants are serialised instead. internal/storage/postgres's integration
// test carries the same key; the two values must stay equal.
const catalogLockKey int64 = 0x63616c6272646730

// inCatalogLock runs fn in a transaction that holds catalogLockKey. The
// lock is transaction level, so Commit or Rollback releases it and no
// path can leak it.
func inCatalogLock(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin catalogue transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, catalogLockKey); err != nil {
		return fmt.Errorf("take catalogue lock: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit catalogue transaction: %w", err)
	}

	return nil
}

// integrationEncryptionKey is 32 bytes, matching the AES-256 key length
// postgres.NewStore requires. Its content is arbitrary and never used
// outside this test binary.
var integrationEncryptionKey = []byte("01234567890123456789012345678901")

// requireIntegrationDatabase returns DATABASE_URL, or stops the test:
// a skip where no CI is set, which is the local case with no Postgres
// reachable, and a failure where it is, so a job that lost its own env
// block reports a failure instead of reporting green having proven
// nothing.
func requireIntegrationDatabase(t *testing.T) string {
	t.Helper()

	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	if os.Getenv("CI") != "" {
		t.Fatal("DATABASE_URL not set while CI is set; the integration job lost its own env: block")
	}

	t.Skip("DATABASE_URL not set; skipping the Postgres-backed test")

	return ""
}

// newIntegrationStore opens a store on the restricted role, creating the
// role if the target database does not have it yet. Role creation has no
// "IF NOT EXISTS" in Postgres, so this checks pg_roles itself and stays
// safe to run repeatedly against the same database.
func newIntegrationStore(ctx context.Context, t *testing.T) *postgres.Store {
	t.Helper()

	adminURL := requireIntegrationDatabase(t)

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	createRole := `
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '` + syncTestRole + `') THEN
        CREATE ROLE ` + syncTestRole + ` LOGIN PASSWORD '` + syncTestPassword + `' NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;`

	// The same privileges docker/postgres/init-app-role.sh grants the
	// application: exactly what the repositories this engine calls use,
	// and no DELETE on users, which nothing deletes.
	grant := `
GRANT USAGE ON SCHEMA public TO ` + syncTestRole + `;
GRANT SELECT, INSERT, UPDATE ON users TO ` + syncTestRole + `;
GRANT EXECUTE ON FUNCTION users_find_by_email(text) TO ` + syncTestRole + `;
GRANT SELECT, INSERT, DELETE ON identities, calendars, calendar_grants TO ` + syncTestRole + `;
GRANT SELECT, INSERT, UPDATE, DELETE ON connections, edges, event_mapping TO ` + syncTestRole + `;`

	// Role creation and the grants share one transaction under the
	// catalogue lock, so no other test binary updates the same catalogue
	// rows between the two.
	if err := inCatalogLock(ctx, adminPool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, createRole); err != nil {
			return fmt.Errorf("create %s role: %w", syncTestRole, err)
		}

		if _, err := tx.Exec(ctx, grant); err != nil {
			return fmt.Errorf("grant %s privileges: %w", syncTestRole, err)
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	restricted, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	restricted.User = url.UserPassword(syncTestRole, syncTestPassword)

	pool, err := postgres.NewPool(ctx, restricted.String())
	if err != nil {
		t.Fatalf("open restricted pool as %s: %v", syncTestRole, err)
	}
	t.Cleanup(pool.Close)

	store, err := postgres.NewStore(pool, integrationEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	return store
}

// newIntegrationUser creates one user to scope rows to.
func newIntegrationUser(ctx context.Context, t *testing.T, store *postgres.Store, name string) storage.User {
	t.Helper()

	user, err := store.Users().Create(ctx, storage.User{
		Email:  name + "-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}

	return user
}

// newIntegrationCalendar records one calendar of one connector as a row
// belonging to user. Each connector is its own provider account with its
// own opaque id space, so each gets its own connection: calendars is
// unique on (connection_id, external_id).
func newIntegrationCalendar(ctx context.Context, t *testing.T, store *postgres.Store, user storage.User, externalID, name string) storage.Calendar {
	t.Helper()

	var calendar storage.Calendar

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		connection, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      user.ID,
			Provider:    storage.ConnectionProviderCalDAV,
			DisplayName: name,
			Credentials: []byte("app-specific-password"),
		})
		if err != nil {
			return fmt.Errorf("create the connection: %w", err)
		}

		calendar, err = store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: connection.ID,
			ExternalID:   externalID,
			Name:         name,
		})
		if err != nil {
			return fmt.Errorf("create the calendar: %w", err)
		}

		return nil
	}); err != nil {
		t.Fatalf("set up calendar %q: %v", name, err)
	}

	return calendar
}

// newIntegrationEngine builds an engine on a real store, with a binder
// holding the bindings the test set up.
func newIntegrationEngine(t *testing.T, store *postgres.Store, binder *fakeBinder) *sync.Engine {
	t.Helper()

	engine, err := sync.New(sync.Options{
		Scope:     store,
		Edges:     store.Edges(),
		Calendars: store.Calendars(),
		Grants:    store.CalendarGrants(),
		Mappings:  store.EventMappings(),
		Binder:    binder,
		Now:       func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	return engine
}

// TestEngineAgainstPostgres runs one edge end to end over the real
// mapping table: a reconcile that copies two events and records them,
// a second pass that writes nothing, and a deletion that gives back
// everything the edge wrote.
func TestEngineAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	store := newIntegrationStore(ctx, t)
	user := newIntegrationUser(ctx, t, store, "engine")

	source := newFakeConnector(defaultSourceCaps)
	target := newFakeConnector(defaultTargetCaps)
	sourceCal := source.AddCalendar(testConnection, provider.Calendar{Name: "Privat", TimeZone: "Europe/Berlin"}).ID
	targetCal := target.AddCalendar(testConnection, provider.Calendar{Name: "Arbeit", TimeZone: "Europe/Berlin"}).ID

	sourceRow := newIntegrationCalendar(ctx, t, store, user, sourceCal, "Privat")
	targetRow := newIntegrationCalendar(ctx, t, store, user, targetCal, "Arbeit")

	var edge storage.Edge

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		var err error
		edge, err = store.Edges().Create(ctx, storage.Edge{
			UserID:           user.ID,
			SourceCalendarID: sourceRow.ID,
			TargetCalendarID: targetRow.ID,
			DetailLevel:      storage.DetailLevelTitleTime,
		})

		return err
	}); err != nil {
		t.Fatalf("create the edge: %v", err)
	}

	binding := integrationBinding(t, edge, source, sourceCal, target, targetCal)
	binder := newFakeBinder()
	binder.add(binding)
	engine := newIntegrationEngine(t, store, binder)

	source.Put(sourceCal, meeting("Jour fixe", 4, 9))
	source.Put(sourceCal, meeting("Retro", 5, 14))

	first, err := engine.Reconcile(ctx, binding)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := first.Count(sync.ActionCreate); got != 2 {
		t.Fatalf("creates: got %d, want 2 (all: %v)", got, actions(first))
	}

	mappings := listMappings(ctx, t, store, user.ID, edge.ID)
	if len(mappings) != 2 {
		t.Fatalf("mapping rows in Postgres: got %d, want 2", len(mappings))
	}
	if mappings[0].ContentHash == "" || mappings[0].TargetEventID == "" {
		t.Fatalf("mapping row is incomplete: %+v", mappings[0])
	}
	if mappings[0].StartsAt.IsZero() || !mappings[0].EndsAt.After(mappings[0].StartsAt) {
		t.Fatalf("mapping row times: %s to %s", mappings[0].StartsAt, mappings[0].EndsAt)
	}

	second, err := engine.Reconcile(ctx, binding)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if got := second.CountReason(sync.ReasonUnchanged); got != 2 {
		t.Fatalf("unchanged: got %d, want 2 (all: %v)", got, actions(second))
	}
	if creates, updates, _ := target.Counts(); creates != 2 || updates != 0 {
		t.Fatalf("connector calls after two passes: got %d creates and %d updates, want 2 and 0", creates, updates)
	}

	if _, err := engine.DeleteEdge(ctx, binding); err != nil {
		t.Fatalf("delete edge: %v", err)
	}

	if left := target.Events(targetCal); len(left) != 0 {
		t.Fatalf("the target still holds %d events", len(left))
	}
	if left := listMappings(ctx, t, store, user.ID, edge.ID); len(left) != 0 {
		t.Fatalf("mapping rows after the deletion: got %d, want none", len(left))
	}

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		_, err := store.Edges().FindByID(ctx, edge.ID)

		return err
	}); err == nil {
		t.Fatal("the edge row is still there")
	}
}

// TestEngineTearsDownAnEdgeWhoseGrantWasRevoked is the invariant #4
// handed to this package: the edges trigger refuses an edge over a
// calendar nobody shared when the edge is written, and revoking the
// grant afterwards does not touch the edge. It runs against Postgres
// because what makes the revocation visible is row level security
// hiding the grantor's calendar from the grantee, which is the one part
// a fake store cannot prove about itself.
func TestEngineTearsDownAnEdgeWhoseGrantWasRevoked(t *testing.T) {
	ctx := context.Background()
	store := newIntegrationStore(ctx, t)

	owner := newIntegrationUser(ctx, t, store, "owner")
	grantee := newIntegrationUser(ctx, t, store, "grantee")

	source := newFakeConnector(defaultSourceCaps)
	target := newFakeConnector(defaultTargetCaps)
	sourceCal := source.AddCalendar(testConnection, provider.Calendar{Name: "Privat", TimeZone: "Europe/Berlin"}).ID
	targetCal := target.AddCalendar(testConnection, provider.Calendar{Name: "Arbeit", TimeZone: "Europe/Berlin"}).ID

	sourceRow := newIntegrationCalendar(ctx, t, store, owner, sourceCal, "Privat")
	targetRow := newIntegrationCalendar(ctx, t, store, grantee, targetCal, "Arbeit")

	var grant storage.CalendarGrant

	if err := store.WithUser(ctx, owner.ID, func(ctx context.Context) error {
		var err error
		grant, err = store.CalendarGrants().Create(ctx, storage.CalendarGrant{
			OwnerUserID:    owner.ID,
			GranteeUserID:  grantee.ID,
			CalendarID:     sourceRow.ID,
			MaxDetailLevel: storage.DetailLevelFull,
		})

		return err
	}); err != nil {
		t.Fatalf("create the grant: %v", err)
	}

	// The edge belongs to the grantee and reads the owner's calendar. The
	// trigger in db/migrations admits it only because the grant exists.
	var edge storage.Edge

	if err := store.WithUser(ctx, grantee.ID, func(ctx context.Context) error {
		var err error
		edge, err = store.Edges().Create(ctx, storage.Edge{
			UserID:           grantee.ID,
			SourceCalendarID: sourceRow.ID,
			TargetCalendarID: targetRow.ID,
			DetailLevel:      storage.DetailLevelBusyBlock,
		})

		return err
	}); err != nil {
		t.Fatalf("create the granted edge: %v", err)
	}

	binding := integrationBinding(t, edge, source, sourceCal, target, targetCal)
	binder := newFakeBinder()
	binder.add(binding)
	engine := newIntegrationEngine(t, store, binder)

	source.Put(sourceCal, meeting("Jour fixe", 4, 9))

	if _, err := engine.Run(ctx, grantee.ID); err != nil {
		t.Fatalf("run while the grant stands: %v", err)
	}
	if got := len(target.Events(targetCal)); got != 1 {
		t.Fatalf("copies while the grant stands: got %d, want 1", got)
	}
	if got := len(listMappings(ctx, t, store, grantee.ID, edge.ID)); got != 1 {
		t.Fatalf("mapping rows while the grant stands: got %d, want 1", got)
	}

	if err := store.WithUser(ctx, owner.ID, func(ctx context.Context) error {
		return store.CalendarGrants().Delete(ctx, grant.ID)
	}); err != nil {
		t.Fatalf("revoke the grant: %v", err)
	}

	report, err := engine.Run(ctx, grantee.ID)
	if err != nil {
		t.Fatalf("run after the revocation: %v", err)
	}

	if got := report.CountReason(sync.ReasonGrantRevoked); got != 1 {
		t.Fatalf("deletions reported as a revoked grant: got %d, want 1 (all: %v)", got, actions(report))
	}
	if got := len(target.Events(targetCal)); got != 0 {
		t.Fatalf("copies after the revocation: got %d, want none", got)
	}
	if got := len(listMappings(ctx, t, store, grantee.ID, edge.ID)); got != 0 {
		t.Fatalf("mapping rows after the revocation: got %d, want none", got)
	}

	if err := store.WithUser(ctx, grantee.ID, func(ctx context.Context) error {
		_, err := store.Edges().FindByID(ctx, edge.ID)

		return err
	}); err == nil {
		t.Fatal("the edge still exists after its grant was revoked")
	}
}

// integrationBinding resolves a stored edge to the two in-memory
// calendars the test drives it with.
func integrationBinding(t *testing.T, edge storage.Edge, source *fakeConnector, sourceCal string, target *fakeConnector, targetCal string) sync.Binding {
	t.Helper()

	mask, err := sync.MaskFor(edge.DetailLevel)
	if err != nil {
		t.Fatalf("mask for %q: %v", edge.DetailLevel, err)
	}

	credential := testCredential()

	return sync.Binding{
		Edge:   edge,
		Mask:   mask,
		Source: sync.Endpoint{Connector: source, Credential: credential, CalendarID: sourceCal, TimeZone: "Europe/Berlin"},
		Target: sync.Endpoint{Connector: target, Credential: credential, CalendarID: targetCal, TimeZone: "Europe/Berlin"},
	}
}

// listMappings reads one edge's mapping rows through the store.
func listMappings(ctx context.Context, t *testing.T, store *postgres.Store, userID, edgeID uuid.UUID) []storage.EventMapping {
	t.Helper()

	var mappings []storage.EventMapping

	if err := store.WithUser(ctx, userID, func(ctx context.Context) error {
		var err error
		mappings, err = store.EventMappings().ListByEdge(ctx, edgeID)

		return err
	}); err != nil {
		t.Fatalf("list mappings: %v", err)
	}

	return mappings
}
