// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { ApiError, ErrorCode } from '@ostendorf-cloud/govue'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

import en from '../i18n/locales/en.json'
import { apiClient } from '../api'
import CanvasView from './CanvasView.vue'

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

const accounts = [
  { id: 'acc-work', provider: 'google', display_name: 'Work Google' },
  { id: 'acc-personal', provider: 'microsoft', display_name: 'Personal Outlook' },
]

const ownCalendars = [
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
    id: 'cal-personal',
    account_id: 'acc-personal',
    name: 'Personal',
    managed: false,
    shared: false,
    owner_email: null,
    max_detail_level: null,
  },
]

const sharedCalendar = {
  id: 'cal-anna',
  account_id: null,
  name: "Anna's team calendar",
  managed: false,
  shared: true,
  owner_email: 'anna@example.com',
  max_detail_level: 'title_time',
}

function renderCanvasView() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'canvas', component: CanvasView },
      { path: '/login', name: 'login', component: { template: '<div />' } },
    ],
  })
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })

  return { router, ...render(CanvasView, { global: { plugins: [router, i18n] } }) }
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

/** sourceHandleFor and targetHandleFor find a calendar node's own Vue Flow
 *  handle, the element a pointer drag starts or ends on. Vue Flow also
 *  completes a connection on two clicks, one per handle, with no drag
 *  involved: jsdom has no layout engine, so it always answers
 *  elementFromPoint with null, and Vue Flow's own click path falls back
 *  to the clicked handle's identity rather than its screen position for
 *  that reason. Driving that path through these two elements therefore
 *  exercises the same onConnect callback a real drag would, without
 *  reaching for a pointer-coordinate simulation jsdom cannot support. */
function sourceHandleFor(calendarName: string): HTMLElement {
  const group = screen.getByRole('group', { name: calendarName })
  const handle = group.querySelector<HTMLElement>('.vue-flow__handle.source')
  if (!handle) {
    throw new Error(`no source handle rendered for ${calendarName}`)
  }
  return handle
}

function targetHandleFor(calendarName: string): HTMLElement {
  const group = screen.getByRole('group', { name: calendarName })
  const handle = group.querySelector<HTMLElement>('.vue-flow__handle.target')
  if (!handle) {
    throw new Error(`no target handle rendered for ${calendarName}`)
  }
  return handle
}

