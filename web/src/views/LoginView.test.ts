import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import en from '../i18n/locales/en.json'
import { apiClient } from '../api'
import LoginView from './LoginView.vue'

vi.mock('../api', () => ({
  apiClient: { get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn(), del: vi.fn() },
  csrfToken: { value: undefined },
}))

function setPublicSiteUrlMeta(content: string | undefined): void {
  document.head.querySelector('meta[name="calbridge-public-site-url"]')?.remove()
  if (content === undefined) {
    return
  }
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-public-site-url')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

function renderLoginView() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/login', name: 'login', component: LoginView },
    ],
  })
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })

  return { router, ...render(LoginView, { global: { plugins: [router, i18n] } }) }
}

describe('LoginView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(apiClient.get).mockResolvedValue({ providers: [] })
    setPublicSiteUrlMeta(undefined)
  })

  it('signs in with email, password and the totp code and routes to home', async () => {
    vi.mocked(apiClient.post).mockResolvedValue(undefined)
    const user = userEvent.setup()
    const { router } = renderLoginView()

    await user.type(screen.getByLabelText(/Email/), 'owner@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct-horse')
    await user.type(screen.getByLabelText(/Authentication code/), '123456')
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(apiClient.post).toHaveBeenCalledWith('/auth/login', {
      email: 'owner@example.com',
      password: 'correct-horse',
      totp_code: '123456',
    })
    expect(router.currentRoute.value.name).toBe('home')
  })

  it('shows the returned totp secret and otpauth uri after signing up', async () => {
    vi.mocked(apiClient.post).mockResolvedValue({
      totp_secret: 'JBSWY3DPEHPK3PXP',
      totp_uri:
        'otpauth://totp/calbridge:owner@example.com?secret=JBSWY3DPEHPK3PXP&issuer=calbridge',
    })
    const user = userEvent.setup()
    renderLoginView()

    await user.click(screen.getByRole('button', { name: 'Need an account? Sign up' }))
    await user.type(screen.getByLabelText(/Email/), 'owner@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct-horse')
    await user.click(screen.getByRole('button', { name: 'Sign up' }))

    expect(apiClient.post).toHaveBeenCalledWith('/auth/signup', {
      email: 'owner@example.com',
      password: 'correct-horse',
    })
    expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeVisible()
    expect(
      screen.getByText(
        'otpauth://totp/calbridge:owner@example.com?secret=JBSWY3DPEHPK3PXP&issuer=calbridge',
      ),
    ).toBeVisible()
  })

  it('renders a button for every provider the backend configured', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({ providers: configuredProviders })
    renderLoginView()

    expect(await screen.findByRole('button', { name: 'Continue with Google' })).toBeVisible()
    expect(await screen.findByRole('button', { name: 'Continue with Apple' })).toBeVisible()
  })

  it('notes that the Apple provider only confirms identity and connects no calendar', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({ providers: configuredProviders })
    renderLoginView()

    await screen.findByRole('button', { name: 'Continue with Apple' })
    expect(screen.getByText('Confirms who you are only. No calendar is connected.')).toBeVisible()
  })

  // Microsoft and Apple hand over no address calbridge will believe, so
  // an account created through either carries a placeholder one that
  // nothing in this release can change. The person has to be told that
  // before they choose, not after.
  it('warns, before the choice is made, that a provider will leave the account without a real address', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({ providers: configuredProviders })
    renderLoginView()

    await screen.findByRole('button', { name: 'Continue with Microsoft' })

    const warnings = screen.getAllByText(/does not give us a confirmed email address/)
    expect(warnings).toHaveLength(2)

    expect(
      screen.getByText(
        'Microsoft does not give us a confirmed email address, so your account will show a placeholder one that you cannot change yet. Sign up with an email address and password if you need your own address shown.',
      ),
    ).toBeVisible()
  })

  it('does not warn about an address for a provider that confirms one', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      providers: [{ id: 'google', name: 'Google', identity_only: false, placeholder_email: false }],
    })
    renderLoginView()

    await screen.findByRole('button', { name: 'Continue with Google' })
    expect(screen.queryByText(/does not give us a confirmed email address/)).toBeNull()
  })

  it('links back to the configured public site host, not a hardcoded one', () => {
    setPublicSiteUrlMeta('https://calbridge.example.com')

    renderLoginView()

    expect(screen.getByRole('link', { name: 'Back to the calbridge homepage' })).toHaveAttribute(
      'href',
      'https://calbridge.example.com',
    )
  })

  it('links back to a different configured host too', () => {
    setPublicSiteUrlMeta('https://landing.staging.example.org')

    renderLoginView()

    expect(screen.getByRole('link', { name: 'Back to the calbridge homepage' })).toHaveAttribute(
      'href',
      'https://landing.staging.example.org',
    )
  })

  it('shows no link back to the public site when none is configured', () => {
    renderLoginView()

    expect(screen.queryByRole('link', { name: 'Back to the calbridge homepage' })).toBeNull()
  })
})

// configuredProviders is the full set a deployment with all three
// configured answers with, including which of them leaves the account on
// a placeholder address.
const configuredProviders = [
  { id: 'google', name: 'Google', identity_only: false, placeholder_email: false },
  { id: 'microsoft', name: 'Microsoft', identity_only: false, placeholder_email: true },
  { id: 'apple', name: 'Apple', identity_only: true, placeholder_email: true },
]
