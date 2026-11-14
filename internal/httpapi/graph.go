// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// This file is the sync graph API: the calendars, edges and grants the
// Vue Flow canvas is a view of. Three rules shape every handler below
// and none of them is optional.
//
// # Every handler runs inside Store.WithUser
//
// inUserScope is the only way a handler here reaches the store, so row
// level security is what decides which rows the handler can see, and the
// handler is not. No handler filters by user id in Go. A read that
// matched nothing matched nothing because the database refused it, which
// is also why a wrong answer here is a bug in a policy rather than a
// missing line in a conditional somebody could delete.
//
// # A refusal never says whether the row exists
//
// Not finding a calendar and not being allowed to see one answer
// identically, down to the status, the code and the message: see
// errCalendarNotFound, errEdgeNotFound and errGrantNotFound, each of
// which is one value used by every path that refuses that kind of id.
// Anything else turns this API into the existence oracle the composite
// foreign keys and the edges trigger were built to close. A handler that
// needs to tell the two apart internally still answers with the same
// error.
//
// The one deliberate exception is the address a grant names: see
// handleCreateGrant.
//
// # internal/sync decides what a detail level means
//
// This package validates a request against a grant; it never interprets
// a level. sync.MaskFor is what says whether a level is a preset at all,
// sync.CheckGrants is what says whether an edge stays inside the grants
// its user holds, and sync.CheckLoop is what refuses a ring. Writing a
// second answer to any of those here would give calbridge two answers to
// one question, and they would diverge.

// maxGraphBody bounds what an edge or a grant request may send. Every
// body here is a handful of uuids, one preset name and at most one
// calendar name, so this is generous for all of them and still small.
const maxGraphBody = 4 << 10

// provisionalExternalIDPrefix is what a calendar calbridge is asked to
// create carries as its provider identifier until a connector has
// actually created it at the provider and can say what the provider
// called it.
//
// calendars.external_id is NOT NULL and non-empty, and no connector is
// wired into this service yet (see AGENTS.md), so the row has to carry
// something and that something has to be recognisable as not yet real. A
// connector's own calendar creation replaces it with the provider's id;
// until then the row records the person's choice of target and nothing
// more. Every id built with this prefix is unique, so the
// calendars_connection_id_external_id_idx uniqueness rule still holds
// between two provisional calendars in the same account.
const provisionalExternalIDPrefix = "calbridge-pending:"

// The refusals this file answers an id it will not resolve with. Each is
// one value rather than a message written at each call site, so "this
// calendar does not exist" and "this calendar is not yours" cannot drift
// into two different answers through an edit to one of them. The
// messages deliberately say nothing about which of the two happened.
var (
	errCalendarNotFound = func() error { return api.NotFound("no such calendar") }
	errEdgeNotFound     = func() error { return api.NotFound("no such edge") }
	errGrantNotFound    = func() error { return api.NotFound("no such grant") }
)

// graphResponse is the whole canvas in one read: the accounts the nodes
// are grouped by, the calendars they stand for, the edges between them,
// the grants on either side of this user, and what each detail level
// preset actually copies.
//
// One endpoint rather than five because the parts have to agree: an edge
// naming a calendar the client has not got yet would render as a dangling
// line, and five calls cannot be made to see one state.
type graphResponse struct {
	Accounts     []accountResponse     `json:"accounts"`
	Calendars    []calendarResponse    `json:"calendars"`
	Edges        []edgeResponse        `json:"edges"`
	Grants       grantsResponse        `json:"grants"`
	DetailLevels []detailLevelResponse `json:"detail_levels"`
}

// accountResponse is one connected provider account. It carries no
// credential and no provider-side identifier: a node's group heading is
// all the canvas needs.
type accountResponse struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name"`
}

// calendarResponse is one node. A calendar this user owns carries its
// AccountID and nothing else; one shared with them carries no account at
// all, because the connection it belongs to is the owner's and this user
// cannot read it, and carries instead who shared it and how much that
// share allows.
type calendarResponse struct {
	ID        string  `json:"id"`
	AccountID *string `json:"account_id"`
	Name      string  `json:"name"`
	Managed   bool    `json:"managed"`
	Shared    bool    `json:"shared"`
	// OwnerEmail and MaxDetailLevel are set on a shared calendar and nil
	// on one of this user's own.
	OwnerEmail     *string `json:"owner_email"`
	MaxDetailLevel *string `json:"max_detail_level"`
}

