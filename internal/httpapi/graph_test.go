// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// The sync graph API's tests. Two things recur below and are worth
// saying once.
//
// Every assertion about a refusal names the mechanism that refused it,
// not merely that something did: a 404 is checked against the exact
// answer the equivalent nonexistent id produces (sameRefusal), and a
// refused detail level is checked against the message
// checkWithinGrants writes for sync.ErrGrantExceeded. A test that only
// asserted "an error came back" would keep passing with the rule
// deleted, because some other check would refuse in its place.
//
// The store under these tests is the in-memory fake, which imitates row
// level security rather than being it. What a test here proves is that
// the handler goes through Store.WithUser and answers correctly for
// whatever the scope admits. That the scope is really enforced by
// Postgres is internal/storage/postgres's integration test's job, and
// the grant and edge cases have their own assertions there.

// testOtherEmail is the second account every cross-user test needs. The
// first is testEmail, from httpapi_test.go. testThirdEmail is the third,
// needed where a row has to exist between two accounts the caller is on
// neither side of.
const (
	testOtherEmail = "other-person@example.com"
	testThirdEmail = "third-person@example.com"
)

// serial is called where t.Parallel() would otherwise be, by every test
// in this file that signs an account in or shares one store with another
// case running at the same time. Two separate globals are the reason.
//
// internal/auth caps how many Argon2id derivations may run at once
// against the memory the chart actually gives the pod, and that cap is a
// package-level budget shared by every test in this binary (see
// auth.MaxConcurrentPasswordHashes). Each sign-in here spends two
// derivations, one for the signup and one for the login, and enough of
// these running at once exhausts the budget and fails a test with
// "too many password hashes are already running", which is the server
// shedding load rather than anything the test is about.
//
// storagetest.Store keeps the scope a WithUser transaction carries in
// one field on the store rather than on the context, so two requests
// scoped to two users at the same time overwrite each other's and a
// repository call answers storage.ErrNoUserScope. The real store carries
// the scope in the transaction itself and has no such limit; this is the
// fake's, and it bounds how its cases may be run rather than anything
// about the code under test.
//
// It is a named call rather than a comment so that the reason travels
// with every test that needs it and a new one is not written parallel by
// habit.
func serial(*testing.T) {}

// testSession is one signed-in account: who it is, a client carrying its
// session cookie, and the CSRF token its unsafe requests spend.
type testSession struct {
	userID uuid.UUID
	email  string
	client *http.Client
	csrf   string
}

// signIn creates a local account for email and logs it in over the real
// routes, so the session and CSRF contract the graph routes sit behind
// is exercised rather than bypassed.
func signIn(t *testing.T, store *storagetest.Store, baseURL, email string) *testSession {
	t.Helper()

	local := auth.NewLocal(store, func() time.Time { return testNow })

	identity, enrolment, err := local.SignUp(t.Context(), email, testPassword)
	if err != nil {
		t.Fatalf("sign %s up: %v", email, err)
	}

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("generate the totp code for %s: %v", email, err)
	}

	client := newCookieClient(t)

	body := fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":%q}`, email, testPassword, code)

	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("log %s in: status = %d, want %d", email, resp.StatusCode, http.StatusNoContent)
	}

	meResp := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", "")
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("read /me for %s: status = %d, want %d", email, meResp.StatusCode, http.StatusOK)
	}

	var me meResponse
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /me for %s: %v", email, err)
	}

	return &testSession{userID: identity.UserID, email: email, client: client, csrf: me.CSRFToken}
}

// seedAccountCalendar gives userID a connected account holding one
// calendar, written straight through the store because no connector
// exists to list one yet.
func seedAccountCalendar(
	t *testing.T,
	store *storagetest.Store,
	userID uuid.UUID,
	accountName, calendarName string,
) (storage.Connection, storage.Calendar) {
	t.Helper()

	var (
		connection storage.Connection
		calendar   storage.Calendar
	)

	if err := store.WithUser(t.Context(), userID, func(ctx context.Context) error {
		var err error

		connection, err = store.Connections().Create(ctx, storage.Connection{
			UserID:      userID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: accountName,
			Credentials: []byte("not-a-real-refresh-token"),
		})
		if err != nil {
			return err
		}

		calendar, err = store.Calendars().Create(ctx, storage.Calendar{
			UserID:       userID,
			ConnectionID: connection.ID,
			ExternalID:   calendarName + "@group.calendar.example.com",
			Name:         calendarName,
		})

		return err
	}); err != nil {
		t.Fatalf("seed %s: %v", calendarName, err)
	}

	return connection, calendar
}

