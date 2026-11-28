// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
)

// ErrRegistrationClosed is returned by every path that would create an
// account when the deployment closed registration: a local signup, and a
// provider sign-in whose subject has no identity yet. It names a refusal
// of the deployment's policy and says nothing about the person, which is
// why a caller may show it as itself.
var ErrRegistrationClosed = errors.New("auth: registration is closed on this instance")

// Option changes how Local and SignIn treat a person who has no account.
// With no option, registration is open, which is what the hosted service
// runs with.
type Option func(*options)

type options struct {
	registrationClosed bool
}

// WithRegistrationClosed refuses every path that would create an account.
// Logging in to an existing account, local or through a provider, is
// untouched.
//
// It is one option for both paths on purpose. A provider sign-in creates
// an account the first time it sees a subject, so it is a second
// registration path, and closing only the local one would leave the
// instance open to anyone holding a Google, Microsoft or Apple account.
func WithRegistrationClosed() Option {
	return func(o *options) { o.registrationClosed = true }
}

func newOptions(opts []Option) options {
	var o options

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// refusalKey is the context key trackRefusal stores its holder under.
type refusalKey struct{}

// refusalHolder carries the typed reason a sign-in was refused from
// StartSession to OnError. govue's oidc package turns any error
// StartSession returns into its own opaque ErrProvider, so without this
// the handler that answers the browser could not tell a closed instance
// from a session store that is down.
type refusalHolder struct {
	err atomic.Pointer[error]
}

// trackRefusal installs a holder on the request's context.
func trackRefusal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), refusalKey{}, &refusalHolder{})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recordRefusal stores err for the request ctx belongs to. It does
// nothing when ctx carries no holder.
func recordRefusal(ctx context.Context, err error) {
	if holder, ok := ctx.Value(refusalKey{}).(*refusalHolder); ok {
		holder.err.Store(&err)
	}
}

// recordedRefusal returns what recordRefusal stored for the request, or
// nil.
func recordedRefusal(ctx context.Context) error {
	holder, ok := ctx.Value(refusalKey{}).(*refusalHolder)
	if !ok {
		return nil
	}

	if err := holder.err.Load(); err != nil {
		return *err
	}

	return nil
}