// edgeResponse is one edge this user owns.
type edgeResponse struct {
	ID               string `json:"id"`
	SourceCalendarID string `json:"source_calendar_id"`
	TargetCalendarID string `json:"target_calendar_id"`
	DetailLevel      string `json:"detail_level"`
	// CreatedCalendar is set only on the answer to a create that asked
	// calbridge for its target, so the client can place the new node
	// without reading the whole graph again.
	CreatedCalendar *calendarResponse `json:"created_calendar,omitempty"`
}

// grantsResponse splits the grants this user is party to by side,
// because the two mean different things to the person reading them:
// Given is what they shared out and may revoke, Received is what they
// may build an edge against.
type grantsResponse struct {
	Given    []grantResponse `json:"given"`
	Received []grantResponse `json:"received"`
}

// grantResponse is one grant. CounterpartyEmail is the other side's
// address, the grantee's on a given grant and the owner's on a received
// one; see storage.GrantCounterparty for why that address and nothing
// else comes back.
type grantResponse struct {
	ID                string `json:"id"`
	CalendarID        string `json:"calendar_id"`
	CounterpartyEmail string `json:"counterparty_email"`
	MaxDetailLevel    string `json:"max_detail_level"`
}

// detailLevelResponse is one preset and the event fields it copies. The
// field list comes from sync.MaskFor, so the screen that offers a preset
// and the engine that applies it are reading the same answer; a client
// that hard coded the list would be the second answer this package
// exists not to have.
type detailLevelResponse struct {
	Level  string   `json:"level"`
	Fields []string `json:"fields"`
}

// detailLevelsWidestFirst is the order the presets are offered in. They
// nest (see sync.FieldMask), so widest first lets a client cap a choice
// at a grant's maximum by taking everything from that entry onwards.
var detailLevelsWidestFirst = [...]storage.DetailLevel{
	storage.DetailLevelFull,
	storage.DetailLevelTitleTime,
	storage.DetailLevelBusyBlock,
}

// createEdgeRequest is handleCreateEdge's body. Exactly one of
// TargetCalendarID and Target is set: an edge writes either into a
// calendar that already exists or into one calbridge creates for it,
// which is the per-edge choice docs/concepts/calbridge.md records.
type createEdgeRequest struct {
	SourceCalendarID string            `json:"source_calendar_id"`
	TargetCalendarID string            `json:"target_calendar_id"`
	Target           *createTargetBody `json:"target"`
	DetailLevel      string            `json:"detail_level"`
}

// createTargetBody asks calbridge to create the target calendar, in one
// of this user's own accounts.
type createTargetBody struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
}

// updateEdgeRequest is handleUpdateEdge's body. The detail level is the
// only thing an edge's editor changes: moving an edge to another
// calendar is a different edge, and the mapping rows the old one wrote
// would have to be torn down for it, which is internal/sync's work.
type updateEdgeRequest struct {
	DetailLevel string `json:"detail_level"`
}

// createGrantRequest is handleCreateGrant's body.
type createGrantRequest struct {
	GranteeEmail   string `json:"grantee_email"`
	CalendarID     string `json:"calendar_id"`
	MaxDetailLevel string `json:"max_detail_level"`
}