// graphBody is the shape GET /api/v1/graph answers with.
type graphBody struct {
	Accounts []struct {
		ID          string `json:"id"`
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name"`
	} `json:"accounts"`
	Calendars []struct {
		ID             string  `json:"id"`
		AccountID      *string `json:"account_id"`
		Name           string  `json:"name"`
		Managed        bool    `json:"managed"`
		Shared         bool    `json:"shared"`
		OwnerEmail     *string `json:"owner_email"`
		MaxDetailLevel *string `json:"max_detail_level"`
	} `json:"calendars"`
	Edges []struct {
		ID               string `json:"id"`
		SourceCalendarID string `json:"source_calendar_id"`
		TargetCalendarID string `json:"target_calendar_id"`
		DetailLevel      string `json:"detail_level"`
		CreatedCalendar  *struct {
			ID        string  `json:"id"`
			AccountID *string `json:"account_id"`
			Name      string  `json:"name"`
			Managed   bool    `json:"managed"`
		} `json:"created_calendar"`
	} `json:"edges"`
	Grants struct {
		Given    []grantBody `json:"given"`
		Received []grantBody `json:"received"`
	} `json:"grants"`
	DetailLevels []struct {
		Level  string   `json:"level"`
		Fields []string `json:"fields"`
	} `json:"detail_levels"`
}

type grantBody struct {
	ID                string `json:"id"`
	CalendarID        string `json:"calendar_id"`
	CounterpartyEmail string `json:"counterparty_email"`
	MaxDetailLevel    string `json:"max_detail_level"`
}

// errorBody is govue's own error envelope, as a client sees it.
type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// refusal is everything about an answer that a caller can observe and
// that two different refusals must not differ in. RequestID is
// deliberately left out: it differs on every request by design.
type refusal struct {
	status  int
	code    string
	message string
}

// readRefusal reads one refusal off a response.
func readRefusal(t *testing.T, resp *http.Response) refusal {
	t.Helper()

	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the error body: %v", err)
	}

	return refusal{status: resp.StatusCode, code: body.Error.Code, message: body.Error.Message}
}

// readGraph reads the whole canvas for one session.
func readGraph(t *testing.T, session *testSession, baseURL string) graphBody {
	t.Helper()

	resp := do(t, session.client, http.MethodGet, baseURL+"/api/v1/graph", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graph status for %s = %d, want %d", session.email, resp.StatusCode, http.StatusOK)
	}

	var body graphBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the graph body: %v", err)
	}

	return body
}

// graphHandlers builds a handler pair over a fresh store and returns
// both, so a test can seed the store the handlers read.
func graphHandlers(t *testing.T) (*storagetest.Store, string) {
	t.Helper()

	deps := testDeps(t)
	store := testStore(t, deps)
	_, baseURL := testClient(t, build(t, deps))

	return store, baseURL
}

func TestTheGraphShowsOnlyWhatTheSessionsOwnScopeAdmits(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	other := signIn(t, store, baseURL, testOtherEmail)

	_, ownerCalendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, otherCalendar := seedAccountCalendar(t, store, other.userID, "Private Google", "Private")

	graph := readGraph(t, owner, baseURL)

	if len(graph.Calendars) != 1 {
		t.Fatalf("the owner sees %d calendars, want exactly their own one", len(graph.Calendars))
	}

	if graph.Calendars[0].ID != ownerCalendar.ID.String() {
		t.Errorf("calendar id = %q, want the owner's own %q", graph.Calendars[0].ID, ownerCalendar.ID)
	}

	for _, calendar := range graph.Calendars {
		if calendar.ID == otherCalendar.ID.String() {
			t.Fatal("the owner's graph carries another account's calendar; the scope did not bound the read")
		}
	}

	if len(graph.Accounts) != 1 || graph.Accounts[0].DisplayName != "Work Google" {
		t.Errorf("accounts = %+v, want only the owner's own connected account", graph.Accounts)
	}
}

