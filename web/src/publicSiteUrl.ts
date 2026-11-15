// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

/** metaName is the <meta> tag index.html carries a placeholder for;
 *  internal/httpapi's newSPAHandler substitutes its content attribute
 *  for the configured PUBLIC_SITE_URL once, at startup. */
const metaName = 'calbridge-public-site-url'

/** publicSiteUrl reads calbridge.de's own public base URL back out of
 *  that tag, so the "back to the public site" link reads a host the
 *  deployment configured rather than one fixed at build time. Returns
 *  an empty string when the tag is missing or empty, which is also the
 *  ordinary shape for a deployment that serves the application only and
 *  configured no public site to link back to; a component renders no
 *  link for that rather than one that goes nowhere. */
export function publicSiteUrl(): string {
  return document.querySelector(`meta[name="${metaName}"]`)?.getAttribute('content') ?? ''
}