// inUserScope is the one way every handler in this file reaches the
// store. It resolves the session to a user id and runs fn inside
// Store.WithUser, so the transaction fn's context carries is scoped and
// row level security is what bounds every statement inside it.
//
// There is deliberately no unscoped variant and no "scope optional"
// parameter: a handler that could run outside a scope is a handler that
// one day will, and the repositories would then answer
// storage.ErrNoUserScope rather than quietly returning somebody else's
// rows, which is a failure mode worth never reaching in the first place.
//
// A handler that reads a request body decodes it inside fn, not before
// calling this, so the session is checked before anything else about the
// request is. Decoding first would answer a caller with no session at
// all whatever the body happened to be wrong about, which both leaks the
// order of the checks and lets an unauthenticated request spend work.
func (a *app) inUserScope(r *http.Request, fn func(ctx context.Context, userID uuid.UUID) error) error {
	s, err := a.sessionFromRequest(r)
	if err != nil {
		return err
	}

	userID, err := uuid.Parse(s.Subject)
	if err != nil {
		// Every session this application starts carries a users row id
		// as its subject (see auth.Identity.Session), so anything else
		// is a session from a build that did not.
		return fmt.Errorf("httpapi: session subject is not a user id: %w", err)
	}

	return a.store.WithUser(r.Context(), userID, func(ctx context.Context) error {
		return fn(ctx, userID)
	})
}

// handleGraph answers the whole canvas.
func (a *app) handleGraph(w http.ResponseWriter, r *http.Request) error {
	var body graphResponse

	if err := a.inUserScope(r, func(ctx context.Context, userID uuid.UUID) error {
		built, err := a.buildGraph(ctx, userID)
		if err != nil {
			return err
		}

		body = built

		return nil
	}); err != nil {
		return err
	}

	return api.Write(w, http.StatusOK, body)
}

// buildGraph reads every part of the canvas inside one scoped
// transaction, so the parts it returns are one state rather than several
// taken a moment apart.
func (a *app) buildGraph(ctx context.Context, userID uuid.UUID) (graphResponse, error) {
	connections, err := a.store.Connections().List(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list connections: %w", err)
	}

	own, err := a.store.Calendars().List(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list calendars: %w", err)
	}

	shared, err := a.store.Sharing().ListCalendarsSharedWithMe(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list shared calendars: %w", err)
	}

	edges, err := a.store.Edges().List(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list edges: %w", err)
	}

	grants, err := a.store.CalendarGrants().List(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list grants: %w", err)
	}

	counterparties, err := a.store.Sharing().ListGrantCounterparties(ctx)
	if err != nil {
		return graphResponse{}, fmt.Errorf("httpapi: list grant counterparties: %w", err)
	}

	emails := make(map[uuid.UUID]string, len(counterparties))
	for _, counterparty := range counterparties {
		emails[counterparty.UserID] = counterparty.Email
	}

	body := graphResponse{
		Accounts:     make([]accountResponse, 0, len(connections)),
		Calendars:    make([]calendarResponse, 0, len(own)+len(shared)),
		Edges:        make([]edgeResponse, 0, len(edges)),
		DetailLevels: make([]detailLevelResponse, 0, len(detailLevelsWidestFirst)),
	}
	body.Grants.Given = []grantResponse{}
	body.Grants.Received = []grantResponse{}

	for _, connection := range connections {
		body.Accounts = append(body.Accounts, accountResponse{
			ID:          connection.ID.String(),
			Provider:    string(connection.Provider),
			DisplayName: connection.DisplayName,
		})
	}

	for _, calendar := range own {
		accountID := calendar.ConnectionID.String()
		body.Calendars = append(body.Calendars, calendarResponse{
			ID:        calendar.ID.String(),
			AccountID: &accountID,
			Name:      calendar.Name,
			Managed:   calendar.Managed,
		})
	}

	for _, calendar := range shared {
		level := string(calendar.MaxDetailLevel)
		node := calendarResponse{
			ID:             calendar.ID.String(),
			Name:           calendar.Name,
			Managed:        calendar.Managed,
			Shared:         true,
			MaxDetailLevel: &level,
		}

		if email, ok := emails[calendar.UserID]; ok {
			node.OwnerEmail = &email
		}

		body.Calendars = append(body.Calendars, node)
	}

	for _, edge := range edges {
		body.Edges = append(body.Edges, edgeFor(edge))
	}

	for _, grant := range grants {
		rendered := grantResponse{
			ID:             grant.ID.String(),
			CalendarID:     grant.CalendarID.String(),
			MaxDetailLevel: string(grant.MaxDetailLevel),
		}

		if grant.OwnerUserID == userID {
			rendered.CounterpartyEmail = emails[grant.GranteeUserID]
			body.Grants.Given = append(body.Grants.Given, rendered)

			continue
		}

		rendered.CounterpartyEmail = emails[grant.OwnerUserID]
		body.Grants.Received = append(body.Grants.Received, rendered)
	}

	levels, err := detailLevels()
	if err != nil {
		return graphResponse{}, err
	}

	body.DetailLevels = levels

	return body, nil
}