func TestARefusalNeverSaysWhetherTheCalendarExists(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	other := signIn(t, store, baseURL, testOtherEmail)

	_, ownerCalendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, otherCalendar := seedAccountCalendar(t, store, other.userID, "Private Google", "Private")

	// The id of a calendar that is really there, under another account,
	// and the id of one that was never there at all. The owner may see
	// neither, and must not be able to tell which is which.
	existsButHidden := otherCalendar.ID.String()
	neverExisted := uuid.NewString()

	// Every position a request can name a calendar in. A refusal has to
	// be the same answer across all of them as well as across the two
	// ids: one call site saying something of its own is the drift the
	// single errCalendarNotFound value exists to stop, and it discloses
	// which check a request got as far as.
	cases := []struct {
		name string
		path string
		body func(calendarID string) string
	}{
		{
			name: "as an edge's source",
			path: "/api/v1/edges",
			body: func(calendarID string) string {
				return fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
					calendarID, ownerCalendar.ID)
			},
		},
		{
			name: "as an edge's target",
			path: "/api/v1/edges",
			body: func(calendarID string) string {
				return fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
					ownerCalendar.ID, calendarID)
			},
		},
		{
			name: "as the calendar a grant shares",
			path: "/api/v1/grants",
			body: func(calendarID string) string {
				return fmt.Sprintf(`{"grantee_email":%q,"calendar_id":%q,"max_detail_level":"busy_block"}`,
					testOtherEmail, calendarID)
			},
		},
	}

	type observed struct {
		where   string
		refusal refusal
	}

	var seen []observed

	for _, testCase := range cases {
		for _, id := range []struct {
			name string
			id   string
		}{
			{"a calendar that exists under another account", existsButHidden},
			{"a calendar that never existed", neverExisted},
		} {
			seen = append(seen, observed{
				where: testCase.name + ", given " + id.name,
				refusal: readRefusal(t, do(t, owner.client, http.MethodPost,
					baseURL+testCase.path, testCase.body(id.id), owner.csrf)),
			})
		}
	}

	for _, got := range seen {
		if got.refusal != seen[0].refusal {
			t.Errorf("%s is refused as %+v, while %s is refused as %+v; a caller can tell the two "+
				"apart, which is an existence oracle", got.where, got.refusal, seen[0].where, seen[0].refusal)
		}
	}

	if seen[0].refusal.status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", seen[0].refusal.status, http.StatusNotFound)
	}
}

func TestARefusalNeverSaysWhetherTheEdgeOrGrantExists(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	other := signIn(t, store, baseURL, testOtherEmail)
	// A third account, so the grant below runs between two accounts the
	// caller is on neither side of. Without it the grant case could only
	// compare two ids that are both absent, which passes however the
	// route words its refusal.
	third := signIn(t, store, baseURL, testThirdEmail)

	_, otherSource := seedAccountCalendar(t, store, other.userID, "Private Google", "Private")
	_, otherTarget := seedAccountCalendar(t, store, other.userID, "Private Google 2", "Mirror")

	// A real grant, created over the real route by its real owner, that
	// the caller is neither the owner nor the grantee of. Its id is a row
	// that exists and that the caller may not see, which is the whole of
	// what the grant case has to be given.
	othersGrant := grantCalendar(t, other, baseURL, otherSource.ID, third, storage.DetailLevelBusyBlock)

	var othersEdge storage.Edge

	if err := store.WithUser(t.Context(), other.userID, func(ctx context.Context) error {
		var err error
		othersEdge, err = store.Edges().Create(ctx, storage.Edge{
			UserID:           other.userID,
			SourceCalendarID: otherSource.ID,
			TargetCalendarID: otherTarget.ID,
			DetailLevel:      storage.DetailLevelFull,
		})

		return err
	}); err != nil {
		t.Fatalf("seed the other account's edge: %v", err)
	}

	cases := []struct {
		name          string
		method        string
		path          string
		body          string
		existingID    string
		nonexistentID string
	}{
		{
			name:          "updating an edge",
			method:        http.MethodPatch,
			path:          "/api/v1/edges/",
			body:          `{"detail_level":"busy_block"}`,
			existingID:    othersEdge.ID.String(),
			nonexistentID: uuid.NewString(),
		},
		{
			name:          "deleting an edge",
			method:        http.MethodDelete,
			path:          "/api/v1/edges/",
			existingID:    othersEdge.ID.String(),
			nonexistentID: uuid.NewString(),
		},
		{
			name:          "revoking a grant",
			method:        http.MethodDelete,
			path:          "/api/v1/grants/",
			existingID:    othersGrant.ID,
			nonexistentID: uuid.NewString(),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			serial(t)

			hidden := readRefusal(t, do(t, owner.client, testCase.method,
				baseURL+testCase.path+testCase.existingID, testCase.body, owner.csrf))
			absent := readRefusal(t, do(t, owner.client, testCase.method,
				baseURL+testCase.path+testCase.nonexistentID, testCase.body, owner.csrf))

			if hidden != absent {
				t.Fatalf("a row under another account is refused as %+v and a nonexistent one as %+v; "+
					"the difference is an existence oracle", hidden, absent)
			}

			if hidden.status != http.StatusNotFound {
				t.Errorf("status = %d, want %d", hidden.status, http.StatusNotFound)
			}
		})
	}
}

