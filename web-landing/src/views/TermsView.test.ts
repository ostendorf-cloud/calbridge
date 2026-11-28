// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { languagePlugins } from '../testSupport'
import TermsView from './TermsView.vue'

describe('TermsView', () => {
  it.each([
    ['en', 'Terms of use', 'Liability'],
    ['de', 'Nutzungsbedingungen', 'Haftung'],
  ] as const)('renders the terms in %s', (locale, title, section) => {
    render(TermsView, { global: { plugins: languagePlugins(locale).plugins } })

    expect(screen.getByRole('heading', { level: 1, name: title })).toBeVisible()
    expect(screen.getByRole('heading', { level: 2, name: section })).toBeVisible()
  })

  it('has no dispute resolution section and no court clause', () => {
    render(TermsView, { global: { plugins: languagePlugins('en').plugins } })

    expect(
      screen.queryByText(/dispute resolution|arbitration|place of jurisdiction|governing law/i),
    ).not.toBeInTheDocument()
  })
})
