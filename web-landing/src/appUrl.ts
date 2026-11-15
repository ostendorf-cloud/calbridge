// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

/** metaName is the <meta> tag index.html carries a placeholder for;
 *  internal/landingapi's newStaticHandler substitutes its content
 *  attribute for the configured APP_URL once, at startup. */
const metaName = 'calbridge-app-url'

/** appUrl reads the application's own public base URL back out of that
 *  tag, so every call to action this build renders reads a host the
 *  deployment configured rather than one fixed at build time: the same
 *  built bundle serves any deployment's own APP_URL. Returns an empty
 *  string when the tag is missing or empty, which a component renders
 *  no link for rather than one that goes nowhere. */
export function appUrl(): string {
  return document.querySelector(`meta[name="${metaName}"]`)?.getAttribute('content') ?? ''
}