func TestACalendarSharedWithYouIsANodeCarryingItsBound(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	grantee := signIn(t, store, baseURL, testOtherEmail)

	_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelTitleTime)

	graph := readGraph(t, grantee, baseURL)

	var found bool

	for _, calendar := range graph.Calendars {
		if calendar.ID != shared.ID.String() {
			continue
		}

		found = true

		if !calendar.Shared {
			t.Error("the shared calendar is not marked shared")
		}

		if calendar.AccountID != nil {
			t.Errorf("account_id = %v, want none: the connection it belongs to is the owner's", *calendar.AccountID)
		}

		if calendar.OwnerEmail == nil || *calendar.OwnerEmail != testEmail {
			t.Errorf("owner_email = %v, want %q", calendar.OwnerEmail, testEmail)
		}

		if calendar.MaxDetailLevel == nil || *calendar.MaxDetailLevel != string(storage.DetailLevelTitleTime) {
			t.Errorf("max_detail_level = %v, want %q", calendar.MaxDetailLevel, storage.DetailLevelTitleTime)
		}
	}

	if !found {
		t.Fatal("a calendar shared with this account is absent from its canvas")
	}
}

func TestAGranteeCannotBuildAnEdgeWiderThanTheGrant(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	grantee := signIn(t, store, baseURL, testOtherEmail)

	_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, granteeTarget := seedAccountCalendar(t, store, grantee.userID, "Private Google", "Private")

	grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelBusyBlock)

	wider := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
		shared.ID, granteeTarget.ID)

	refused := readRefusal(t, do(t, grantee.client, http.MethodPost, baseURL+"/api/v1/edges", wider, grantee.csrf))

	if refused.status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: the grant's own bound is what refuses this",
			refused.status, http.StatusForbidden)
	}

	// Naming the mechanism: this is the message checkWithinGrants writes
	// for sync.ErrGrantExceeded, and no other refusal in this package
	// produces it. A 403 from somewhere else would fail here.
	const exceeded = "this edge would copy more than the share of that calendar allows"
	if refused.message != exceeded {
		t.Errorf("message = %q, want %q", refused.message, exceeded)
	}

	// The same edge inside the bound is accepted, which is what proves
	// the refusal above was about the level and not about the calendar.
	within := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"busy_block"}`,
		shared.ID, granteeTarget.ID)

	resp := do(t, grantee.client, http.MethodPost, baseURL+"/api/v1/edges", within, grantee.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("an edge inside the grant's bound was refused with %d, want %d",
			resp.StatusCode, http.StatusCreated)
	}
}

func TestAnEdgeCannotBeWidenedPastItsGrantAfterwards(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	grantee := signIn(t, store, baseURL, testOtherEmail)

	_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, granteeTarget := seedAccountCalendar(t, store, grantee.userID, "Private Google", "Private")

	grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelBusyBlock)

	created := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"busy_block"}`,
		shared.ID, granteeTarget.ID)

	resp := do(t, grantee.client, http.MethodPost, baseURL+"/api/v1/edges", created, grantee.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create the edge: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var edge struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&edge); err != nil {
		t.Fatalf("decode the created edge: %v", err)
	}

	refused := readRefusal(t, do(t, grantee.client, http.MethodPatch,
		baseURL+"/api/v1/edges/"+edge.ID, `{"detail_level":"title_time"}`, grantee.csrf))

	if refused.status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: widening an edge is exactly the move the grant stops",
			refused.status, http.StatusForbidden)
	}

	const exceeded = "this edge would copy more than the share of that calendar allows"
	if refused.message != exceeded {
		t.Errorf("message = %q, want %q", refused.message, exceeded)
	}
}

