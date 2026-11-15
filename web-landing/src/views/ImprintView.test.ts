import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

import en from '../i18n/locales/en.json'
import ImprintView from './ImprintView.vue'

describe('ImprintView', () => {
  it('shows a named placeholder for the operator, never an invented company or address', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
    render(ImprintView, { global: { plugins: [i18n] } })

    expect(screen.getByRole('heading', { name: 'Imprint' })).toBeVisible()
    expect(
      screen.getByText('[Operator name — filled in by the owner of this deployment]'),
    ).toBeVisible()
    expect(
      screen.getByText('[Postal address — filled in by the owner of this deployment]'),
    ).toBeVisible()
  })
})
