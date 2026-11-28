import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createAppI18n } from './index'

const cookieName = 'govue-language'

function clearCookie(): void {
  document.cookie = `${cookieName}=; Path=/; Max-Age=0`
}

function readCookie(): string | undefined {
  const entry = document.cookie.split('; ').find((part) => part.startsWith(`${cookieName}=`))
  return entry?.slice(cookieName.length + 1)
}

function browserLanguages(languages: string[]): void {
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(languages)
  // govue falls back to navigator.language when the list is empty.
  vi.spyOn(navigator, 'language', 'get').mockReturnValue(languages[0] ?? '')
}

describe('createAppI18n', () => {
  beforeEach(() => {
    clearCookie()
  })

  afterEach(() => {
    clearCookie()
    vi.restoreAllMocks()
  })

  it('falls back to German with no stored choice and no browser language', () => {
    browserLanguages([])

    const { govue } = createAppI18n()

    expect(govue.language.locale.value).toBe('de')
  })

  it('picks English for an English browser', () => {
    browserLanguages(['en-GB'])

    const { govue } = createAppI18n()

    expect(govue.language.locale.value).toBe('en')
  })

  it('falls back to German for a browser language this build does not serve', () => {
    browserLanguages(['fr-FR'])

    const { govue } = createAppI18n()

    expect(govue.language.locale.value).toBe('de')
  })

  it('lets the stored choice beat the browser', () => {
    browserLanguages(['de-DE'])
    document.cookie = `${cookieName}=en; Path=/`

    const { govue } = createAppI18n()

    expect(govue.language.locale.value).toBe('en')
  })

  it('ignores a stored value that is not a language this build serves', () => {
    browserLanguages(['en-US'])
    document.cookie = `${cookieName}=klingon; Path=/`

    const { govue } = createAppI18n()

    expect(govue.language.locale.value).toBe('en')
  })

  it('sets the lang attribute of the document to the resolved language', () => {
    browserLanguages(['en-US'])

    createAppI18n()

    expect(document.documentElement.lang).toBe('en')
  })

  it('stores a chosen language in the cookie the server reads', () => {
    browserLanguages(['de-DE'])
    const { govue } = createAppI18n()

    govue.language.setLocale('en')

    expect(readCookie()).toBe('en')
  })

  it('stores nothing when the language only follows the address', () => {
    browserLanguages(['de-DE'])
    const { govue } = createAppI18n()

    govue.language.setLocale('en', { persist: false })

    expect(govue.language.locale.value).toBe('en')
    expect(readCookie()).toBeUndefined()
  })

  it('ignores a language this build does not serve', () => {
    browserLanguages(['de-DE'])
    const { govue } = createAppI18n()

    govue.language.setLocale('fr')

    expect(govue.language.locale.value).toBe('de')
    expect(readCookie()).toBeUndefined()
  })

  it('shares one instance, so a page message follows the language', () => {
    browserLanguages(['en-GB'])

    const { i18n } = createAppI18n()

    expect(i18n.global.t('nav.pricing')).toBe('Pricing')
  })

  it('carries govue own texts next to the page messages', () => {
    browserLanguages(['de-DE'])

    const { i18n } = createAppI18n()

    expect(i18n.global.t('govue.legal.link.imprint')).toBe('Impressum')
  })

  it('names the terms link after the page, not govue seller wording', () => {
    browserLanguages(['de-DE'])

    const { i18n } = createAppI18n()

    expect(i18n.global.t('govue.legal.link.terms')).toBe('Nutzungsbedingungen')
  })
})