func TestTheDetailLevelsComeFromInternalSync(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)

	graph := readGraph(t, owner, baseURL)

	want := []storage.DetailLevel{
		storage.DetailLevelFull,
		storage.DetailLevelTitleTime,
		storage.DetailLevelBusyBlock,
	}

	if len(graph.DetailLevels) != len(want) {
		t.Fatalf("got %d detail levels, want %d", len(graph.DetailLevels), len(want))
	}

	for i, level := range want {
		got := graph.DetailLevels[i]

		if got.Level != string(level) {
			t.Fatalf("detail_levels[%d].level = %q, want %q (widest first)", i, got.Level, level)
		}

		mask, err := sync.MaskFor(level)
		if err != nil {
			t.Fatalf("sync.MaskFor(%q) error = %v", level, err)
		}

		wantFields := make([]string, 0, len(mask.Fields()))
		for _, field := range mask.Fields() {
			wantFields = append(wantFields, field.String())
		}

		if len(got.Fields) != len(wantFields) {
			t.Fatalf("detail_levels[%d].fields = %v, want %v", i, got.Fields, wantFields)
		}

		for j, field := range wantFields {
			if got.Fields[j] != field {
				t.Errorf("detail_levels[%d].fields[%d] = %q, want %q; this API must not answer "+
					"what a level copies from anywhere but internal/sync", i, j, got.Fields[j], field)
			}
		}
	}
}

