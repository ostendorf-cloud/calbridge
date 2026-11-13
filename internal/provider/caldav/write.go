// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

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

// contentTypeCalendar is the media type of a calendar object resource
// (RFC 5545 section 8.1).
const contentTypeCalendar = "text/calendar; charset=utf-8"

// resourceSuffix is the extension a calendar object resource is stored
// under, which RFC 4791 section 5.3.2 uses throughout.
const resourceSuffix = ".ics"

// mkcalendar is the request body of RFC 4791 section 5.3.1.
type mkcalendar struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav mkcalendar"`
	Set     mkcalendarSet `xml:"DAV: set"`
}

// mkcalendarSet carries the properties the new collection is created
// with.
type mkcalendarSet struct {
	Prop mkcalendarProp `xml:"DAV: prop"`
}

// mkcalendarProp is the property set a managed calendar is created with:
// the name the user sees, and a restriction to events, because calbridge
// mirrors nothing else.
type mkcalendarProp struct {
	DisplayName string             `xml:"DAV: displayname"`
	Components  mkcalendarCompList `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
}

// mkcalendarCompList names the components the calendar takes.
type mkcalendarCompList struct {
	Comp []mkcalendarComp `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

// mkcalendarComp is one component name.
type mkcalendarComp struct {
	Name string `xml:"name,attr"`
}

// propfindETag asks for the entity tag of one resource.
const propfindETag = `<propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`

// CreateCalendar creates a calendar calbridge owns.
func (c *Connector) CreateCalendar(ctx context.Context, cred provider.Credential, name string) (provider.Calendar, error) {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return provider.Calendar{}, err
	}
	if !c.Capabilities().CalendarCreation {
		return provider.Calendar{}, fmt.Errorf("caldav profile %q: create calendar: %w", p.Name, provider.ErrUnsupported)
	}

	home, err := c.calendarHome(ctx, p, own)
	if err != nil {
		return provider.Calendar{}, err
	}
	at := *home
	at.Path = home.Path + managedPrefix + c.newUID() + "/"
	at.RawPath = ""

	body, err := marshalBody(mkcalendar{Set: mkcalendarSet{Prop: mkcalendarProp{
		DisplayName: name,
		Components:  mkcalendarCompList{Comp: []mkcalendarComp{{Name: "VEVENT"}}},
	}}})
	if err != nil {
		return provider.Calendar{}, err
	}

	res, err := c.do(ctx, p, own, request{
		method:      "MKCALENDAR",
		url:         &at,
		body:        body,
		contentType: contentTypeXML,
	})
	if err != nil {
		return provider.Calendar{}, err
	}
	if err := createdCalendar(res); err != nil {
		return provider.Calendar{}, err
	}

	return provider.Calendar{ID: collectionID(&at), Name: name, Managed: true}, nil
}

// createdCalendar reads the answer to a MKCALENDAR.
//
// RFC 4791 section 5.3.1.1 gives a server two ways to answer: 201 when
// the collection was created with every property it was asked for, and
// 207 carrying a status per property when one could not be set, in which
// case the server "MUST NOT create the calendar collection". A 207 whose
// property statuses are all successful is a creation too, which is how
// at least one server reports one, so the status of the properties, not
// the status of the request, is what decides.
func createdCalendar(res result) error {
	if res.status == http.StatusCreated {
		return nil
	}
	if err := expect(res, "MKCALENDAR", http.StatusMultiStatus); err != nil {
		return err
	}
	ms, err := parseMultistatus(res, "MKCALENDAR")
	if err != nil {
		return err
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			code, ok := statusCode(ps.Status)
			if !ok || code/100 != 2 {
				return fmt.Errorf("caldav MKCALENDAR could not set a property (%s) and created nothing: %w",
					ps.Status, ErrServer)
			}
		}
	}

	return nil
}

// DeleteCalendar removes a calendar calbridge created.
func (c *Connector) DeleteCalendar(ctx context.Context, cred provider.Credential, calendarID string) error {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return err
	}
	if !c.Capabilities().CalendarCreation {
		return fmt.Errorf("caldav profile %q: delete calendar: %w", p.Name, provider.ErrUnsupported)
	}
	at, err := parseCalendarURL(p, calendarID)
	if err != nil {
		return err
	}
	if !isManaged(at) {
		return fmt.Errorf("caldav calendar %q was not created by calbridge: %w", lastSegment(at.Path), provider.ErrUnsupported)
	}

	res, err := c.do(ctx, p, own, request{method: "DELETE", url: at})
	if err != nil {
		return err
	}
	if res.status == http.StatusNotFound || res.status == http.StatusGone {
		// Removing what is already gone succeeds, so an interrupted run
		// is safe to repeat.
		return nil
	}

	return expect(res, "DELETE", http.StatusNoContent, http.StatusOK, http.StatusAccepted)
}

