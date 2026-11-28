import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { languagePlugins } from '../testSupport'
import NotFoundView from './NotFoundView.vue'

describe('NotFoundView', () => {
  it.each([
    ['en', 'Page not found', 'Back to the start page'],
    ['de', 'Seite nicht gefunden', 'Zurück zur Startseite'],
  ] as const)('says the page is missing in %s and links home', (locale, title, home) => {
    render(NotFoundView, { global: { plugins: languagePlugins(locale).plugins } })

    expect(screen.getByRole('heading', { level: 1, name: title })).toBeVisible()
    expect(screen.getByRole('link', { name: home })).toHaveAttribute('href', '/')
  })
})