func TestAnEdgeCanAskCalbridgeToCreateItsTarget(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	body := fmt.Sprintf(
		`{"source_calendar_id":%q,"target":{"account_id":%q,"name":"Mirror of Work"},"detail_level":"busy_block"}`,
		source.ID, account.ID)

	resp := do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", body, owner.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var created struct {
		TargetCalendarID string `json:"target_calendar_id"`
		CreatedCalendar  *struct {
			ID        string  `json:"id"`
			AccountID *string `json:"account_id"`
			Name      string  `json:"name"`
			Managed   bool    `json:"managed"`
		} `json:"created_calendar"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode the created edge: %v", err)
	}

	if created.CreatedCalendar == nil {
		t.Fatal("no created_calendar on the answer, want the calendar calbridge was asked to create")
	}

	if !created.CreatedCalendar.Managed {
		t.Error("the created calendar is not marked managed; a calendar calbridge creates it also owns")
	}

	if created.CreatedCalendar.Name != "Mirror of Work" {
		t.Errorf("name = %q, want the name the request asked for", created.CreatedCalendar.Name)
	}

	if created.TargetCalendarID != created.CreatedCalendar.ID {
		t.Errorf("the edge writes into %q, want the calendar it just created, %q",
			created.TargetCalendarID, created.CreatedCalendar.ID)
	}
}

func TestCalbridgeWillNotCreateACalendarInAnAccountThatIsNotYours(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	other := signIn(t, store, baseURL, testOtherEmail)

	_, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	othersAccount, _ := seedAccountCalendar(t, store, other.userID, "Private Google", "Private")

	hiddenBody := fmt.Sprintf(
		`{"source_calendar_id":%q,"target":{"account_id":%q,"name":"Mirror"},"detail_level":"busy_block"}`,
		source.ID, othersAccount.ID)
	absentBody := fmt.Sprintf(
		`{"source_calendar_id":%q,"target":{"account_id":%q,"name":"Mirror"},"detail_level":"busy_block"}`,
		source.ID, uuid.NewString())

	hidden := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", hiddenBody, owner.csrf))
	absent := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", absentBody, owner.csrf))

	if hidden != absent {
		t.Fatalf("another account's connection is refused as %+v and a nonexistent one as %+v; "+
			"the difference is an existence oracle", hidden, absent)
	}

	if hidden.status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", hidden.status, http.StatusNotFound)
	}

	if store.CountCalendars() != 2 {
		t.Errorf("the store holds %d calendars, want the 2 that were seeded and nothing created", store.CountCalendars())
	}
}

func TestAnEdgeThatClosesALoopIsRefused(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	forward := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
		first.ID, second.ID)

	if resp := do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", forward, owner.csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create the first edge: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	back := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
		second.ID, first.ID)

	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", back, owner.csrf))

	if refused.status != http.StatusConflict {
		t.Fatalf("status = %d, want %d: sync.CheckLoop is what refuses this", refused.status, http.StatusConflict)
	}

	const loop = "this edge would copy a calendar back to itself"
	if refused.message != loop {
		t.Errorf("message = %q, want %q", refused.message, loop)
	}
}

func TestAGrantIsVisibleToBothSidesAndRevocableByEither(t *testing.T) {
	serial(t)

	cases := []struct {
		name    string
		revoker func(owner, grantee *testSession) *testSession
	}{
		{
			name:    "revoked by the owner who shared it",
			revoker: func(owner, _ *testSession) *testSession { return owner },
		},
		{
			name:    "revoked by the account it was shared with",
			revoker: func(_, grantee *testSession) *testSession { return grantee },
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			serial(t)

			store, baseURL := graphHandlers(t)

			owner := signIn(t, store, baseURL, testEmail)
			grantee := signIn(t, store, baseURL, testOtherEmail)

			_, shared := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

			grant := grantCalendar(t, owner, baseURL, shared.ID, grantee, storage.DetailLevelTitleTime)

			ownerGraph := readGraph(t, owner, baseURL)
			if len(ownerGraph.Grants.Given) != 1 || ownerGraph.Grants.Given[0].CounterpartyEmail != testOtherEmail {
				t.Fatalf("the owner's given grants are %+v, want one naming %q",
					ownerGraph.Grants.Given, testOtherEmail)
			}

			granteeGraph := readGraph(t, grantee, baseURL)
			if len(granteeGraph.Grants.Received) != 1 || granteeGraph.Grants.Received[0].CounterpartyEmail != testEmail {
				t.Fatalf("the grantee's received grants are %+v, want one naming %q",
					granteeGraph.Grants.Received, testEmail)
			}

			revoker := testCase.revoker(owner, grantee)

			resp := do(t, revoker.client, http.MethodDelete, baseURL+"/api/v1/grants/"+grant.ID, "", revoker.csrf)
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("revoke: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}

			if store.CountCalendarGrants() != 0 {
				t.Fatalf("the store holds %d grants after the revocation, want none",
					store.CountCalendarGrants())
			}

			after := readGraph(t, grantee, baseURL)
			for _, calendar := range after.Calendars {
				if calendar.ID == shared.ID.String() {
					t.Fatal("the revoked calendar is still a node on the grantee's canvas")
				}
			}
		})
	}
}

func TestAGrantNamingAnAddressWithNoAccountIsRefused(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, calendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	body := fmt.Sprintf(`{"grantee_email":"nobody@example.com","calendar_id":%q,"max_detail_level":"busy_block"}`,
		calendar.ID)

	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/grants", body, owner.csrf))

	if refused.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", refused.status, http.StatusNotFound)
	}

	const noAccount = "no account exists for this email address"
	if refused.message != noAccount {
		t.Errorf("message = %q, want %q", refused.message, noAccount)
	}
}

func TestADetailLevelOutsideThePresetsIsRefused(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	body := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"everything"}`,
		first.ID, second.ID)

	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", body, owner.csrf))

	if refused.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: sync.MaskFor is what refuses an unknown preset",
			refused.status, http.StatusBadRequest)
	}

	const unknown = "choose one of the detail levels this API offers"
	if refused.message != unknown {
		t.Errorf("message = %q, want %q", refused.message, unknown)
	}
}

