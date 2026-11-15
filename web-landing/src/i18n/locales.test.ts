// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// createAppI18n falls English back to German for any key German has and
// English does not, which is the right behaviour at runtime and a silent
// one in a review: a privacy section added to German alone renders in
// German on the English page, and nothing says so. This file is what
// says so.
import { describe, expect, it } from 'vitest'

import de from './locales/de.json'
import en from './locales/en.json'

/** flatKeys lists every leaf key of a messages object as a dotted path,
 *  so two locales can be compared as sets rather than walked by hand. */
function flatKeys(messages: unknown, prefix = ''): string[] {
  if (typeof messages !== 'object' || messages === null) {
    return [prefix]
  }

  return Object.entries(messages).flatMap(([key, value]) =>
    flatKeys(value, prefix === '' ? key : `${prefix}.${key}`),
  )
}

describe('the two locales', () => {
  it('carry the same keys', () => {
    expect(flatKeys(en).sort()).toEqual(flatKeys(de).sort())
  })

  it('leave no message empty', () => {
    for (const [name, messages] of [
      ['de', de],
      ['en', en],
    ] as const) {
      for (const key of flatKeys(messages)) {
        const value = key
          .split('.')
          .reduce<unknown>((node, part) => (node as Record<string, unknown>)[part], messages)
        expect(value, `${name}.${key}`).toEqual(expect.any(String))
        expect((value as string).trim(), `${name}.${key}`).not.toBe('')
      }
    }
  })
})
