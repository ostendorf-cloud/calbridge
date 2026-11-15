// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Every page this build serves has to carry German and English, the same
// two languages the application does. Each existing view test below
// renders in English only; this file is the one place proving the other
// half of that claim, for every route at once, rather than duplicating a
// German variant of every test above.
import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

import de from '../i18n/locales/de.json'
import en from '../i18n/locales/en.json'
import HomeView from './HomeView.vue'
import ImprintView from './ImprintView.vue'
import PricingView from './PricingView.vue'
import PrivacyView from './PrivacyView.vue'

function setAppUrlMeta(content: string): void {
  document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-app-url')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

const pages = [
  { name: 'HomeView', view: HomeView, titleKey: 'home.hero.title' as const },
  { name: 'PricingView', view: PricingView, titleKey: 'pricing.title' as const },
  { name: 'ImprintView', view: ImprintView, titleKey: 'imprint.title' as const },
  { name: 'PrivacyView', view: PrivacyView, titleKey: 'privacy.title' as const },
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
  for (const { name, view, titleKey } of pages) {
    it(`renders ${name} in German`, () => {
      setAppUrlMeta('https://app.calbridge.example.com')
      const i18n = createI18n({ legacy: false, locale: 'de', messages: { de } })

      render(view, { global: { plugins: [i18n] } })

      expect(screen.getByRole('heading', { level: 1, name: read(de, titleKey) })).toBeVisible()
    })

    it(`renders ${name} in English`, () => {
      setAppUrlMeta('https://app.calbridge.example.com')
      const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })

      render(view, { global: { plugins: [i18n] } })

      expect(screen.getByRole('heading', { level: 1, name: read(en, titleKey) })).toBeVisible()
    })
  }
})