func TestEveryGraphRouteNeedsASession(t *testing.T) {
	t.Parallel()

	_, baseURL := graphHandlers(t)

	client := cookielessClient(t)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/graph", ""},
		{http.MethodPost, "/api/v1/edges", `{"source_calendar_id":"x","target_calendar_id":"y","detail_level":"full"}`},
		{http.MethodPatch, "/api/v1/edges/" + uuid.NewString(), `{"detail_level":"full"}`},
		{http.MethodDelete, "/api/v1/edges/" + uuid.NewString(), ""},
		{http.MethodPost, "/api/v1/grants", `{"grantee_email":"a@example.com","calendar_id":"x","max_detail_level":"full"}`},
		{http.MethodDelete, "/api/v1/grants/" + uuid.NewString(), ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			t.Parallel()

			refused := readRefusal(t, do(t, client, testCase.method, baseURL+testCase.path, testCase.body, ""))

			if refused.status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d: sessionFromRequest is what refuses this",
					refused.status, http.StatusUnauthorized)
			}

			if refused.message != "log in first" {
				t.Errorf("message = %q, want %q", refused.message, "log in first")
			}
		})
	}
}

func TestAnUnsafeGraphRequestWithoutItsCSRFTokenIsRefused(t *testing.T) {
	serial(t)

	store, baseURL := graphHandlers(t)

	owner := signIn(t, store, baseURL, testEmail)
	_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
	_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

	body := fmt.Sprintf(`{"source_calendar_id":%q,"target_calendar_id":%q,"detail_level":"full"}`,
		first.ID, second.ID)

	// The same session, the same body, the token left off. These routes
	// sit behind the CSRF protector like every other unsafe route, and
	// this is what proves the mounting actually took.
	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges", body, ""))

	if refused.status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: csrf.Protector is what refuses this",
			refused.status, http.StatusForbidden)
	}

	const stale = "cross origin or stale request, please reload"
	if refused.message != stale {
		t.Errorf("message = %q, want %q", refused.message, stale)
	}

	if store.CountEdges() != 0 {
		t.Errorf("the store holds %d edges, want none written by a request the protector refused",
			store.CountEdges())
	}
}

// TestTheGrantRouteIsRateLimited holds down what makes
// handleCreateGrant's "no account exists for this email address" an
// acceptable answer. It is only equivalent to what signup already
// discloses while the two are bounded the same way; unlimited, it would
// be the cheaper way to enumerate the user base by address.
func TestTheGrantRouteIsRateLimited(t *testing.T) {
	serial(t)

	deps := testDeps(t)
	deps.GrantRateLimitBurst = 1

	store := testStore(t, deps)
	_, baseURL := testClient(t, build(t, deps))

	owner := signIn(t, store, baseURL, testEmail)
	_, calendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	// Each attempt names an address no account has, so it spends a token
	// and creates nothing. The sign-in spent nothing of this budget: the
	// login route carries the other limiter.
	attempt := func() int {
		body := fmt.Sprintf(
			`{"grantee_email":%q,"calendar_id":%q,"max_detail_level":"busy_block"}`,
			"nobody-"+uuid.NewString()+"@example.com", calendar.ID)

		return do(t, owner.client, http.MethodPost, baseURL+"/api/v1/grants", body, owner.csrf).StatusCode
	}

	if status := attempt(); status != http.StatusNotFound {
		t.Fatalf("the first attempt answered %d, want %d", status, http.StatusNotFound)
	}

	if status := attempt(); status != http.StatusTooManyRequests {
		t.Errorf("the attempt after the burst answered %d, want %d: the limiter is what refuses this",
			status, http.StatusTooManyRequests)
	}
}

