import { createGovueI18n } from '@ostendorf-cloud/govue'
import { createI18n } from 'vue-i18n'

import de from './locales/de.json'
import en from './locales/en.json'

/** The language the first render falls back to when the visitor has no
 *  stored choice and no browser language this build serves. German is the
 *  source of truth for the copy: this is calbridge's public face, on
 *  calbridge.de. */
export const DEFAULT_LOCALE = 'de'

/** createAppI18n builds the one vue-i18n instance and hands it to govue's
 *  language layer, which owns the choice order (stored choice, then the
 *  browser, then the default), the switch, the lang attribute and where the
 *  choice is kept.
 *
 *  The choice lives in a cookie, not in localStorage. The server reads the
 *  same cookie to pick the page the short legal paths redirect to, so a
 *  visitor who chose a language once gets it from /privacy on the next
 *  visit too. govue and the Go side both name it govue-language.
 *
 *  English falls back to German for any key not yet translated, rather than
 *  showing the raw key: German is authored as the complete set of messages.
 *  locales.test.ts is what keeps that fallback from hiding a missing key. */
export function createAppI18n() {
  const i18n = createI18n({
    legacy: false,
    locale: DEFAULT_LOCALE,
    fallbackLocale: DEFAULT_LOCALE,
    missingWarn: true,
    fallbackWarn: false,
    messages: { de, en },
  })
  // govue labels the terms link "AGB", the German word for the general
  // terms of a seller. calbridge sells nothing, so the footer says what the
  // page is called, the same as the page's own title.
  const govue = createGovueI18n({
    i18n,
    detect: true,
    storage: { kind: 'cookie' },
    messages: {
      de: { govue: { legal: { link: { terms: de.terms.title } } } },
      en: { govue: { legal: { link: { terms: en.terms.title } } } },
    },
  })

  return { i18n, govue }
}
