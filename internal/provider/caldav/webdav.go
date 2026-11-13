// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// The structures below carry the two XML namespaces WebDAV and CalDAV
// define in their element tags: "DAV:" from RFC 4918 section 21 and
// "urn:ietf:params:xml:ns:caldav" from RFC 4791 section 3.

// maxBodyBytes bounds a response body the connector reads into memory,
// so a server cannot exhaust the process by answering a sync report with
// an endless multistatus.
const maxBodyBytes = 64 << 20

// newUID returns the UID of an event this connector creates. RFC 5545
// section 3.8.4.7 asks for a globally unique value.
func newUID() string { return uuid.NewString() }

// multistatus is the body of a 207 answer (RFC 4918 section 13). The
// DAV:sync-token child is RFC 6578 section 6.2's addition to it.
type multistatus struct {
	XMLName   xml.Name     `xml:"DAV: multistatus"`
	Responses []davResp    `xml:"DAV: response"`
	SyncToken string       `xml:"DAV: sync-token"`
	Error     *davProblems `xml:"DAV: error"`
}

// davResp is one member's answer inside a multistatus.
type davResp struct {
	Href     string       `xml:"DAV: href"`
	Propstat []davPropsta `xml:"DAV: propstat"`
	Status   string       `xml:"DAV: status"`
	Error    *davProblems `xml:"DAV: error"`
}

// davPropsta is one group of properties sharing a status.
type davPropsta struct {
	Prop   davProp `xml:"DAV: prop"`
	Status string  `xml:"DAV: status"`
}

// davProp carries every property this connector asks for. A property the
// server did not answer stays at its zero value.
type davProp struct {
	ResourceType            *davResourceType  `xml:"DAV: resourcetype"`
	DisplayName             string            `xml:"DAV: displayname"`
	GetETag                 string            `xml:"DAV: getetag"`
	CurrentUserPrincipal    *davHref          `xml:"DAV: current-user-principal"`
	CurrentUserPrivilegeSet *davPrivilegeSet  `xml:"DAV: current-user-privilege-set"`
	CalendarHomeSet         *davHrefs         `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	CalendarDescription     string            `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
	CalendarTimeZone        string            `xml:"urn:ietf:params:xml:ns:caldav calendar-timezone"`
	SupportedComponents     *davComponentSet  `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	CalendarData            string            `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
	ScheduleCalendarTransp  *davTransp        `xml:"urn:ietf:params:xml:ns:caldav schedule-calendar-transp"`
	Unknown                 []davUnknownChild `xml:",any"`
}

// davUnknownChild swallows a property this connector did not ask for, so
// a server that answers with extras is read without an error.
type davUnknownChild struct {
	XMLName xml.Name
}