// detailLevels renders each preset with the fields it copies, read from
// internal/sync rather than restated here.
func detailLevels() ([]detailLevelResponse, error) {
	rendered := make([]detailLevelResponse, 0, len(detailLevelsWidestFirst))

	for _, level := range detailLevelsWidestFirst {
		mask, err := sync.MaskFor(level)
		if err != nil {
			return nil, fmt.Errorf("httpapi: detail level %q: %w", string(level), err)
		}

		fields := make([]string, 0, len(mask.Fields()))
		for _, field := range mask.Fields() {
			fields = append(fields, field.String())
		}

		rendered = append(rendered, detailLevelResponse{Level: string(level), Fields: fields})
	}

	return rendered, nil
}

// handleCreateEdge draws one edge, either into an existing calendar or
// into one it creates for the purpose.
func (a *app) handleCreateEdge(w http.ResponseWriter, r *http.Request) error {
	var created edgeResponse

	if err := a.inUserScope(r, func(ctx context.Context, userID uuid.UUID) error {
		var req createEdgeRequest
		if err := api.DecodeLimit(r, &req, maxGraphBody); err != nil {
			return err
		}

		level, err := detailLevelFrom(req.DetailLevel)
		if err != nil {
			return err
		}

		sourceID, err := calendarIDFrom(req.SourceCalendarID)
		if err != nil {
			return err
		}

		if (req.TargetCalendarID == "") == (req.Target == nil) {
			return api.Invalid("name either an existing target calendar or the account to create one in, not both")
		}

		reach, err := a.reachable(ctx)
		if err != nil {
			return err
		}

		if !reach.has(sourceID) {
			return errCalendarNotFound()
		}

		targetID, createdCalendar, err := a.resolveTarget(ctx, userID, req, reach)
		if err != nil {
			return err
		}

		edge := storage.Edge{
			UserID:           userID,
			SourceCalendarID: sourceID,
			TargetCalendarID: targetID,
			DetailLevel:      level,
		}

		if err := a.checkWithinGrants(ctx, edge, reach); err != nil {
			return err
		}

		existing, err := a.store.Edges().List(ctx)
		if err != nil {
			return fmt.Errorf("httpapi: list edges: %w", err)
		}

		if err := sync.CheckLoop(existing, edge); err != nil {
			if errors.Is(err, sync.ErrLoop) {
				return api.Conflict("this edge would copy a calendar back to itself")
			}

			return fmt.Errorf("httpapi: check loop: %w", err)
		}

		stored, err := a.store.Edges().Create(ctx, edge)
		if err != nil {
			return edgeWriteError(err)
		}

		created = edgeFor(stored)
		created.CreatedCalendar = createdCalendar

		return nil
	}); err != nil {
		return err
	}

	return api.Write(w, http.StatusCreated, created)
}

