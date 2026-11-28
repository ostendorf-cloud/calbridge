import { render, screen } from '@testing-library/vue'
import { afterEach, describe, expect, it } from 'vitest'

import { clearMeta, languagePlugins, setOperatorMeta, testOperator } from '../testSupport'
import PrivacyView from './PrivacyView.vue'

describe('PrivacyView', () => {
  afterEach(clearMeta)

  it('names the controller the deployment configures', () => {
    setOperatorMeta()
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.getByRole('heading', { level: 2, name: 'Controller' })).toBeVisible()
    expect(screen.getByText('Erika Mustermann')).toBeVisible()
    expect(screen.getByText('10115 Berlin')).toBeVisible()
    expect(screen.getByRole('link', { name: 'kontakt@calbridge.example' })).toHaveAttribute(
      'href',
      'mailto:kontakt@calbridge.example',
    )
  })

  it('names the privacy contact only when the deployment set one', () => {
    setOperatorMeta()
    const { unmount } = render(PrivacyView, {
      global: { plugins: languagePlugins('en').plugins },
    })
    expect(screen.queryByText(/Contact for privacy questions/)).not.toBeInTheDocument()
    unmount()

    setOperatorMeta({ ...testOperator, dataProtection: 'datenschutz@calbridge.example' })
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(
      screen.getByText('Contact for privacy questions: datenschutz@calbridge.example'),
    ).toBeVisible()
  })

  it('says the operator details are unavailable instead of a blank controller', () => {
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(
      screen.getByText(
        'The operator details are not available on this page. Please come back later.',
      ),
    ).toBeVisible()
  })

  it.each([
    'Signing in with Google, Microsoft or Apple',
    'Calendar access at Google and Microsoft',
    'How long data is kept',
    'Revoking access',
    'Data from Google APIs',
    'Language and theme on this site',
  ])('carries the provider data section %s', (heading) => {
    setOperatorMeta()
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.getByRole('heading', { level: 2, name: heading })).toBeVisible()
  })

  it('states that sign-in asks for identity only and refresh tokens are stored encrypted', () => {
    setOperatorMeta()
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(screen.getByText(/Signing in with a provider asks for identity only/)).toBeVisible()
    expect(screen.getByText(/stores a refresh token only encrypted/)).toBeVisible()
  })

  it('carries the Google Limited Use statement', () => {
    setOperatorMeta()
    render(PrivacyView, { global: { plugins: languagePlugins('en').plugins } })

    expect(
      screen.getByText(/Google API Services User Data Policy, including the Limited Use/),
    ).toBeVisible()
  })
})