// CreateEvent writes an event into the calendar.
func (c *Connector) CreateEvent(
	ctx context.Context,
	cred provider.Credential,
	calendarID string,
	event provider.Event,
) (provider.Event, error) {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return provider.Event{}, err
	}
	calendar, err := parseCalendarURL(p, calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	caps := c.Capabilities()
	stored, err := accept(p, caps, event)
	if err != nil {
		return provider.Event{}, err
	}

	if stored.UID == "" {
		stored.UID = c.newUID()
	}
	stored.ID = stored.UID + resourceSuffix
	now := c.now().UTC()
	stored.Created, stored.Updated = now, now

	object, err := encodeResource(stored, now, caps)
	if err != nil {
		return provider.Event{}, err
	}
	etag, err := c.put(ctx, p, own, calendar, stored.ID, object, ifNoneMatchNew)
	if err != nil {
		return provider.Event{}, err
	}
	stored.ETag = etag

	return stored, nil
}

// UpdateEvent overwrites the event the id names.
func (c *Connector) UpdateEvent(
	ctx context.Context,
	cred provider.Credential,
	calendarID string,
	event provider.Event,
) (provider.Event, error) {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return provider.Event{}, err
	}
	calendar, err := parseCalendarURL(p, calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	caps := c.Capabilities()
	stored, err := accept(p, caps, event)
	if err != nil {
		return provider.Event{}, err
	}
	if strings.Contains(stored.ID, instanceSeparator) {
		return provider.Event{}, fmt.Errorf(
			"caldav cannot write one overridden occurrence on its own: %w", provider.ErrUnsupported)
	}

	held, err := c.readEvent(ctx, p, own, calendar, stored.ID)
	if err != nil {
		return provider.Event{}, err
	}
	stored.UID = held.UID
	stored.Created = held.Created
	stored.Updated = c.now().UTC()

	object, err := encodeResource(stored, stored.Updated, caps)
	if err != nil {
		return provider.Event{}, err
	}
	condition := stored.ETag
	if condition == "" {
		condition = held.ETag
	}
	etag, err := c.put(ctx, p, own, calendar, stored.ID, object, condition)
	if err != nil {
		return provider.Event{}, err
	}
	stored.ETag = etag

	return stored, nil
}

// DeleteEvent removes an event from the calendar.
func (c *Connector) DeleteEvent(ctx context.Context, cred provider.Credential, calendarID, eventID string) error {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return err
	}
	if strings.Contains(eventID, instanceSeparator) {
		return fmt.Errorf("caldav cannot remove one overridden occurrence on its own: %w", provider.ErrUnsupported)
	}
	calendar, err := parseCalendarURL(p, calendarID)
	if err != nil {
		return err
	}
	at, err := resourceURL(calendar, eventID)
	if err != nil {
		return err
	}

	res, err := c.do(ctx, p, own, request{method: "DELETE", url: at})
	if err != nil {
		return err
	}
	if res.status == http.StatusNotFound || res.status == http.StatusGone {
		// Removing what is already gone succeeds, so an interrupted run
		// is safe to repeat.
		return nil
	}

	return expect(res, "DELETE", http.StatusNoContent, http.StatusOK, http.StatusAccepted)
}

// ifNoneMatchNew is the condition a creating PUT carries: RFC 4791
// section 5.3.2 has a client use If-None-Match "*" so a write that would
// overwrite an existing resource is refused instead.
const ifNoneMatchNew = "*"

