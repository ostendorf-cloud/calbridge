// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// fakeMirror stands in for internal/mirror's Service. It records what the
// handlers ask of it and answers with whatever a test set, so a test can
// prove what the handler does around the provider without a provider.
type fakeMirror struct {
	mu sync.Mutex

	// createErr, deleteErr and discardErr are returned by the matching
	// method when set.
	createErr  error
	deleteErr  error
	discardErr error

	// afterCreate runs once the provider has made a calendar and before
	// CreateCalendar returns, so a test can change the world between the
	// handler's two scopes.
	afterCreate func()

	// refused is the set of edges CredentialRefused reports as refused.
	refused map[uuid.UUID]bool

	// edges is what DeleteEdge sees when the row still exists: a test
	// reads it to prove the teardown ran before the row went.
	store *storagetest.Store

	created   []string
	discarded []string
	queued    []uuid.UUID
	deleted   []uuid.UUID
	revoked   []storage.CalendarGrant

	// edgeExistedAtDelete records whether the edge row was still there
	// when DeleteEdge was called.
	edgeExistedAtDelete []bool

	calendarSeq int
}

func (f *fakeMirror) CreateCalendar(_ context.Context, _, _ uuid.UUID, name string) (string, error) {
	f.mu.Lock()

	if f.createErr != nil {
		f.mu.Unlock()

		return "", f.createErr
	}

	f.calendarSeq++
	id := fmt.Sprintf("provider-calendar-%d", f.calendarSeq)
	f.created = append(f.created, id+":"+name)
	hook := f.afterCreate

	f.mu.Unlock()

	if hook != nil {
		hook()
	}

	return id, nil
}

func (f *fakeMirror) DiscardCalendar(_ context.Context, _, _ uuid.UUID, externalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.discarded = append(f.discarded, externalID)

	return f.discardErr
}

func (f *fakeMirror) EdgeCreated(_, edgeID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.queued = append(f.queued, edgeID)
}

func (f *fakeMirror) DeleteEdge(_ context.Context, _, edgeID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, found := f.store.Edge(edgeID)
	f.edgeExistedAtDelete = append(f.edgeExistedAtDelete, found)
	f.deleted = append(f.deleted, edgeID)

	return f.deleteErr
}

func (f *fakeMirror) GrantRevoked(grant storage.CalendarGrant) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.revoked = append(f.revoked, grant)
}

func (f *fakeMirror) CredentialRefused(edgeID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.refused[edgeID]
}

// graphHandlersWithMirror is graphHandlers with the fake mirror the
// handlers were built with returned too, so a test can set its answers and
// read what it was asked.
func graphHandlersWithMirror(t *testing.T) (*storagetest.Store, string, *fakeMirror) {
	t.Helper()

	deps := testDeps(t)
	store := testStore(t, deps)

	mirror, ok := deps.Mirror.(*fakeMirror)
	if !ok {
		t.Fatalf("deps.Mirror is a %T, want the fake mirror", deps.Mirror)
	}

	mirror.store = store

	_, baseURL := testClient(t, build(t, deps))

	return store, baseURL, mirror
}

func createTargetBody(source, account fmt.Stringer) string {
	return fmt.Sprintf(
		`{"source_calendar_id":%q,"target":{"account_id":%q,"name":"Mirror of Work"},"detail_level":"busy_block"}`,
		source, account)
}

func TestAnEdgeIntoACreatedCalendarCarriesTheProvidersOwnID(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	resp := do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges",
		createTargetBody(source.ID, account.ID), owner.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	if len(mirror.created) != 1 || mirror.created[0] != "provider-calendar-1:Mirror of Work" {
		t.Fatalf("the provider was asked to create %v, want one calendar named by the request", mirror.created)
	}

	var externalID string

	if err := store.WithUser(t.Context(), owner.userID, func(ctx context.Context) error {
		calendars, err := store.Calendars().List(ctx)
		if err != nil {
			return err
		}

		for _, calendar := range calendars {
			if calendar.Managed {
				externalID = calendar.ExternalID
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("list calendars: %v", err)
	}

	if externalID != "provider-calendar-1" {
		t.Errorf("the managed calendar's external_id = %q, want the id the provider gave it", externalID)
	}

	if len(mirror.queued) != 1 {
		t.Errorf("the mirror was asked to reconcile %d edges, want 1", len(mirror.queued))
	}

	if len(mirror.discarded) != 0 {
		t.Errorf("the mirror discarded %v, want nothing on a successful create", mirror.discarded)
	}
}

func TestAnEdgeBeyondItsGrantCreatesNoCalendarAtTheProvider(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	grantee := signIn(t, store, baseURL, testOtherEmail)

	account, _ := seedAccountCalendar(t, store, grantee.userID, "Private Google", "Private")
	_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	// A grant at the lowest level, and an edge asking for more: the first
	// scope refuses it before any calendar is created.
	grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelBusyBlock)

	refused := readRefusal(t, do(t, grantee.client, http.MethodPost, baseURL+"/api/v1/edges", fmt.Sprintf(
		`{"source_calendar_id":%q,"target":{"account_id":%q,"name":"Mirror"},"detail_level":"full"}`,
		shared.ID, account.ID), grantee.csrf))
	if refused.status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", refused.status, http.StatusForbidden)
	}

	if len(mirror.created) != 0 {
		t.Errorf("the provider was asked to create %v for a refused edge, want nothing", mirror.created)
	}
}

func TestAnEdgeWhoseStoreWriteFailsAfterTheProviderCreateDiscardsTheCalendar(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	// The source is deleted between the first scope and the second by a
	// mirror that removes it as part of creating the calendar.
	mirror.afterCreate = func() {
		if err := store.WithUser(t.Context(), owner.userID, func(ctx context.Context) error {
			return store.Calendars().Delete(ctx, source.ID)
		}); err != nil {
			t.Errorf("delete the source: %v", err)
		}
	}

	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges",
		createTargetBody(source.ID, account.ID), owner.csrf))
	if refused.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", refused.status, http.StatusNotFound)
	}

	if len(mirror.discarded) != 1 || mirror.discarded[0] != "provider-calendar-1" {
		t.Errorf("the mirror discarded %v, want the calendar the provider created", mirror.discarded)
	}

	if len(mirror.queued) != 0 {
		t.Errorf("the mirror was asked to reconcile %d edges, want none", len(mirror.queued))
	}
}

