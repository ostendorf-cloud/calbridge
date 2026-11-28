// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { describe, expect, it } from 'vitest'

import { detectPreferredLocale, isLocale, readStoredLocale } from './index'

describe('isLocale', () => {
  it('accepts a supported locale', () => {
    expect(isLocale('de')).toBe(true)
    expect(isLocale('en')).toBe(true)
  })

  it('rejects a language this shell does not serve', () => {
    expect(isLocale('fr')).toBe(false)
  })

  it('rejects an empty value', () => {
    expect(isLocale('')).toBe(false)
  })
})

describe('detectPreferredLocale', () => {
  it('falls back to German for an empty language list', () => {
    expect(detectPreferredLocale([])).toBe('de')
  })

  it('picks English for an English regional tag', () => {
    expect(detectPreferredLocale(['en-GB'])).toBe('en')
  })

  it('picks German for a German regional tag', () => {
    expect(detectPreferredLocale(['de-DE'])).toBe('de')
  })

  it('falls back to German for a language this shell does not serve', () => {
    expect(detectPreferredLocale(['fr-FR'])).toBe('de')
  })

  it('only looks at the highest weighted language', () => {
    expect(detectPreferredLocale(['de-DE', 'en-US'])).toBe('de')
  })
})

// fakeStorage is a minimal Storage stand-in, so a test can drive
// readStoredLocale's getItem call without touching the real
// localStorage every other test in this file leaves alone.
function fakeStorage(value: string | null): Pick<Storage, 'getItem'> {
  return { getItem: () => value }
}

// throwingStorage models a private window, cleared site data, or
// storage blocked outright: getItem itself throws rather than returning
// null.
function throwingStorage(): Pick<Storage, 'getItem'> {
  return {
    getItem: () => {
      throw new Error('storage is unavailable')
    },
  }
}

describe('readStoredLocale', () => {
  it('returns undefined for an empty environment with nothing stored', () => {
    expect(readStoredLocale(fakeStorage(null))).toBeUndefined()
  })

  it('returns the stored locale when it is supported', () => {
    expect(readStoredLocale(fakeStorage('en'))).toBe('en')
  })

  it('returns undefined for a stored value that is not a supported locale', () => {
    expect(readStoredLocale(fakeStorage('fr'))).toBeUndefined()
  })

  it('returns undefined when storage itself throws', () => {
    expect(readStoredLocale(throwingStorage())).toBeUndefined()
  })
})
