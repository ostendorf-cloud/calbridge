import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n } from './i18n'
import { createAppRouter } from './router'
import { clearMeta, setAppUrlMeta, setOperatorMeta } from './testSupport'
import App from './App.vue'

async function renderApp(path = '/', locale: 'de' | 'en' = 'en') {
  const { i18n, govue } = createAppI18n()
  govue.language.setLocale(locale, { persist: false })
  const router = createAppRouter(govue.language, createMemoryHistory())
  await router.push(path)
  await router.isReady()

  const view = render(App, { global: { plugins: [router, i18n, govue] } })
  return { ...view, router, language: govue.language }
}

describe('App', () => {
  beforeEach(() => {
    setAppUrlMeta('https://app.calbridge.example.com')
    setOperatorMeta()
    vi.stubGlobal('location', { ...window.location, assign: vi.fn() })
  })

  afterEach(() => {
    clearMeta()
    document.cookie = 'govue-language=; Path=/; Max-Age=0'
    vi.unstubAllGlobals()
  })

  it('renders the skip link ahead of the current route view', async () => {
    await renderApp()

    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(await screen.findByRole('heading', { level: 1 })).toBeVisible()
  })

  it.each([
    ['en', '/en/imprint', '/en/privacy', '/en/terms', ['Imprint', 'Privacy', 'Terms of use']],
    [
      'de',
      '/de/imprint',
      '/de/privacy',
      '/de/terms',
      ['Impressum', 'Datenschutz', 'Nutzungsbedingungen'],
    ],
  ] as const)(
    'links the imprint, the privacy policy and the terms from the footer in %s',
    async (locale, imprint, privacy, terms, labels) => {
      await renderApp('/', locale)

      const footer = screen.getByRole('navigation', {
        name: locale === 'en' ? 'Footer' : 'Fußzeile',
      })
      const links = Array.from(footer.querySelectorAll('a')).map((a) => [
        a.textContent?.trim(),
        a.getAttribute('href'),
      ])

      expect(links).toEqual([
        [labels[0], imprint],
        [labels[1], privacy],
        [labels[2], terms],
      ])
    },
  )

  it.each(['/', '/pricing', '/en/imprint', '/en/privacy', '/en/terms', '/no/such/page'])(
    'shows the footer links on %s',
    async (path) => {
      await renderApp(path)

      expect(screen.getByRole('navigation', { name: 'Footer' })).toBeVisible()
    },
  )

  it('offers the theme switch in the header', async () => {
    await renderApp()

    expect(screen.getByRole('radiogroup')).toBeVisible()
  })

  it.each([
    ['/en/imprint', 'de', '/de/imprint'],
    ['/en/privacy', 'de', '/de/privacy'],
    ['/en/terms', 'de', '/de/terms'],
    ['/de/imprint', 'en', '/en/imprint'],
    ['/de/privacy', 'en', '/en/privacy'],
    ['/de/terms', 'en', '/en/terms'],
  ] as const)(
    'moves %s to the same page in the other language with the switch',
    async (path, target, expected) => {
      const locale = path.startsWith('/en') ? 'en' : 'de'
      const { router, language } = await renderApp(path, locale)

      await userEvent.selectOptions(screen.getByRole('combobox'), target)

      expect(router.currentRoute.value.path).toBe(expected)
      expect(language.locale.value).toBe(target)
    },
  )

  it('changes the language without leaving a page that carries no prefix', async () => {
    const { router } = await renderApp('/pricing')

    await userEvent.selectOptions(screen.getByRole('combobox'), 'de')

    expect(router.currentRoute.value.path).toBe('/pricing')
    expect(await screen.findByRole('heading', { level: 1, name: 'Preise' })).toBeVisible()
  })

  it('sends the "open the application" button to whatever host is configured', async () => {
    await renderApp()
    await userEvent.click(
      within(screen.getByRole('banner')).getByRole('button', { name: 'Open the application' }),
    )

    expect(window.location.assign).toHaveBeenCalledWith('https://app.calbridge.example.com')
  })

  it('sends the same button to a different configured host, proving it is not hardcoded', async () => {
    setAppUrlMeta('https://app.staging.example.org')

    await renderApp()
    await userEvent.click(
      within(screen.getByRole('banner')).getByRole('button', { name: 'Open the application' }),
    )

    expect(window.location.assign).toHaveBeenCalledWith('https://app.staging.example.org')
  })
})
