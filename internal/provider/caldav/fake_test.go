// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The tests in this package replay responses recorded from a CalDAV
// server instead of talking to one. The connector's HTTP surface is one
// interface, so the replay sits there: no listener is opened and no
// request leaves the process.

// answer is one recorded response.
type answer struct {
	status int
	body   string
	header http.Header
}

// call is one request the connector made.
type call struct {
	method string
	host   string
	path   string
	header http.Header
	body   string
}

// fakeServer answers a request from a table keyed by method and path.
type fakeServer struct {
	mu      sync.Mutex
	answers map[string]answer
	calls   []call
}

// newFakeServer returns a server with no answers in it yet.
func newFakeServer() *fakeServer {
	return &fakeServer{answers: map[string]answer{}}
}

// on records the answer to one method and path.
func (s *fakeServer) on(method, path string, a answer) *fakeServer {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.answers[method+" "+path] = a

	return s
}

// Do answers one request from the table.
func (s *fakeServer) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		read, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(read)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, call{
		method: req.Method,
		host:   req.URL.Host,
		path:   req.URL.Path,
		header: req.Header.Clone(),
		body:   body,
	})

	a, ok := s.answers[req.Method+" "+req.URL.Path]
	if !ok {
		a = answer{status: http.StatusNotFound, body: ""}
	}
	header := a.header
	if header == nil {
		header = http.Header{}
	}

	return &http.Response{
		StatusCode: a.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(a.body)),
		Request:    req,
	}, nil
}

// recorded returns the requests made so far.
func (s *fakeServer) recorded() []call {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]call(nil), s.calls...)
}

// last returns the most recent request of one method, and fails the test
// where none was made.
func (s *fakeServer) last(t *testing.T, method string) call {
	t.Helper()

	calls := s.recorded()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method == method {
			return calls[i]
		}
	}
	t.Fatalf("no %s request was made", method)

	return call{}
}

// written is the answer a successful write comes back as, carrying the
// entity tag the resource now has.
func written(status int, etag string) answer {
	header := http.Header{}
	header.Set("ETag", etag)

	return answer{status: status, header: header}
}

// read is the answer a GET of one calendar object comes back as.
func read(body, etag string) answer {
	header := http.Header{}
	header.Set("ETag", etag)
	header.Set("Content-Type", "text/calendar; charset=utf-8")

	return answer{status: http.StatusOK, body: body, header: header}
}

// multistatus is the answer shape a WebDAV report comes back in.
func multistatus(body string) answer {
	return answer{
		status: http.StatusMultiStatus,
		body:   body,
		header: http.Header{"Content-Type": []string{`application/xml; charset="utf-8"`}},
	}
}

// recorded reads one recorded response body from testdata.
func recordedBody(t *testing.T, name string) string {
	t.Helper()

	read, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read recorded response: %v", err)
	}

	return string(read)
}

// discoverable fills in the two discovery steps every test needs, and
// returns the server so a test adds the answers it is about.
func discoverable(t *testing.T) *fakeServer {
	t.Helper()

	return newFakeServer().
		on("PROPFIND", "/", multistatus(recordedBody(t, "principal.xml"))).
		on("PROPFIND", "/20241234/principal/", multistatus(recordedBody(t, "home.xml")))
}
