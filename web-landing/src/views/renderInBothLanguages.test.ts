// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Every page this build serves has to carry German and English, the same
// two languages the application does. Each existing view test renders in
// English only; this file is the one place proving the other half of that
// claim, for every route at once, rather than duplicating a German variant
// of every test above.
import { render, screen } from '@testing-library/vue'
import { afterEach, describe, expect, it } from 'vitest'

import de from '../i18n/locales/de.json'
import en from '../i18n/locales/en.json'
import { clearMeta, languagePlugins, setAppUrlMeta, setOperatorMeta } from '../testSupport'
import HomeView from './HomeView.vue'
import ImprintView from './ImprintView.vue'
import NotFoundView from './NotFoundView.vue'
import PricingView from './PricingView.vue'
import PrivacyView from './PrivacyView.vue'
import TermsView from './TermsView.vue'

const pages = [
  { name: 'HomeView', view: HomeView, titleKey: 'home.hero.title' as const },
  { name: 'PricingView', view: PricingView, titleKey: 'pricing.title' as const },
  { name: 'ImprintView', view: ImprintView, titleKey: 'imprint.title' as const },
  { name: 'PrivacyView', view: PrivacyView, titleKey: 'privacy.title' as const },
  { name: 'TermsView', view: TermsView, titleKey: 'terms.title' as const },
  { name: 'NotFoundView', view: NotFoundView, titleKey: 'notFound.title' as const },
]

function read(messages: typeof de, key: string): string {
  return key.split('.').reduce<unknown>((node, part) => {
    if (typeof node !== 'object' || node === null) {
      throw new Error(`locale key ${key} does not resolve`)
    }
    return (node as Record<string, unknown>)[part]
  }, messages) as string
}

describe('every page', () => {
  afterEach(clearMeta)

  for (const { name, view, titleKey } of pages) {
    for (const [locale, messages] of [
      ['de', de],
      ['en', en],
    ] as const) {
      it(`renders ${name} in ${locale === 'de' ? 'German' : 'English'}`, () => {
        setAppUrlMeta('https://app.calbridge.example.com')
        setOperatorMeta()

        render(view, { global: { plugins: languagePlugins(locale).plugins } })

        expect(
          screen.getByRole('heading', { level: 1, name: read(messages, titleKey) }),
        ).toBeVisible()
      })
    }
  }
})
