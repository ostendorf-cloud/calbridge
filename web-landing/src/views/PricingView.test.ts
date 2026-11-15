import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

import en from '../i18n/locales/en.json'
import PricingView from './PricingView.vue'

describe('PricingView', () => {
  it('states that calbridge is privately operated with no prices yet, and nothing else', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
    render(PricingView, { global: { plugins: [i18n] } })

    expect(screen.getByRole('heading', { name: 'Pricing' })).toBeVisible()
    expect(screen.getByText('calbridge is privately operated today.')).toBeVisible()
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
  })
})
