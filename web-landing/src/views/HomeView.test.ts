import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'

import en from '../i18n/locales/en.json'
import HomeView from './HomeView.vue'

function setAppUrlMeta(content: string): void {
  document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-app-url')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

function renderHomeView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
  return render(HomeView, { global: { plugins: [i18n] } })
}

describe('HomeView', () => {
  beforeEach(() => {
    vi.stubGlobal('location', { ...window.location, assign: vi.fn() })
  })

  afterEach(() => {
    document.head.querySelector('meta[name="calbridge-app-url"]')?.remove()
    vi.unstubAllGlobals()
  })

  it('explains all three detail levels calbridge offers', () => {
    renderHomeView()

    expect(screen.getByRole('heading', { name: 'Full' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Title and time' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Busy or free only' })).toBeVisible()
    expect(screen.getByText(/no title, no description, nothing else/)).toBeVisible()
  })

  it('sends the hero call to action to the configured application host', async () => {
    setAppUrlMeta('https://app.calbridge.example.com')

    renderHomeView()
    const [heroButton] = screen.getAllByRole('button', { name: 'Open the application' })
    await userEvent.click(heroButton)

    expect(window.location.assign).toHaveBeenCalledWith('https://app.calbridge.example.com')
  })

  it('sends the closing call to action to a different configured host too', async () => {
    setAppUrlMeta('https://app.staging.example.org')

    renderHomeView()
    // The hero and the closing section both carry a button with the same
    // accessible name; the second one is the closing call to action.
    const buttons = screen.getAllByRole('button', { name: 'Open the application' })
    await userEvent.click(buttons[1])

    expect(window.location.assign).toHaveBeenCalledWith('https://app.staging.example.org')
  })
})
