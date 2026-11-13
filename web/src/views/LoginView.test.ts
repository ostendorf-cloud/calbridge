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
  })

  it('submits the email to the login endpoint', async () => {
    vi.mocked(apiClient.post).mockResolvedValue(undefined)
    const user = userEvent.setup()
    const { router } = renderLoginView()

    await user.type(screen.getByLabelText(/Email/), 'owner@example.com')
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(apiClient.post).toHaveBeenCalledWith('/auth/login', { email: 'owner@example.com' })
    expect(router.currentRoute.value.name).toBe('home')
  })

  it('does not submit when the email is left empty', async () => {
    const user = userEvent.setup()
    renderLoginView()

    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(apiClient.post).not.toHaveBeenCalled()
    expect(await screen.findByText('Please enter an email address.')).toBeVisible()
  })
})
