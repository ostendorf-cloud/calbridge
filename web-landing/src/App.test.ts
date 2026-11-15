import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import en from './i18n/locales/en.json'
import App from './App.vue'

function setAppUrlMeta(content: string): void {
  document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-app-url')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

async function renderApp() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'home', component: { template: '<p>home</p>' } }],
  })
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
  await router.push('/')
  await router.isReady()

  return render(App, { global: { plugins: [router, i18n] } })
}

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('location', { ...window.location, assign: vi.fn() })
  })

  afterEach(() => {
    document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
    vi.unstubAllGlobals()
  })

  it('renders the skip link ahead of the current route view', async () => {
    setAppUrlMeta('https://app.calbridge.example.com')

    await renderApp()

    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(await screen.findByText('home')).toBeVisible()
  })

  it('links the imprint and the privacy policy from the footer', async () => {
    setAppUrlMeta('https://app.calbridge.example.com')

    await renderApp()

    expect(screen.getByRole('link', { name: 'Imprint' })).toHaveAttribute('href', '/imprint')
    expect(screen.getByRole('link', { name: 'Privacy policy' })).toHaveAttribute('href', '/privacy')
  })

  it('sends the "open the application" button to whatever host is configured', async () => {
    setAppUrlMeta('https://app.calbridge.example.com')

    await renderApp()
    await userEvent.click(screen.getByRole('button', { name: 'Open the application' }))

    expect(window.location.assign).toHaveBeenCalledWith('https://app.calbridge.example.com')
  })

  it('sends the same button to a different configured host, proving it is not hardcoded', async () => {
    setAppUrlMeta('https://app.staging.example.org')

    await renderApp()
    await userEvent.click(screen.getByRole('button', { name: 'Open the application' }))

    expect(window.location.assign).toHaveBeenCalledWith('https://app.staging.example.org')
  })
})