// resolveTarget answers which calendar the new edge writes into: the one
// the request named, which has to be reachable like the source, or a new
// one calbridge creates in an account this user owns.
func (a *app) resolveTarget(
	ctx context.Context,
	userID uuid.UUID,
	req createEdgeRequest,
	reach reachableCalendars,
) (uuid.UUID, *calendarResponse, error) {
	if req.Target == nil {
		targetID, err := calendarIDFrom(req.TargetCalendarID)
		if err != nil {
			return uuid.Nil, nil, err
		}

		if !reach.has(targetID) {
			return uuid.Nil, nil, errCalendarNotFound()
		}

		return targetID, nil, nil
	}

	accountID, err := uuid.Parse(req.Target.AccountID)
	if err != nil {
		return uuid.Nil, nil, api.Invalid("name the account the target calendar should be created in")
	}

	if req.Target.Name == "" {
		return uuid.Nil, nil, api.Invalid("give the target calendar a name")
	}

	// The account has to be one this user owns, and the read that decides
	// that is scoped, so an account id belonging to somebody else and one
	// that was never real are refused with the same answer.
	if _, err := a.store.Connections().FindByID(ctx, accountID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return uuid.Nil, nil, api.NotFound("no such account")
		}

		return uuid.Nil, nil, fmt.Errorf("httpapi: find account: %w", err)
	}

	calendar, err := a.store.Calendars().Create(ctx, storage.Calendar{
		UserID:       userID,
		ConnectionID: accountID,
		ExternalID:   provisionalExternalIDPrefix + uuid.NewString(),
		Name:         req.Target.Name,
		Managed:      true,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return uuid.Nil, nil, api.NotFound("no such account")
		}

		return uuid.Nil, nil, fmt.Errorf("httpapi: create target calendar: %w", err)
	}

	reach.own[calendar.ID] = struct{}{}

	renderedAccountID := calendar.ConnectionID.String()

	return calendar.ID, &calendarResponse{
		ID:        calendar.ID.String(),
		AccountID: &renderedAccountID,
		Name:      calendar.Name,
		Managed:   calendar.Managed,
	}, nil
}

// handleUpdateEdge changes an edge's detail level, and nothing else.
func (a *app) handleUpdateEdge(w http.ResponseWriter, r *http.Request) error {
	var updated edgeResponse

	if err := a.inUserScope(r, func(ctx context.Context, _ uuid.UUID) error {
		edgeID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			// An id that is not a uuid cannot name a row, and saying so
			// is not a disclosure: it is true of every id of that shape,
			// whoever asks.
			return errEdgeNotFound()
		}

		var req updateEdgeRequest
		if err := api.DecodeLimit(r, &req, maxGraphBody); err != nil {
			return err
		}

		level, err := detailLevelFrom(req.DetailLevel)
		if err != nil {
			return err
		}

		edge, err := a.store.Edges().FindByID(ctx, edgeID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return errEdgeNotFound()
			}

			return fmt.Errorf("httpapi: find edge: %w", err)
		}

		reach, err := a.reachable(ctx)
		if err != nil {
			return err
		}

		// The grant is re-checked against the level being asked for, not
		// the one already stored: widening an edge is exactly the move a
		// grant has to stop.
		edge.DetailLevel = level
		if err := a.checkWithinGrants(ctx, edge, reach); err != nil {
			return err
		}

		stored, err := a.store.Edges().UpdateDetailLevel(ctx, edgeID, level)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return errEdgeNotFound()
			}

			return fmt.Errorf("httpapi: update edge: %w", err)
		}

		updated = edgeFor(stored)

		return nil
	}); err != nil {
		return err
	}

	return api.Write(w, http.StatusOK, updated)
}

