// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { render, screen } from '@testing-library/vue'
import { afterEach, describe, expect, it } from 'vitest'

import { clearMeta, languagePlugins, setOperatorMeta, testOperator } from '../testSupport'
import ImprintView from './ImprintView.vue'

describe('ImprintView', () => {
  afterEach(clearMeta)

  it('shows the operator the deployment configures', () => {
    setOperatorMeta()
    render(ImprintView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.getByRole('heading', { level: 1, name: 'Imprint' })).toBeVisible()
    expect(screen.getByText('Erika Mustermann')).toBeVisible()
    expect(screen.getByText('Musterstrasse 12')).toBeVisible()
    expect(screen.getByText('10115 Berlin')).toBeVisible()
    expect(screen.getByRole('link', { name: /kontakt@calbridge.example/ })).toHaveAttribute(
      'href',
      'mailto:kontakt@calbridge.example',
    )
  })

  it('leaves out an optional field the deployment did not set', () => {
    setOperatorMeta()
    render(ImprintView, { global: { plugins: languagePlugins('en').plugins } })

    for (const label of [
      'Telephone',
      'Represented by',
      'Register entry',
      'VAT identification number',
      'Responsible for content',
    ]) {
      expect(screen.queryByText(label)).not.toBeInTheDocument()
    }
  })

  it('shows every optional field the deployment set', () => {
    setOperatorMeta({
      ...testOperator,
      country: 'Germany',
      phone: '+49 30 1234567',
      representative: 'Max Beispiel',
      register: 'Amtsgericht Berlin, HRB 12345',
      vatId: 'DE123456789',
      contentResponsible: 'Erika Mustermann',
    })
    render(ImprintView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.getByText('Germany')).toBeVisible()
    expect(screen.getByText(/\+49 30 1234567/)).toBeVisible()
    expect(screen.getByText('Max Beispiel')).toBeVisible()
    expect(screen.getByText('Amtsgericht Berlin, HRB 12345')).toBeVisible()
    expect(screen.getByText('DE123456789')).toBeVisible()
    expect(screen.getByText('Responsible for content')).toBeVisible()
  })

  it('has no dispute resolution section', () => {
    setOperatorMeta()
    render(ImprintView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.queryByText(/dispute|arbitration|Streitschlichtung/i)).not.toBeInTheDocument()
  })

  it.each([
    ['no operator tag', undefined],
    ['the unsubstituted placeholder', '__CALBRIDGE_OPERATOR__'],
    ['an operator without an address', JSON.stringify({ name: 'Erika Mustermann' })],
  ])('says the details are unavailable for %s, never a blank name', (_name, content) => {
    if (content !== undefined) {
      setOperatorMeta(content)
    }
    render(ImprintView, { global: { plugins: languagePlugins('en').plugins } })

    expect(
      screen.getByText(
        'The operator details are not available on this page. Please come back later.',
      ),
    ).toBeVisible()
    expect(screen.queryByRole('definition')).not.toBeInTheDocument()
  })
})