// davResourceType is the DAV:resourcetype value. A calendar collection
// reports both DAV:collection and CALDAV:calendar (RFC 4791 section 4.2).
type davResourceType struct {
	Collection *struct{} `xml:"DAV: collection"`
	Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

// isCalendar reports whether the resource is a calendar collection.
func (r *davResourceType) isCalendar() bool {
	return r != nil && r.Collection != nil && r.Calendar != nil
}

// davHref is a property whose value is one DAV:href.
type davHref struct {
	Href string `xml:"DAV: href"`
}

// davHrefs is a property whose value is a list of DAV:href.
type davHrefs struct {
	Href []string `xml:"DAV: href"`
}

// davPrivilegeSet is DAV:current-user-privilege-set (RFC 3744 section
// 5.4), which is how the connector tells a calendar it may only read.
type davPrivilegeSet struct {
	Privilege []davPrivilege `xml:"DAV: privilege"`
}

// davPrivilege is one privilege in the set.
type davPrivilege struct {
	Write        *struct{} `xml:"DAV: write"`
	WriteContent *struct{} `xml:"DAV: write-content"`
	All          *struct{} `xml:"DAV: all"`
}

// writable reports whether the set grants a write of the collection's
// content.
func (s *davPrivilegeSet) writable() bool {
	if s == nil {
		// A server that answers no privilege set says nothing about the
		// calendar, and a calendar assumed read only can never be an
		// edge's target. The write itself is what reports a refusal.
		return true
	}
	for _, p := range s.Privilege {
		if p.Write != nil || p.WriteContent != nil || p.All != nil {
			return true
		}
	}

	return false
}

// davComponentSet is CALDAV:supported-calendar-component-set (RFC 4791
// section 5.2.3).
type davComponentSet struct {
	Comp []davComp `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

// davComp is one component name inside the set.
type davComp struct {
	Name string `xml:"name,attr"`
}

// holdsEvents reports whether the calendar takes VEVENTs. RFC 4791
// section 5.2.3 makes an absent property mean every component is
// allowed.
func (s *davComponentSet) holdsEvents() bool {
	if s == nil || len(s.Comp) == 0 {
		return true
	}
	for _, c := range s.Comp {
		if strings.EqualFold(c.Name, "VEVENT") {
			return true
		}
	}

	return false
}

// davTransp is CALDAV:schedule-calendar-transp, read only so the
// property does not fall into davProp.Unknown.
type davTransp struct {
	Opaque      *struct{} `xml:"urn:ietf:params:xml:ns:caldav opaque"`
	Transparent *struct{} `xml:"urn:ietf:params:xml:ns:caldav transparent"`
}

// davProblems is a DAV:error body, whose children name the preconditions
// that failed (RFC 4918 section 16).
type davProblems struct {
	NumberOfMatchesWithinLimits *struct{} `xml:"DAV: number-of-matches-within-limits"`
	ValidSyncToken              *struct{} `xml:"DAV: valid-sync-token"`
}

// statusCode reads the three digit code out of a DAV:status line, whose
// shape RFC 4918 section 13 takes from the HTTP status line, for example
// "HTTP/1.1 404 Not Found".
func statusCode(status string) (int, bool) {
	fields := strings.Fields(status)
	if len(fields) < 2 {
		return 0, false
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}

	return code, true
}

// prop returns the first property group the response carries with a 2xx
// status, which is where the values the connector asked for are.
func (r davResp) prop() davProp {
	for _, ps := range r.Propstat {
		if code, ok := statusCode(ps.Status); ok && code/100 == 2 {
			return ps.Prop
		}
	}

	return davProp{}
}

// gone reports whether the response says the member is no longer in the
// collection, which RFC 6578 section 3.2 carries as a 404 on the
// response itself.
func (r davResp) gone() bool {
	code, ok := statusCode(r.Status)

	return ok && code == http.StatusNotFound
}

// request is one call to the server: the method, the absolute URL, the
// headers it needs beyond the ones every request carries, and the body.
type request struct {
	method  string
	url     *url.URL
	headers map[string]string
	body    []byte
	// contentType is the body's media type, empty for a request with no
	// body.
	contentType string
}

// result is what the connector reads off an answer.
type result struct {
	status int
	header http.Header
	body   []byte
}

// do sends one request with the credential's authorization, and reads
// the answer. It never puts the credential into an error.
func (c *Connector) do(ctx context.Context, p Profile, cred Credential, req request) (result, error) {
	if !p.allowsHost(req.url.Host) {
		return result{}, fmt.Errorf("caldav profile %q refuses host %q: %w", p.Name, req.url.Host, ErrForbiddenHost)
	}
	if req.url.Scheme != "https" {
		return result{}, fmt.Errorf("caldav url %q is not https: %w", req.url.Redacted(), ErrForbiddenHost)
	}

	var body io.Reader
	if len(req.body) > 0 {
		body = bytes.NewReader(req.body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url.String(), body)
	if err != nil {
		return result{}, fmt.Errorf("caldav build %s request: %w", req.method, err)
	}

	authorization, err := cred.authorization(p)
	if err != nil {
		return result{}, err
	}
	httpReq.Header.Set("Authorization", authorization)
	httpReq.Header.Set("User-Agent", userAgent)
	if req.contentType != "" {
		httpReq.Header.Set("Content-Type", req.contentType)
	}
	for name, value := range req.headers {
		httpReq.Header.Set(name, value)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		// The URL is in the error the transport returns, and a CalDAV URL
		// holds an account identifier but never the password.
		return result{}, fmt.Errorf("caldav %s: %w", req.method, err)
	}
	defer func() {
		// The body is drained and closed so the connection is reused. A
		// failure to close one tells the caller nothing it can act on.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
	}()

	read, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return result{}, fmt.Errorf("caldav %s: read body: %w", req.method, err)
	}

	out := result{status: resp.StatusCode, header: resp.Header, body: read}
	if err := authError(p, out); err != nil {
		return result{}, err
	}

	return out, nil
}

// userAgent names calbridge to the server, which is what an account
// owner sees in a connection list.
const userAgent = "calbridge"

// authError turns a refusal of the credential into ErrAuthentication, so
// a revoked app specific password surfaces as a connection error rather
// than as an empty result.
func authError(p Profile, res result) error {
	if res.status != http.StatusUnauthorized {
		return nil
	}

	return fmt.Errorf("caldav profile %q: %w", p.Name, ErrAuthentication)
}

// expect reports an error unless the answer carries one of the given
// statuses. The error names the status and the request, and quotes no
// part of the body, because a server may echo a request header into it.
func expect(res result, method string, want ...int) error {
	for _, w := range want {
		if res.status == w {
			return nil
		}
	}

	return fmt.Errorf("caldav %s answered %d %s: %w", method, res.status, http.StatusText(res.status), ErrServer)
}

// parseMultistatus reads a 207 body.
func parseMultistatus(res result, method string) (multistatus, error) {
	var ms multistatus
	if err := xml.Unmarshal(res.body, &ms); err != nil {
		return multistatus{}, fmt.Errorf("caldav %s: parse multistatus: %w", method, ErrServer)
	}

	return ms, nil
}

// marshalBody renders a request body with the XML declaration a CalDAV
// server expects.
func marshalBody(v any) ([]byte, error) {
	out, err := xml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("caldav build request body: %w", err)
	}

	return append([]byte(xml.Header), out...), nil
}

// resolve turns an href a server returned into an absolute URL the
// connector may use, against the URL the answer came from. A server's
// own answer is what points the connector at the next request, so the
// host it names is checked against the profile before the credential
// goes anywhere near it.
func resolve(p Profile, base *url.URL, href string) (*url.URL, error) {
	if strings.TrimSpace(href) == "" {
		return nil, fmt.Errorf("caldav server returned an empty href: %w", ErrServer)
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return nil, fmt.Errorf("caldav server returned an unparseable href: %w", ErrServer)
	}

	out := base.ResolveReference(ref)
	if out.Scheme != "https" {
		return nil, fmt.Errorf("caldav href resolves to %q, which is not https: %w", out.Redacted(), ErrForbiddenHost)
	}
	if !p.allowsHost(out.Host) {
		return nil, fmt.Errorf("caldav profile %q refuses host %q: %w", p.Name, out.Host, ErrForbiddenHost)
	}

	return out, nil
}

// parseCalendarURL reads a calendar id back into the URL it is, and
// refuses one that points outside the profile.
func parseCalendarURL(p Profile, calendarID string) (*url.URL, error) {
	if calendarID == "" {
		return nil, fmt.Errorf("caldav: %w", provider.ErrUnknownCalendar)
	}
	u, err := url.Parse(calendarID)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("caldav calendar id is not a url: %w", provider.ErrUnknownCalendar)
	}
	if !p.allowsHost(u.Host) {
		return nil, fmt.Errorf("caldav profile %q refuses host %q: %w", p.Name, u.Host, ErrForbiddenHost)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}

	return u, nil
}
