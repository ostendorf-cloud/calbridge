// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package auth is the business logic behind calbridge's two login paths:
// a local account with a password and TOTP, and sign in with Google,
// Microsoft or Apple through govue's oidc package. calbridge owns
// identity and depends on no external identity provider, which is what
// lets a stranger sign up rather than be invited into one cluster, per
// docs/concepts/calbridge.md.
//
// Nothing here is an HTTP handler, except the two govue already hands
// back as http.Handler values (oidc.Handlers.Login and
// oidc.Handlers.Callback, wired by NewProviderHandlers): routing,
// middleware and the local signup and login endpoints are
// internal/httpapi's job. That package calls this one; this one does not
// call out to it.
//
// # One identity, whatever the path
//
// [Identity] is what both paths converge on, and the only thing either
// returns. Its UserID is always a users row id, never a provider's
// subject claim, which is what lets every later layer, and
// session.Manager.EndAll, key on one identifier regardless of how a
// login happened. Nothing above this package ever branches on login
// method.
//
// # The linking rule
//
// A provider identity used to sign in is never matched against a
// provider account another user connected as a calendar. The collision
// creates a separate account.
//
// This is a security boundary, not a tidiness rule. Signing in with
// Google and connecting a Google calendar are two different consent
// steps over two different scope sets, and someone who gains control of
// a mailbox whose calendar another person connected would otherwise be
// able to sign in as that person. So the two live in two tables with no
// reference to each other (db/migrations), and the enforcement here is
// structural rather than a check that could be forgotten: [Store], the
// interface this package needs from storage, exposes users and
// identities and nothing else. There is no connection repository to
// reach for, so no code path in this package can follow one. The test
// that holds the rule down asserts both halves: the colliding sign-in
// creates a separate account, and the connection repository is never
// consulted at all.
//
// Matching is on the identity provider's stable subject claim and never
// on the email or the username, both of which can be reassigned to
// somebody else; govue's own oidc.Identity doc comment is explicit about
// that.
//
// # Identity scopes only
//
// A provider sign-in asks for identity scopes and nothing else, and the
// scope list is fixed by this package rather than configured: see
// [ProviderConfig]. No calendar scope is ever requested here. Connecting
// a provider's calendar is a separate consent step belonging to the
// connector issues, which is also what keeps Google's sensitive calendar
// scopes out of the sign-up screen.
//
// Apple is offered as a sign-in and grants identity and nothing else. An
// iCloud calendar is still connected separately over CalDAV with an
// Apple ID and an app-specific password, and nothing in this package
// implies otherwise.
//
// # Addresses from a provider, and what they cost
//
// An account created by provider sign-in is known by the provider's
// email claim only when the ID token marks that claim verified.
// Otherwise it gets a placeholder under PlaceholderEmailDomain, derived
// from the provider's subject.
//
// For two of the three configured providers that is every account, not
// an edge case. Microsoft Entra ID emits no email_verified claim at all
// and tells applications not to rely on its email claim; Apple is never
// asked for the email scope, because asking for it would move the
// callback to a POST that govue's oidc handler refuses. Only Google
// sends a verified address.
//
// The consequence is worth being plain about: a Microsoft or Apple
// account's address is a string nothing can deliver to, it is what
// GET /api/v1/me reports and the UI shows, and nothing in this package
// can change it. Believing an unverified claim instead would let a
// tenant administrator name somebody else's address, which is the worse
// of the two, so this is the trade taken knowingly rather than a gap
// nobody noticed. Letting a person confirm and correct their own address
// is the fix, and it is its own issue. See signInEmail.
//
// # The second factor
//
// A local account carries a password and a TOTP secret, and both are
// checked in one call: see [Local.Login]. One call rather than a
// password step followed by a separate TOTP step, which would need
// somewhere to keep "this browser already proved the password" between
// two requests, which is itself a small session to design, secure and
// expire. A UI is still free to show two screens and submit both values
// together.
//
// A provider sign-in carries no second factor of its own: the identity
// provider already decided what it took to authenticate that account,
// and asking for a TOTP code calbridge issued on top of it would be a
// second enrolment for an account that may never have had a password.
package auth
