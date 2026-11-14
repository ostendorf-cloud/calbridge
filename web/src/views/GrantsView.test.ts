import { ApiError, ErrorCode } from '@ostendorf-cloud/govue'
import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import en from '../i18n/locales/en.json'
import { apiClient } from '../api'
import GrantsView from './GrantsView.vue'

vi.mock('../api', () => ({
  apiClient: { get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn(), del: vi.fn() },
  csrfToken: { value: undefined },
}))

const detailLevels = [
  {
    level: 'full',
    fields: [
      'summary',
      'description',
      'location',
      'attendees',
      'status',
      'transparency',
      'visibility',
      'recurrence',
    ],
  },
  {
    level: 'title_time',
    fields: ['summary', 'status', 'transparency', 'visibility', 'recurrence'],
  },
  { level: 'busy_block', fields: ['status', 'transparency'] },
]

const calendars = [
  {
    id: 'cal-work',
    account_id: 'acc-work',
    name: 'Work',
    managed: false,
    shared: false,
    owner_email: null,
    max_detail_level: null,
  },
  {
    id: 'cal-anna',
    account_id: null,
    name: "Anna's team calendar",
    managed: false,
    shared: true,
    owner_email: 'anna@example.com',
    max_detail_level: 'title_time',
  },
]

const givenGrant = {
  id: 'grant-given-1',
  calendar_id: 'cal-work',
  counterparty_email: 'bob@example.com',
  max_detail_level: 'busy_block',
}

const receivedGrant = {
  id: 'grant-received-1',
  calendar_id: 'cal-anna',
  counterparty_email: 'anna@example.com',
  max_detail_level: 'title_time',
}

function mockGraph(overrides: Partial<{ given: unknown[]; received: unknown[] }> = {}) {
  vi.mocked(apiClient.get).mockResolvedValue({
    calendars,
    detail_levels: detailLevels,
    grants: {
      given: overrides.given ?? [givenGrant],
      received: overrides.received ?? [receivedGrant],
    },
  })
}

function renderGrantsView() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'grants', component: GrantsView },
      { path: '/login', name: 'login', component: { template: '<div />' } },
    ],
  })
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })

  return { router, ...render(GrantsView, { global: { plugins: [router, i18n] } }) }
}

/** chooseOption drives a GovueSelect the same way its own test suite does:
 *  ArrowDown opens the list, and Enter on the focused option commits it. A
 *  pointer click on a Reka UI select does not register reliably in jsdom.
 *  The name allows an optional trailing "(required)", since a required
 *  field's accessible name carries that suffix and this helper does not
 *  repeat it for every call site. */
async function chooseOption(comboboxName: string, optionName: string): Promise<void> {
  const trigger = screen.getByRole('combobox', {
    name: new RegExp(`^${comboboxName}( \\(required\\))?$`),
  })
  await fireEvent.keyDown(trigger, { key: 'ArrowDown' })
  const option = await screen.findByRole('option', { name: optionName })
  option.focus()
  await fireEvent.keyDown(option, { key: 'Enter' })
  await waitFor(() => expect(screen.queryByRole('option', { name: optionName })).toBeNull())
}

const consequenceSentence =
  'Revoking tears down every connection that uses this grant, and deletes the events those connections wrote into their target.'

describe('GrantsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('lists what was granted by the user and what was granted to the user', async () => {
    mockGraph()
    renderGrantsView()

    expect(await screen.findByText('Work → bob@example.com')).toBeVisible()
    expect(screen.getByText("Anna's team calendar (anna@example.com)")).toBeVisible()
    expect(screen.getByText('Busy or free only')).toBeVisible()
    expect(screen.getByText('Title and time')).toBeVisible()
  })

  it('grants access with the calendar, the email and the maximum detail level the form holds', async () => {
    mockGraph({ given: [] })
    vi.mocked(apiClient.post).mockResolvedValue({
      id: 'grant-new',
      calendar_id: 'cal-work',
      counterparty_email: 'carla@example.com',
      max_detail_level: 'title_time',
    })
    const user = userEvent.setup()
    renderGrantsView()
    await screen.findByRole('heading', { name: 'Grant access' })

    await chooseOption('Your calendar', 'Work')
    // exact: false, since the label's accessible text carries a trailing
    // "(required)" this test does not repeat.
    await user.type(
      screen.getByLabelText("The person's email address", { exact: false }),
      'carla@example.com',
    )
    await chooseOption('Maximum detail level', 'Title and time')
    await user.click(screen.getByRole('button', { name: 'Grant access' }))

    await waitFor(() =>
      expect(apiClient.post).toHaveBeenCalledWith('/grants', {
        grantee_email: 'carla@example.com',
        calendar_id: 'cal-work',
        max_detail_level: 'title_time',
      }),
    )
    expect(await screen.findByText('Work → carla@example.com')).toBeVisible()
  })

  it('states the revoke consequence on the control for a grant the user gave', async () => {
    mockGraph()
    const user = userEvent.setup()
    renderGrantsView()
    await screen.findByText('Work → bob@example.com')

    await user.click(screen.getAllByRole('button', { name: 'Revoke' })[0])

    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(consequenceSentence)
  })

  it('states the same revoke consequence on the control for a grant the user received', async () => {
    mockGraph()
    const user = userEvent.setup()
    renderGrantsView()
    await screen.findByText("Anna's team calendar (anna@example.com)")

    await user.click(screen.getAllByRole('button', { name: 'Revoke' })[1])

    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(consequenceSentence)
  })

  it('removes a grant from its list once the revoke is confirmed', async () => {
    mockGraph()
    vi.mocked(apiClient.del).mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderGrantsView()
    await screen.findByText('Work → bob@example.com')

    await user.click(screen.getAllByRole('button', { name: 'Revoke' })[0])
    await user.click(screen.getByRole('button', { name: 'Revoke the grant' }))

    expect(apiClient.del).toHaveBeenCalledWith('/grants/grant-given-1')
    await vi.waitFor(() => expect(screen.queryByText('Work → bob@example.com')).toBeNull())
  })

  it('renders the shared navigation back to the other views', async () => {
    mockGraph()
    renderGrantsView()
    await screen.findByText('Work → bob@example.com')

    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'Calendar canvas' })).toHaveAttribute('href', '/canvas')
  })

  it('redirects to login when the session is not authenticated', async () => {
    vi.mocked(apiClient.get).mockRejectedValue(
      new ApiError({ code: ErrorCode.unauthenticated, message: 'log in first', status: 401 }),
    )
    const { router } = renderGrantsView()

    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
  })
})