// put writes one calendar object resource and returns its entity tag.
// The condition is "*" for a creation, an entity tag for an update, and
// empty for a write with no condition at all.
func (c *Connector) put(
	ctx context.Context,
	p Profile,
	cred Credential,
	calendar *url.URL,
	name, object, condition string,
) (string, error) {
	at, err := resourceURL(calendar, name)
	if err != nil {
		return "", err
	}

	headers := map[string]string{}
	switch {
	case condition == ifNoneMatchNew:
		headers["If-None-Match"] = ifNoneMatchNew
	case condition != "" && (!p.Quirks.NoIfMatchWildcard || condition != "*"):
		headers["If-Match"] = condition
	}

	res, err := c.do(ctx, p, cred, request{
		method:      http.MethodPut,
		url:         at,
		headers:     headers,
		body:        []byte(object),
		contentType: contentTypeCalendar,
	})
	if err != nil {
		return "", err
	}
	switch res.status {
	case http.StatusPreconditionFailed:
		return "", fmt.Errorf("caldav refused the write of %q: %w", name, ErrConflict)
	case http.StatusNotFound:
		return "", fmt.Errorf("caldav event %q: %w", name, provider.ErrUnknownEvent)
	}
	if err := expect(res, http.MethodPut, http.StatusCreated, http.StatusNoContent, http.StatusOK); err != nil {
		return "", err
	}

	if etag := res.header.Get("ETag"); etag != "" {
		return etag, nil
	}

	// RFC 4791 section 5.3.4 only recommends the ETag header on a PUT, so
	// a server that left it out is asked for the property instead.
	return c.fetchETag(ctx, p, cred, at)
}

// fetchETag reads DAV:getetag off one resource.
func (c *Connector) fetchETag(ctx context.Context, p Profile, cred Credential, at *url.URL) (string, error) {
	res, err := c.do(ctx, p, cred, request{
		method:      "PROPFIND",
		url:         at,
		headers:     map[string]string{"Depth": "0"},
		body:        []byte(propfindETag),
		contentType: contentTypeXML,
	})
	if err != nil {
		return "", err
	}
	if err := expect(res, "PROPFIND", http.StatusMultiStatus); err != nil {
		return "", err
	}
	ms, err := parseMultistatus(res, "PROPFIND")
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		if etag := r.prop().GetETag; etag != "" {
			return etag, nil
		}
	}

	return "", nil
}

// readEvent fetches one resource and returns the master event in it,
// which is what an update overwrites.
func (c *Connector) readEvent(
	ctx context.Context,
	p Profile,
	cred Credential,
	calendar *url.URL,
	name string,
) (provider.Event, error) {
	at, err := resourceURL(calendar, name)
	if err != nil {
		return provider.Event{}, err
	}

	res, err := c.do(ctx, p, cred, request{method: http.MethodGet, url: at})
	if err != nil {
		return provider.Event{}, err
	}
	if res.status == http.StatusNotFound || res.status == http.StatusGone {
		return provider.Event{}, fmt.Errorf("caldav event %q: %w", name, provider.ErrUnknownEvent)
	}
	if err := expect(res, http.MethodGet, http.StatusOK); err != nil {
		return provider.Event{}, err
	}

	events, err := decodeResource(name, string(res.body))
	if err != nil {
		return provider.Event{}, err
	}
	for _, event := range events {
		if !event.Recurrence.IsInstance() {
			event.ETag = res.header.Get("ETag")

			return event, nil
		}
	}

	return provider.Event{}, fmt.Errorf("caldav resource %q holds only overridden occurrences: %w", name, ErrServer)
}

// accept applies the declared capabilities to an event on its way to the
// server: it refuses what cannot be expressed and drops what may be
// dropped, so a capability a caller read is the same answer it gets.
//
// caps is what Connector.Capabilities returns, not the profile's own
// value. The two differ as soon as a second profile is added, because
// the interface asks one connector for one answer and that answer is the
// intersection. Gating on the profile instead would let a connector
// declare that it writes no attendee and then write one for the server
// that happens to allow it, which is the outcome the declaration is
// there to rule out. The profile is still what names the server in a
// message.
func accept(p Profile, caps provider.Capabilities, event provider.Event) (provider.Event, error) {
	if err := event.Validate(); err != nil {
		return provider.Event{}, err
	}
	if event.Recurrence.IsInstance() {
		return provider.Event{}, fmt.Errorf(
			"caldav cannot write one overridden occurrence on its own: %w", provider.ErrUnsupported)
	}
	if event.Recurrence.IsMaster() && !caps.NativeRecurrence {
		return provider.Event{}, fmt.Errorf("caldav profile %q: recurrence rule: %w", p.Name, provider.ErrUnsupported)
	}
	if event.Start.Kind == provider.TimeKindFloating && !caps.FloatingTimes {
		return provider.Event{}, fmt.Errorf("caldav profile %q: floating time: %w", p.Name, provider.ErrUnsupported)
	}
	if !caps.Attendees {
		event.Attendees = nil
	}
	// Every server names the organiser after the writing account, so the
	// connector drops what it was handed rather than pretending it stuck.
	event.Organizer = nil

	return event, nil
}
