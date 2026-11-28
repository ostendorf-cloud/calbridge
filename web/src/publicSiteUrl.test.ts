// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { afterEach, describe, expect, it } from 'vitest'

import { publicSiteUrl } from './publicSiteUrl'

function setMeta(content: string | undefined): void {
  document.head.querySelector('meta[name="calbridge-public-site-url"]')?.remove()
  if (content === undefined) {
    return
  }
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-public-site-url')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

describe('publicSiteUrl', () => {
  afterEach(() => {
    setMeta(undefined)
  })

  it('reads the host a deployment configured, not one fixed at build time', () => {
    setMeta('https://calbridge.example.com')
    expect(publicSiteUrl()).toBe('https://calbridge.example.com')
  })

  it('reads a different configured host just as well, proving the value is not hardcoded', () => {
    setMeta('https://landing.staging.example.org')
    expect(publicSiteUrl()).toBe('https://landing.staging.example.org')
  })

  it('returns an empty string when the tag is missing', () => {
    expect(publicSiteUrl()).toBe('')
  })

  it('returns an empty string when the tag carries no content', () => {
    setMeta('')
    expect(publicSiteUrl()).toBe('')
  })
})
