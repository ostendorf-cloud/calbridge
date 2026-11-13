// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// syncCollection is the report body of RFC 6578 section 3.2. The
// DAV:limit element is left out: a server is free to ignore it, and the
// connector reads whatever size of page the server chose to hand over.
type syncCollection struct {
	XMLName xml.Name    `xml:"DAV: sync-collection"`
	Token   string      `xml:"DAV: sync-token"`
	Level   string      `xml:"DAV: sync-level"`
	Prop    syncProps   `xml:"DAV: prop"`
	Limit   *limitProps `xml:"DAV: limit,omitempty"`
}

// syncProps names the one property a sync report asks for. The event
// bodies come from the multiget that follows, because a sync report that
// carries them cannot be asked for in a form every server answers.
type syncProps struct {
	GetETag struct{} `xml:"DAV: getetag"`
}

// limitProps is the DAV:limit element of RFC 5323 section 5.17, which
// RFC 6578 reuses to bound a page.
type limitProps struct {
	NResults int `xml:"DAV: nresults"`
}

// calendarMultiget is the report body of RFC 4791 section 7.9.
type calendarMultiget struct {
	XMLName xml.Name     `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget"`
	Prop    multigetProp `xml:"DAV: prop"`
	Href    []string     `xml:"DAV: href"`
}

// multigetProp names the properties a multiget asks for.
type multigetProp struct {
	GetETag      struct{} `xml:"DAV: getetag"`
	CalendarData struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
}

// syncPageSize is how many results the connector asks a server to put in
// one page, where the profile says the server honours a limit. It is
// large enough that a quiet calendar comes back in one round trip and
// small enough that a first sync of a busy one does not arrive as a
// single body.
const syncPageSize = 500

// Changes reads what changed in the calendar since token.
//
// The three answers RFC 6578 allows map onto the three the interface
// documents, and the connector invents no fourth:
//
//   - A complete answer is provider.PageStateComplete.
//   - A truncated answer, which the server reports as a 507 on the
//     request URI with the DAV:number-of-matches-within-limits
//     precondition, is provider.PageStateResumable. Section 3.2 makes
//     the token that comes with it the correct position for the partial
//     set, so the caller stores it and resumes from it.
//   - A rejected token, which the server reports with the
//     DAV:valid-sync-token precondition, is provider.ErrSyncTokenInvalid,
//     and the caller restarts from an initial sync.
func (c *Connector) Changes(
	ctx context.Context,
	cred provider.Credential,
	calendarID string,
	token provider.SyncToken,
) (provider.ChangePage, error) {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return provider.ChangePage{}, err
	}
	calendar, err := parseCalendarURL(p, calendarID)
	if err != nil {
		return provider.ChangePage{}, err
	}

	page, err := c.syncReport(ctx, p, own, calendar, token)
	if err != nil {
		return provider.ChangePage{}, err
	}

	upserts, err := c.readResources(ctx, p, own, calendar, page.changed)
	if err != nil {
		return provider.ChangePage{}, err
	}

	out := provider.ChangePage{
		Changes: append(upserts, page.deletes...),
		Token:   provider.SyncToken{Value: page.token},
		State:   page.state,
	}
	if err := out.Validate(); err != nil {
		return provider.ChangePage{}, err
	}

	return out, nil
}

// syncPage is what one sync report said: the resources to read, the
// resources that are gone, the token, and what the caller does next.
type syncPage struct {
	changed []string
	deletes []provider.Change
	token   string
	state   provider.PageState
}

// syncReport sends one DAV:sync-collection report and reads its answer.
func (c *Connector) syncReport(
	ctx context.Context,
	p Profile,
	cred Credential,
	calendar *url.URL,
	token provider.SyncToken,
) (syncPage, error) {
	body := syncCollection{Token: token.Value, Level: "1"}
	if !p.Quirks.IgnoresSyncLimit {
		body.Limit = &limitProps{NResults: syncPageSize}
	}
	raw, err := marshalBody(body)
	if err != nil {
		return syncPage{}, err
	}

	res, err := c.do(ctx, p, cred, request{
		method:      "REPORT",
		url:         calendar,
		headers:     map[string]string{"Depth": "0"},
		body:        raw,
		contentType: contentTypeXML,
	})
	if err != nil {
		return syncPage{}, err
	}
	if err := rejectedToken(p, res, token); err != nil {
		return syncPage{}, err
	}
	if err := expect(res, "REPORT sync-collection", http.StatusMultiStatus); err != nil {
		return syncPage{}, err
	}
	ms, err := parseMultistatus(res, "REPORT sync-collection")
	if err != nil {
		return syncPage{}, err
	}
	if ms.Error != nil && ms.Error.ValidSyncToken != nil {
		return syncPage{}, fmt.Errorf("caldav profile %q rejected the sync token: %w", p.Name, provider.ErrSyncTokenInvalid)
	}

	return readSyncPage(p, calendar, ms)
}

