// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// CanvasView.test.ts and GrantsView.test.ts both mock the whole '../api'
// module, token ref included, which proves nothing about whether the token
// a real session gets from GET /api/v1/me ever reaches a real write. This
// file is the one that does: it leaves '../api' untouched, so the real
// govue client and the real csrfToken ref run, and only the network edge
// (fetch) is replaced. A regression here is a session that cannot create
// an edge or grant a share in an actual browser.
import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Component } from 'vue'

import en from '../i18n/locales/en.json'

// vi.hoisted runs before the "../api" and view imports below evaluate, so
// the stub is already globalThis.fetch by the time api.ts's createClient
// binds it. Stubbing afterwards would be too late: govue's client binds
// fetch once, at module load, not on every call.
const fetchMock = vi.hoisted(() => {
  const mock = vi.fn()
  vi.stubGlobal('fetch', mock)
  return mock
})

import { csrfToken } from '../api'
import CanvasView from './CanvasView.vue'
import GrantsView from './GrantsView.vue'

const CSRF_TOKEN = 'csrf-real-abc'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function methodOf(init: RequestInit | undefined): string {
  return init?.method ?? 'GET'
}

/** chooseOption drives a GovueSelect the way its own test suite does:
 *  ArrowDown opens the list, Enter on the focused option commits it. */
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

function renderView(component: Component) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'view', component },
      { path: '/login', name: 'login', component: { template: '<div />' } },
    ],
  })
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
  return render(component, { global: { plugins: [router, i18n] } })
}

describe('the CSRF token a real session obtains', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    csrfToken.value = undefined
  })

  it('reaches the header of a write CanvasView sends, after CanvasView bootstraps it itself', async () => {
    fetchMock.mockImplementation((input: string | URL, init?: RequestInit) => {
      const path = input.toString()
      if (path.endsWith('/api/v1/me') && methodOf(init) === 'GET') {
        return jsonResponse({ email: 'owner@example.com', csrf_token: CSRF_TOKEN })
      }
      if (path.endsWith('/api/v1/graph') && methodOf(init) === 'GET') {
        return jsonResponse({
          accounts: [{ id: 'acc-work', provider: 'google', display_name: 'Work Google' }],
          calendars: [
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
              account_id: 'acc-work',
              name: 'Personal',
              managed: false,
              shared: false,
              owner_email: null,
              max_detail_level: null,
            },
          ],
          edges: [],
          grants: { given: [], received: [] },
          detail_levels: [{ level: 'busy_block', fields: ['status', 'transparency'] }],
        })
      }
      if (path.endsWith('/api/v1/edges') && methodOf(init) === 'POST') {
        return jsonResponse({
          id: 'edge-1',
          source_calendar_id: 'cal-work',
          target_calendar_id: 'cal-personal',
          detail_level: 'busy_block',
        })
      }
      throw new Error(`unexpected fetch ${methodOf(init)} ${path}`)
    })

    const user = userEvent.setup()
    renderView(CanvasView)

    const connectFromWork = await screen.findByRole('button', { name: 'Connect from here: Work' })
    await user.click(connectFromWork)
    await chooseOption('Target calendar', 'Personal (Work Google)')
    await user.click(screen.getByRole('button', { name: 'Create connection' }))

    await waitFor(() => expect(csrfToken.value).toBe(CSRF_TOKEN))
    await waitFor(() => {
      const edgeCall = fetchMock.mock.calls.find(
        ([url, init]) => url.toString().endsWith('/api/v1/edges') && methodOf(init) === 'POST',
      )
      expect(edgeCall).toBeDefined()
      const [, init] = edgeCall as [string, RequestInit]
      expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe(CSRF_TOKEN)
    })
  })

  it('reaches the header of a write GrantsView sends, after GrantsView bootstraps it itself', async () => {
    fetchMock.mockImplementation((input: string | URL, init?: RequestInit) => {
      const path = input.toString()
      if (path.endsWith('/api/v1/me') && methodOf(init) === 'GET') {
        return jsonResponse({ email: 'owner@example.com', csrf_token: CSRF_TOKEN })
      }
      if (path.endsWith('/api/v1/graph') && methodOf(init) === 'GET') {
        return jsonResponse({
          calendars: [
            {
              id: 'cal-work',
              account_id: 'acc-work',
              name: 'Work',
              managed: false,
              shared: false,
              owner_email: null,
              max_detail_level: null,
            },
          ],
          detail_levels: [{ level: 'busy_block', fields: ['status', 'transparency'] }],
          grants: { given: [], received: [] },
        })
      }
      if (path.endsWith('/api/v1/grants') && methodOf(init) === 'POST') {
        return jsonResponse({
          id: 'grant-1',
          calendar_id: 'cal-work',
          counterparty_email: 'carla@example.com',
          max_detail_level: 'busy_block',
        })
      }
      throw new Error(`unexpected fetch ${methodOf(init)} ${path}`)
    })

    const user = userEvent.setup()
    renderView(GrantsView)

    await screen.findByRole('heading', { name: 'Grant access' })
    await chooseOption('Your calendar', 'Work')
    await user.type(
      screen.getByLabelText("The person's email address", { exact: false }),
      'carla@example.com',
    )
    await chooseOption('Maximum detail level', 'Busy or free only')
    await user.click(screen.getByRole('button', { name: 'Grant access' }))

    await waitFor(() => expect(csrfToken.value).toBe(CSRF_TOKEN))
    await waitFor(() => {
      const grantCall = fetchMock.mock.calls.find(
        ([url, init]) => url.toString().endsWith('/api/v1/grants') && methodOf(init) === 'POST',
      )
      expect(grantCall).toBeDefined()
      const [, init] = grantCall as [string, RequestInit]
      expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe(CSRF_TOKEN)
    })
  })
})