describe('CanvasView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a node per calendar, grouped by account, with the shared group separate', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: [...ownCalendars, sharedCalendar],
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })

    renderCanvasView()

    // Vue Flow renders every node hidden until it has measured its real
    // size, which happens asynchronously through a ResizeObserver; the
    // text is already in the document by then, so the visibility itself
    // is what this waits on, not the content.
    await vi.waitFor(() => {
      expect(screen.getByText('Work Google')).toBeVisible()
      expect(screen.getByText('Personal Outlook')).toBeVisible()
      expect(screen.getByText('Work')).toBeVisible()
      expect(screen.getByText('Personal')).toBeVisible()
      expect(screen.getByText('Shared with you')).toBeVisible()
      expect(screen.getByText("Anna's team calendar")).toBeVisible()
    })
  })

  it('creates an edge end to end through the keyboard-reachable path, without a pointer drag', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    vi.mocked(apiClient.post).mockResolvedValue({
      id: 'edge-1',
      source_calendar_id: 'cal-work',
      target_calendar_id: 'cal-personal',
      detail_level: 'busy_block',
    })
    const user = userEvent.setup()
    renderCanvasView()

    // The button lives inside a Vue Flow node, hidden until measured; a
    // plain getByRole would run before that and come back empty. Every
    // control from here on is driven by the keyboard alone, matching the
    // test's own name: a pointer click on the same elements would exercise
    // the same handlers and could not tell a keyboard-only path from one
    // that only happens to also accept a click.
    const connectFromWork = await screen.findByRole('button', { name: 'Connect from here: Work' })
    connectFromWork.focus()
    await user.keyboard('{Enter}')
    expect(screen.getByText('Connecting from Work. Choose a target below.')).toBeVisible()

    await chooseOption('Target calendar', 'Personal (Personal Outlook)')
    await chooseOption('Detail level', 'Busy or free only')

    const submit = screen.getByRole('button', { name: 'Create connection' })
    submit.focus()
    await user.keyboard('{Enter}')

    await waitFor(() =>
      expect(apiClient.post).toHaveBeenCalledWith('/edges', {
        source_calendar_id: 'cal-work',
        target_calendar_id: 'cal-personal',
        detail_level: 'busy_block',
      }),
    )
  })

  it('moves focus to the target calendar select once a node fills the source field', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    const user = userEvent.setup()
    renderCanvasView()

    const connectFromWork = await screen.findByRole('button', { name: 'Connect from here: Work' })
    await user.click(connectFromWork)

    // Without this, a keyboard user who just pressed this button would have
    // to tab past every remaining node to reach the field the announcement
    // just told them to fill in.
    await waitFor(() =>
      expect(
        screen.getByRole('combobox', { name: /^Target calendar( \(required\))?$/ }),
      ).toHaveFocus(),
    )
  })

  it('gives a calendar node no tab stop of its own beside its connect button', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    renderCanvasView()

    // Vue Flow's own node wrapper is already one focusable stop; a second,
    // manual one on the node body duplicates it without doing anything on
    // activation.
    const node = await screen.findByRole('group', { name: 'Work' })
    expect(node).not.toHaveAttribute('tabindex')
  })

  it('renders the shared navigation back to the other views', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    renderCanvasView()
    await screen.findByText('Work')

    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'Grants' })).toHaveAttribute('href', '/grants')
  })

  it('shows the field list of the chosen preset, taken from the server-sent detail levels', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [
        {
          id: 'edge-1',
          source_calendar_id: 'cal-work',
          target_calendar_id: 'cal-personal',
          detail_level: 'title_time',
        },
      ],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    const user = userEvent.setup()
    renderCanvasView()
    await screen.findByText('Work')

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    await user.click(screen.getByText('Which fields does this detail level copy?'))

    expect(screen.getByText('Title')).toBeVisible()
    expect(screen.getByText('Status')).toBeVisible()
    expect(screen.getByText('Availability')).toBeVisible()
    expect(screen.getByText('Visibility')).toBeVisible()
    expect(screen.getByText('Recurrence')).toBeVisible()
    expect(screen.queryByText('Description')).toBeNull()
    expect(screen.queryByText('Location')).toBeNull()
    expect(screen.queryByText('Attendees')).toBeNull()
  })

  it("cannot give a shared calendar's connection a preset wider than the grant's maximum", async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: [ownCalendars[0], sharedCalendar],
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    renderCanvasView()
    await screen.findByText('Work')

    await chooseOption('Source', "Anna's team calendar (Shared with you)")
    await chooseOption('Target calendar', 'Work (Work Google)')

    const detailLevelTrigger = screen.getByRole('combobox', {
      name: /^Detail level( \(required\))?$/,
    })
    await fireEvent.keyDown(detailLevelTrigger, { key: 'ArrowDown' })

    expect(screen.getAllByRole('option').map((option) => option.textContent?.trim())).toEqual([
      'Title and time',
      'Busy or free only',
    ])
  })

  it('sends the target form instead of a target calendar id when calbridge should create the target', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts: [accounts[0]],
      calendars: [ownCalendars[0]],
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    vi.mocked(apiClient.post).mockResolvedValue({
      id: 'edge-1',
      source_calendar_id: 'cal-work',
      target_calendar_id: 'cal-mirror',
      detail_level: 'full',
      created_calendar: {
        id: 'cal-mirror',
        account_id: 'acc-work',
        name: 'Mirror of Work',
        managed: true,
        shared: false,
        owner_email: null,
        max_detail_level: null,
      },
    })
    const user = userEvent.setup()
    renderCanvasView()
    await screen.findByText('Work')

    await chooseOption('Source', 'Work (Work Google)')
    await chooseOption('Target', 'Let calbridge create a new calendar')
    await chooseOption('Account for the new calendar', 'Work Google')
    // exact: false, since the label's accessible text carries a trailing
    // "(required)" this test does not repeat.
    await user.type(
      await screen.findByLabelText('Name of the new calendar', { exact: false }),
      'Mirror of Work',
    )
    await user.click(screen.getByRole('button', { name: 'Create connection' }))

    await waitFor(() =>
      expect(apiClient.post).toHaveBeenCalledWith('/edges', {
        source_calendar_id: 'cal-work',
        target: { account_id: 'acc-work', name: 'Mirror of Work' },
        detail_level: 'full',
      }),
    )
  })

  it('connects two calendars through their own handles, the pointerless equivalent of a drag', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    vi.mocked(apiClient.post).mockResolvedValue({
      id: 'edge-1',
      source_calendar_id: 'cal-work',
      target_calendar_id: 'cal-personal',
      detail_level: 'busy_block',
    })
    renderCanvasView()
    // Waits for visibility, not just presence: Vue Flow keeps every node
    // hidden until it has measured it, and a hidden node's handle has no
    // accessible role yet for sourceHandleFor/targetHandleFor to find.
    await screen.findByRole('button', { name: 'Connect from here: Work' })

    await fireEvent.click(sourceHandleFor('Work'))
    await fireEvent.click(targetHandleFor('Personal'))

    await waitFor(() =>
      expect(apiClient.post).toHaveBeenCalledWith('/edges', {
        source_calendar_id: 'cal-work',
        target_calendar_id: 'cal-personal',
        detail_level: 'busy_block',
      }),
    )
  })

  it('refuses a connection into a calendar it does not own, from the same handle-to-handle path', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: [ownCalendars[0], sharedCalendar],
      edges: [],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    renderCanvasView()
    await screen.findByRole('button', { name: 'Connect from here: Work' })

    await fireEvent.click(sourceHandleFor('Work'))
    await fireEvent.click(targetHandleFor("Anna's team calendar"))

    expect(screen.getByText('Only a calendar you own can be a target.')).toBeVisible()
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it('saves a new detail level through a PATCH when the edge editor is confirmed', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [
        {
          id: 'edge-1',
          source_calendar_id: 'cal-work',
          target_calendar_id: 'cal-personal',
          detail_level: 'title_time',
        },
      ],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    vi.mocked(apiClient.patch).mockResolvedValue({
      id: 'edge-1',
      source_calendar_id: 'cal-work',
      target_calendar_id: 'cal-personal',
      detail_level: 'busy_block',
    })
    const user = userEvent.setup()
    renderCanvasView()
    await screen.findByText('Work')

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(apiClient.patch).toHaveBeenCalledWith('/edges/edge-1', { detail_level: 'title_time' }),
    )
    // Scoped to the edge's own row: the edge label on the canvas itself
    // shows the same text, and a plain getByText would be ambiguous.
    const edgeRow = screen.getByText('Work → Personal').closest('li')
    expect(edgeRow).not.toBeNull()
    expect(within(edgeRow as HTMLElement).getByText('Busy or free only')).toBeVisible()
  })

  it('states the deletion consequence and sends a DELETE once the user confirms', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({
      accounts,
      calendars: ownCalendars,
      edges: [
        {
          id: 'edge-1',
          source_calendar_id: 'cal-work',
          target_calendar_id: 'cal-personal',
          detail_level: 'title_time',
        },
      ],
      grants: { given: [], received: [] },
      detail_levels: detailLevels,
    })
    vi.mocked(apiClient.del).mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderCanvasView()
    await screen.findByText('Work → Personal')

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
      'Deleting removes every event this connection wrote into its target.',
    )

    await user.click(screen.getByRole('button', { name: 'Delete the connection' }))

    expect(apiClient.del).toHaveBeenCalledWith('/edges/edge-1')
    await waitFor(() => expect(screen.queryByText('Work → Personal')).toBeNull())
  })

  it('redirects to login when the session is not authenticated', async () => {
    vi.mocked(apiClient.get).mockRejectedValue(
      new ApiError({ code: ErrorCode.unauthenticated, message: 'log in first', status: 401 }),
    )
    const { router } = renderCanvasView()

    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
  })
})