func TestACreateRefusedByTheProviderIsAnswered(t *testing.T) {
	serial(t)

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "credential refused",
			err:        provider.ErrCredentialRejected,
			wantStatus: http.StatusBadGateway,
			wantCode:   "credential_refused",
		},
		{
			name:       "calendar creation unsupported",
			err:        provider.ErrUnsupported,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			serial(t)

			store, baseURL, mirror := graphHandlersWithMirror(t)
			mirror.createErr = testCase.err

			owner := signIn(t, store, baseURL, testEmail)
			account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

			refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges",
				createTargetBody(source.ID, account.ID), owner.csrf))

			if refused.status != testCase.wantStatus {
				t.Errorf("status = %d, want %d", refused.status, testCase.wantStatus)
			}

			if refused.code != testCase.wantCode {
				t.Errorf("code = %q, want %q", refused.code, testCase.wantCode)
			}

			if store.CountEdges() != 0 {
				t.Errorf("the store holds %d edges, want none", store.CountEdges())
			}
		})
	}
}

func TestDeletingAnEdgeTearsItDownBeforeTheRowGoes(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	edge := createEdgeFor(t, owner, baseURL, first.ID, second.ID)

	resp := do(t, owner.client, http.MethodDelete, baseURL+"/api/v1/edges/"+edge, "", owner.csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if len(mirror.deleted) != 1 || mirror.deleted[0].String() != edge {
		t.Fatalf("the mirror tore down %v, want the deleted edge %s", mirror.deleted, edge)
	}

	if !mirror.edgeExistedAtDelete[0] {
		t.Error("the edge row was gone before the mirror ran the teardown, want the row to go after it")
	}
}

func TestAnEdgeStaysWhenTheProviderRefusesTheCredential(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)
	mirror.deleteErr = provider.ErrCredentialRejected

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	edge := createEdgeFor(t, owner, baseURL, first.ID, second.ID)

	refused := readRefusal(t, do(t, owner.client, http.MethodDelete, baseURL+"/api/v1/edges/"+edge, "", owner.csrf))
	if refused.status != http.StatusBadGateway || refused.code != "credential_refused" {
		t.Errorf("answer = %+v, want a 502 credential_refused", refused)
	}

	if store.CountEdges() != 1 {
		t.Errorf("the store holds %d edges, want the edge to stay", store.CountEdges())
	}
}

func TestDeletingAnEdgeThatIsNotYoursNeverReachesTheMirror(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	other := signIn(t, store, baseURL, testOtherEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	edge := createEdgeFor(t, owner, baseURL, first.ID, second.ID)

	refused := readRefusal(t, do(t, other.client, http.MethodDelete, baseURL+"/api/v1/edges/"+edge, "", other.csrf))
	if refused.status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", refused.status, http.StatusNotFound)
	}

	if len(mirror.deleted) != 0 {
		t.Errorf("the mirror tore down %v for an edge the caller cannot see, want nothing", mirror.deleted)
	}
}

func TestRevokingAGrantHandsItToTheMirror(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	grantee := signIn(t, store, baseURL, testOtherEmail)
	_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	grant := grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelTitleTime)

	resp := do(t, owner.client, http.MethodDelete, baseURL+"/api/v1/grants/"+grant.ID, "", owner.csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if len(mirror.revoked) != 1 {
		t.Fatalf("the mirror was told of %d revocations, want 1", len(mirror.revoked))
	}

	got := mirror.revoked[0]
	if got.ID.String() != grant.ID || got.CalendarID != shared.ID || got.GranteeUserID != grantee.userID {
		t.Errorf("the mirror was told of %+v, want the grant that was revoked", got)
	}
}

func TestARefusedCredentialShowsOnTheEdge(t *testing.T) {
	serial(t)

	store, baseURL, mirror := graphHandlersWithMirror(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	edge := createEdgeFor(t, owner, baseURL, first.ID, second.ID)

	before := readGraph(t, owner, baseURL)
	if len(before.Edges) != 1 || before.Edges[0].CredentialRefused {
		t.Fatalf("edges = %+v, want one edge not marked refused", before.Edges)
	}

	mirror.refused = map[uuid.UUID]bool{uuid.MustParse(edge): true}

	after := readGraph(t, owner, baseURL)
	if len(after.Edges) != 1 || !after.Edges[0].CredentialRefused {
		t.Errorf("edges = %+v, want the edge marked credential_refused", after.Edges)
	}
}

// createEdgeFor creates an edge between two existing calendars and returns
// its id.
func createEdgeFor(t *testing.T, session *testSession, baseURL string, source, target uuid.UUID) string {
	t.Helper()

	resp := do(t, session.client, http.MethodPost, baseURL+"/api/v1/edges", fmt.Sprintf(
		`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"busy_block"}`, source, target), session.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create an edge: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var created struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode the created edge: %v", err)
	}

	return created.ID
}