// handleDeleteEdge removes one edge.
//
// It removes the row and nothing else. Every event the edge wrote is
// taken back by internal/sync's own teardown, which is where that
// belongs: it has the connectors and the mapping rows, and a second
// implementation here would be a second answer to what an edge left
// behind. See internal/sync's package doc.
func (a *app) handleDeleteEdge(w http.ResponseWriter, r *http.Request) error {
	if err := a.inUserScope(r, func(ctx context.Context, _ uuid.UUID) error {
		edgeID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return errEdgeNotFound()
		}

		if err := a.store.Edges().Delete(ctx, edgeID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return errEdgeNotFound()
			}

			return fmt.Errorf("httpapi: delete edge: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	api.NoContent(w)

	return nil
}

// handleCreateGrant shares one of this user's calendars with another
// account, up to a maximum detail level.
//
// Naming the grantee by address is the one place in this file where an
// answer depends on whether a row exists: an address with no account is
// refused as such. That discloses nothing new, because signup already
// answers "an account already exists for this email address" (see
// signUpError), so whether an address has an account is known to anyone
// who can reach the signup route. Collapsing it here while leaving it
// open there would buy nothing and would leave a person unable to tell a
// mistyped address from a share that worked.
func (a *app) handleCreateGrant(w http.ResponseWriter, r *http.Request) error {
	var created grantResponse

	if err := a.inUserScope(r, func(ctx context.Context, userID uuid.UUID) error {
		var req createGrantRequest
		if err := api.DecodeLimit(r, &req, maxGraphBody); err != nil {
			return err
		}

		level, err := detailLevelFrom(req.MaxDetailLevel)
		if err != nil {
			return err
		}

		calendarID, err := calendarIDFrom(req.CalendarID)
		if err != nil {
			return err
		}

		if req.GranteeEmail == "" {
			return api.Invalid("enter the email address of the account to share with")
		}

		// Deliberately against the pool rather than the scoped
		// transaction: it answers for an address that is not the
		// caller's own, which no scope would admit. See
		// storage.SharingRepository.
		granteeID, err := a.store.Sharing().FindUserIDByEmail(ctx, req.GranteeEmail)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return api.NotFound("no account exists for this email address")
			}

			return fmt.Errorf("httpapi: find grantee: %w", err)
		}

		if granteeID == userID {
			return api.Invalid("a calendar cannot be shared with its own account")
		}

		// Scoped, so a calendar belonging to somebody else and one that
		// never existed are refused identically, and a grant can only
		// ever name a calendar this user actually owns.
		if _, err := a.store.Calendars().FindByID(ctx, calendarID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return errCalendarNotFound()
			}

			return fmt.Errorf("httpapi: find calendar: %w", err)
		}

		grant, err := a.store.CalendarGrants().Create(ctx, storage.CalendarGrant{
			OwnerUserID:    userID,
			GranteeUserID:  granteeID,
			CalendarID:     calendarID,
			MaxDetailLevel: level,
		})
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrConflict):
				return api.Conflict("this calendar is already shared with that account")
			case errors.Is(err, storage.ErrNotFound):
				return errCalendarNotFound()
			default:
				return fmt.Errorf("httpapi: create grant: %w", err)
			}
		}

		created = grantResponse{
			ID:                grant.ID.String(),
			CalendarID:        grant.CalendarID.String(),
			CounterpartyEmail: req.GranteeEmail,
			MaxDetailLevel:    string(grant.MaxDetailLevel),
		}

		return nil
	}); err != nil {
		return err
	}

	return api.Write(w, http.StatusCreated, created)
}