// TestTheGrantRouteAndTheLoginRouteSpendSeparateBudgets is why the grant
// route has a limiter of its own rather than the one signup and login
// carry.
//
// Sharing a calendar is an ordinary authenticated action a person
// repeats, and the enumeration bound on it has nothing to do with the
// session eviction bound on login. On one bucket each becomes the
// other's denial of service, and behind an Ingress with no trusted proxy
// configured every request resolves to the ingress controller, so
// refusing one client refuses all of them.
func TestTheGrantRouteAndTheLoginRouteSpendSeparateBudgets(t *testing.T) {
	t.Run("spending the grant budget leaves a login possible", func(t *testing.T) {
		serial(t)

		deps := testDeps(t)
		deps.GrantRateLimitBurst = 1

		store := testStore(t, deps)
		_, baseURL := testClient(t, build(t, deps))

		owner := signIn(t, store, baseURL, testEmail)
		_, calendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

		share := func() int {
			body := fmt.Sprintf(
				`{"grantee_email":%q,"calendar_id":%q,"max_detail_level":"busy_block"}`,
				"nobody-"+uuid.NewString()+"@example.com", calendar.ID)

			return do(t, owner.client, http.MethodPost, baseURL+"/api/v1/grants", body, owner.csrf).StatusCode
		}

		if status := share(); status != http.StatusNotFound {
			t.Fatalf("the first share answered %d, want %d", status, http.StatusNotFound)
		}

		if status := share(); status != http.StatusTooManyRequests {
			t.Fatalf("the share after the burst answered %d, want %d", status, http.StatusTooManyRequests)
		}

		// The grant budget is gone. A login must still be answered on its
		// merits, which for these credentials is 401.
		status := do(t, cookielessClient(t), http.MethodPost,
			baseURL+"/api/v1/auth/login", wrongCredentials, "").StatusCode

		if status == http.StatusTooManyRequests {
			t.Errorf("a login after the grant budget ran out answered %d: the two routes share a "+
				"bucket, so sharing calendars locks everybody out of logging in", status)
		}

		if status != http.StatusUnauthorized {
			t.Errorf("the login answered %d, want %d", status, http.StatusUnauthorized)
		}
	})

	t.Run("spending the login budget leaves a calendar shareable", func(t *testing.T) {
		serial(t)

		deps := testDeps(t)
		deps.RateLimitBurst = 1

		store := testStore(t, deps)
		_, baseURL := testClient(t, build(t, deps))

		// signIn logs in over the real route, which spends the only
		// token that budget has.
		owner := signIn(t, store, baseURL, testEmail)
		_, calendar := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

		if status := do(t, cookielessClient(t), http.MethodPost,
			baseURL+"/api/v1/auth/login", wrongCredentials, "").StatusCode; status != http.StatusTooManyRequests {
			t.Fatalf("a second login answered %d, want %d; the login budget has to be spent for this "+
				"to prove anything", status, http.StatusTooManyRequests)
		}

		body := fmt.Sprintf(
			`{"grantee_email":%q,"calendar_id":%q,"max_detail_level":"busy_block"}`,
			"nobody-"+uuid.NewString()+"@example.com", calendar.ID)

		status := do(t, owner.client, http.MethodPost, baseURL+"/api/v1/grants", body, owner.csrf).StatusCode

		if status == http.StatusTooManyRequests {
			t.Errorf("sharing a calendar after the login budget ran out answered %d: the grant route "+
				"draws on the login bucket", status)
		}

		if status != http.StatusNotFound {
			t.Errorf("the share answered %d, want %d", status, http.StatusNotFound)
		}
	})
}

// cookielessClient is a client with no session at all, for the routes
// that must refuse one.
func cookielessClient(t *testing.T) *http.Client {
	t.Helper()

	return newCookieClient(t)
}

// grantCalendar shares calendarID with grantee over the real route and
// returns the grant it created.
func grantCalendar(
	t *testing.T,
	owner *testSession,
	baseURL string,
	calendarID uuid.UUID,
	grantee *testSession,
	level storage.DetailLevel,
) grantBody {
	t.Helper()

	body := fmt.Sprintf(`{"grantee_email":%q,"calendar_id":%q,"max_detail_level":%q}`,
		grantee.email, calendarID, level)

	resp := do(t, owner.client, http.MethodPost, baseURL+"/api/v1/grants", body, owner.csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create the grant: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var grant grantBody
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		t.Fatalf("decode the created grant: %v", err)
	}

	return grant
}

// compile-time check that the handler package's Store is satisfied by
// the in-memory fake every test here builds on, so a method added to
// that interface fails here rather than at a cast inside a test.
var _ httpapi.Store = (*storagetest.Store)(nil)