// readSyncPage walks a sync report's multistatus.
func readSyncPage(p Profile, calendar *url.URL, ms multistatus) (syncPage, error) {
	page := syncPage{token: ms.SyncToken, state: provider.PageStateComplete}
	for _, r := range ms.Responses {
		at, err := resolve(p, calendar, r.Href)
		if err != nil {
			return syncPage{}, err
		}
		if truncated(r) {
			// RFC 6578 section 3.2: the server cut the answer short under
			// its own limit, and the token it returned is the correct
			// position for what it did hand over.
			page.state = provider.PageStateResumable

			continue
		}
		if sameCollection(at, calendar) {
			if !p.Quirks.SyncReportsCollection {
				return syncPage{}, fmt.Errorf("caldav sync report lists the collection itself: %w", ErrServer)
			}

			continue
		}
		name := lastSegment(at.Path)
		if name == "" {
			continue
		}
		if r.gone() {
			page.deletes = append(page.deletes, provider.Change{Kind: provider.ChangeKindDelete, EventID: name})

			continue
		}
		page.changed = append(page.changed, name)
	}

	if page.token == "" {
		return syncPage{}, fmt.Errorf("caldav sync report carried no DAV:sync-token: %w", ErrServer)
	}

	return page, nil
}

// truncated reports whether the response is the server saying it cut the
// answer short, which RFC 6578 section 3.2 carries as a 507 with the
// DAV:number-of-matches-within-limits precondition.
func truncated(r davResp) bool {
	if code, ok := statusCode(r.Status); ok && code == http.StatusInsufficientStorage {
		return true
	}

	return r.Error != nil && r.Error.NumberOfMatchesWithinLimits != nil
}

// rejectedToken turns the server's refusal of a token into the error the
// interface defines for it. A refusal arrives as a precondition failure,
// which RFC 4918 section 16 carries on a 403 unless the condition says
// otherwise, and servers differ on whether they use 403, 409 or 412.
// Only a request that carried a token is read this way.
func rejectedToken(p Profile, res result, token provider.SyncToken) error {
	if token.IsZero() {
		return nil
	}
	switch res.status {
	case http.StatusForbidden, http.StatusConflict, http.StatusPreconditionFailed:
	default:
		return nil
	}

	var problems struct {
		XMLName        xml.Name  `xml:"DAV: error"`
		ValidSyncToken *struct{} `xml:"DAV: valid-sync-token"`
	}
	if err := xml.Unmarshal(res.body, &problems); err != nil || problems.ValidSyncToken == nil {
		// Only a body that parses and names DAV:valid-sync-token says the
		// token was the problem. A refusal naming another precondition,
		// an empty body, or an error page from something between here and
		// the server, all leave the stored position good: discarding it
		// on a guess costs a full initial sync for nothing.
		return fmt.Errorf("caldav REPORT sync-collection answered %d %s: %w",
			res.status, http.StatusText(res.status), ErrServer)
	}

	return fmt.Errorf("caldav profile %q rejected the sync token: %w", p.Name, provider.ErrSyncTokenInvalid)
}

// readResources fetches the calendar objects the sync report named and
// turns them into upserts. One resource holds one UID, so it yields the
// recurring master and one event for each occurrence that was
// overridden.
func (c *Connector) readResources(
	ctx context.Context,
	p Profile,
	cred Credential,
	calendar *url.URL,
	names []string,
) ([]provider.Change, error) {
	if len(names) == 0 {
		return nil, nil
	}

	body := calendarMultiget{}
	for _, name := range names {
		at, err := resourceURL(calendar, name)
		if err != nil {
			return nil, err
		}
		body.Href = append(body.Href, at.EscapedPath())
	}
	raw, err := marshalBody(body)
	if err != nil {
		return nil, err
	}

	res, err := c.do(ctx, p, cred, request{
		method:      "REPORT",
		url:         calendar,
		headers:     map[string]string{"Depth": "1"},
		body:        raw,
		contentType: contentTypeXML,
	})
	if err != nil {
		return nil, err
	}
	if err := expect(res, "REPORT calendar-multiget", http.StatusMultiStatus); err != nil {
		return nil, err
	}
	ms, err := parseMultistatus(res, "REPORT calendar-multiget")
	if err != nil {
		return nil, err
	}

	var out []provider.Change
	for _, r := range ms.Responses {
		at, err := resolve(p, calendar, r.Href)
		if err != nil {
			return nil, err
		}
		if r.gone() {
			// The resource went away between the sync report and this
			// read. The next run reports the removal, and reporting it
			// here as a deletion would contradict an initial sync, which
			// reports no deletion at all.
			continue
		}
		prop := r.prop()
		if prop.CalendarData == "" {
			// The sync report named this resource as changed and the
			// multiget then handed over no object for it. Skipping it
			// would return a page the caller reads as complete, store its
			// token, and never see the change again, which is the one
			// outcome this connector exists to prevent.
			return nil, fmt.Errorf(
				"caldav server named %q as changed and returned no calendar data for it: %w",
				lastSegment(at.Path), ErrServer)
		}
		events, err := decodeResource(lastSegment(at.Path), prop.CalendarData)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			event.ETag = prop.GetETag
			out = append(out, provider.Change{Kind: provider.ChangeKindUpsert, EventID: event.ID, Event: event})
		}
	}

	return out, nil
}

// resourceURL builds the URL of one object resource inside a collection,
// and refuses a name that would leave it.
func resourceURL(calendar *url.URL, name string) (*url.URL, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("caldav event id %q is not a resource name: %w", name, provider.ErrUnknownEvent)
	}
	out := *calendar
	out.Path = calendar.Path + name
	out.RawPath = ""

	return &out, nil
}