// handleDeleteGrant revokes one grant. Either side may do it, matching
// calendar_grants' own policy.
//
// It removes the grant row and nothing else, and that is deliberate: the
// edges the grant covered belong to the grantee and are invisible to the
// owner's scope, so the owner's own transaction could not reach them
// even to try. internal/sync's EnforceGrants is what tears them down and
// takes back what they wrote, scoped to the grantee, which is the only
// scope that can; see docs/adr/0002-a-revoked-grant-deletes-the-edge.md
// for why that teardown is the agreed consequence rather than leaving a
// narrowed edge standing.
func (a *app) handleDeleteGrant(w http.ResponseWriter, r *http.Request) error {
	if err := a.inUserScope(r, func(ctx context.Context, _ uuid.UUID) error {
		grantID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return errGrantNotFound()
		}

		if err := a.store.CalendarGrants().Delete(ctx, grantID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return errGrantNotFound()
			}

			return fmt.Errorf("httpapi: delete grant: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	api.NoContent(w)

	return nil
}

// reachableCalendars is which calendars the current scope may name in an
// edge: the ones it owns, and the ones a grant shares with it. It is
// built from two scoped reads, so membership is decided by row level
// security and not by anything written here.
type reachableCalendars struct {
	own    map[uuid.UUID]struct{}
	shared map[uuid.UUID]struct{}
}

// has reports whether the calendar may be named at all. A calendar that
// is not in either set is refused exactly as one that does not exist is;
// see this file's own doc comment.
func (c reachableCalendars) has(id uuid.UUID) bool {
	_, own := c.own[id]
	_, shared := c.shared[id]

	return own || shared
}

// reachable reads both halves inside the scope the caller already opened.
func (a *app) reachable(ctx context.Context) (reachableCalendars, error) {
	own, err := a.store.Calendars().List(ctx)
	if err != nil {
		return reachableCalendars{}, fmt.Errorf("httpapi: list calendars: %w", err)
	}

	shared, err := a.store.Sharing().ListCalendarsSharedWithMe(ctx)
	if err != nil {
		return reachableCalendars{}, fmt.Errorf("httpapi: list shared calendars: %w", err)
	}

	reach := reachableCalendars{
		own:    make(map[uuid.UUID]struct{}, len(own)),
		shared: make(map[uuid.UUID]struct{}, len(shared)),
	}

	for _, calendar := range own {
		reach.own[calendar.ID] = struct{}{}
	}

	for _, calendar := range shared {
		reach.shared[calendar.ID] = struct{}{}
	}

	return reach, nil
}

// checkWithinGrants refuses an edge the grants this user holds do not
// cover. The decision is sync.CheckGrants', not this package's: this
// only assembles the coverage from scoped reads and turns the two
// refusals into answers.
//
// Both of them answer 403 rather than 404, and that is not a disclosure:
// the caller has already been shown, by reachable, that it may name
// these calendars at all. What is being refused here is the amount of
// detail, which is the grantor's own choice and the thing the grantee
// has to be able to see in order to pick a level that works.
func (a *app) checkWithinGrants(ctx context.Context, edge storage.Edge, reach reachableCalendars) error {
	grants, err := a.store.CalendarGrants().List(ctx)
	if err != nil {
		return fmt.Errorf("httpapi: list grants: %w", err)
	}

	_, ownsSource := reach.own[edge.SourceCalendarID]
	_, ownsTarget := reach.own[edge.TargetCalendarID]

	err = sync.CheckGrants(edge, sync.Coverage{
		OwnsSource: ownsSource,
		OwnsTarget: ownsTarget,
		Grants:     grants,
	})

	switch {
	case err == nil:
		return nil
	case errors.Is(err, sync.ErrGrantExceeded):
		return api.PermissionDenied("this edge would copy more than the share of that calendar allows")
	case errors.Is(err, sync.ErrGrantMissing):
		// reachable already admitted both calendars, so a grant that is
		// missing now means it was revoked between the two reads. The
		// caller is told the same thing as for an unknown calendar.
		return errCalendarNotFound()
	default:
		return fmt.Errorf("httpapi: check grants: %w", err)
	}
}

// detailLevelFrom validates a preset name through internal/sync, the one
// package that decides what a detail level is.
func detailLevelFrom(value string) (storage.DetailLevel, error) {
	level := storage.DetailLevel(value)

	if _, err := sync.MaskFor(level); err != nil {
		if errors.Is(err, sync.ErrInvalidDetailLevel) {
			return "", api.Invalid("choose one of the detail levels this API offers")
		}

		return "", fmt.Errorf("httpapi: detail level: %w", err)
	}

	return level, nil
}

// calendarIDFrom parses a calendar id, answering an unparseable one the
// same way an unreachable one is answered.
func calendarIDFrom(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, errCalendarNotFound()
	}

	return id, nil
}

// edgeWriteError turns a refused edge write into an answer. The database
// trigger behind edges refuses a calendar this user neither owns nor
// holds a grant over, which reaches here as storage.ErrNotFound: the
// reads above already checked the same thing, so getting here means the
// coverage changed underneath this request, and the caller is told what
// it would have been told a moment earlier.
// There is deliberately no "this edge already exists" branch: edges
// carries no uniqueness rule over (user, source, target), so two edges
// between the same pair of calendars at different detail levels are a
// shape the schema allows, and a message claiming otherwise would be
// describing a constraint that is not there.
func edgeWriteError(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return errCalendarNotFound()
	}

	return fmt.Errorf("httpapi: create edge: %w", err)
}

// edgeFor renders one stored edge.
func edgeFor(edge storage.Edge) edgeResponse {
	return edgeResponse{
		ID:               edge.ID.String(),
		SourceCalendarID: edge.SourceCalendarID.String(),
		TargetCalendarID: edge.TargetCalendarID.String(),
		DetailLevel:      string(edge.DetailLevel),
	}
}
