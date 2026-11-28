// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// internal/landingapi keeps the same list of pages in Go (spaPages) and
// answers any other path with a 404. These tests pin this side of it, so
// a page added to one list and not the other fails here.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n } from '../i18n'
import { createAppRouter } from './index'

const knownPages = [
  ['/', 'home'],
  ['/pricing', 'pricing'],
  ['/de/imprint', 'govue-legal-imprint-de'],
  ['/en/imprint', 'govue-legal-imprint-en'],
  ['/de/privacy', 'govue-legal-privacy-de'],
  ['/en/privacy', 'govue-legal-privacy-en'],
  ['/de/terms', 'govue-legal-terms-de'],
  ['/en/terms', 'govue-legal-terms-en'],
] as const

const cookieName = 'govue-language'

function newRouter() {
  const { govue } = createAppI18n()
  govue.language.setLocale('de', { persist: false })
  const router = createAppRouter(govue.language, createMemoryHistory())
  return { router, language: govue.language }
}

describe('createAppRouter', () => {
  beforeEach(() => {
    document.cookie = `${cookieName}=; Path=/; Max-Age=0`
  })

  afterEach(() => {
    document.cookie = `${cookieName}=; Path=/; Max-Age=0`
  })

  it.each(knownPages)('resolves %s to its own page', (path, name) => {
    const { router } = newRouter()

    expect(router.resolve(path).name).toBe(name)
  })

  it.each([
    '/imprint',
    '/privacy',
    '/terms',
    '/impressum',
    '/datenschutz',
    '/agb',
    '/fr/imprint',
    '/de/unknown',
    '/de/imprint/',
    '/DE/imprint',
    '/Pricing',
    '/pricing/',
    '/no/such/page',
  ])('resolves %s to the not found page, not to a page of the site', (path) => {
    const { router } = newRouter()

    expect(router.resolve(path).name).toBe('not-found')
  })

  it('marks each legal route with its page and language', () => {
    const { router } = newRouter()

    expect(router.resolve('/en/terms').meta).toEqual({ locale: 'en', legalPage: 'terms' })
  })

  it('follows the language of a prefixed address without storing it', async () => {
    const { router, language } = newRouter()

    await router.push('/en/privacy')

    expect(language.locale.value).toBe('en')
    expect(document.cookie).not.toContain(`${cookieName}=`)
  })

  it('follows the address in both directions', async () => {
    const { router, language } = newRouter()

    await router.push('/en/privacy')
    await router.push('/de/privacy')

    expect(language.locale.value).toBe('de')
  })

  it('keeps the chosen language on a page that carries no prefix', async () => {
    const { router, language } = newRouter()

    await router.push('/en/privacy')
    await router.push('/pricing')

    expect(language.locale.value).toBe('en')
  })

  it('keeps the chosen language on the not found page', async () => {
    const { router, language } = newRouter()

    await router.push('/no/such/page')

    expect(language.locale.value).toBe('de')
  })
})
