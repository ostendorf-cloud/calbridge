// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Shared by the view and shell tests: the page's meta tags, and the
// plugins a view needs, built the way main.ts builds them.
import type { GovueI18n } from '@ostendorf-cloud/govue'
import type { Plugin } from 'vue'

import { createAppI18n } from './i18n'
import type { Operator } from './operator'

/** A complete operator that looks like real data and is not one. */
export const testOperator: Operator = {
  name: 'Erika Mustermann',
  street: 'Musterstrasse 12',
  postalCode: '10115',
  city: 'Berlin',
  email: 'kontakt@calbridge.example',
}

function setMeta(name: string, content: string): void {
  document.head.querySelector(`meta[name="${name}"]`)?.remove()
  const meta = document.createElement('meta')
  meta.setAttribute('name', name)
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

export function setOperatorMeta(operator: Partial<Operator> | string = testOperator): void {
  setMeta('calbridge-operator', typeof operator === 'string' ? operator : JSON.stringify(operator))
}

export function setAppUrlMeta(content: string): void {
  setMeta('calbridge-app-url', content)
}

export function clearMeta(): void {
  document.head.querySelector('meta[name="calbridge-operator"]')?.remove()
  document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
}

/** languagePlugins returns the plugins main.ts installs for the language
 *  layer, set to one language without storing a choice. */
export function languagePlugins(locale: 'de' | 'en'): {
  plugins: Plugin[]
  language: GovueI18n['language']
} {
  const { i18n, govue } = createAppI18n()
  govue.language.setLocale(locale, { persist: false })

  return { plugins: [i18n, govue], language: govue.language }
}
