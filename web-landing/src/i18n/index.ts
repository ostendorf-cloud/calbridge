import { createI18n } from 'vue-i18n'

import de from './locales/de.json'
import en from './locales/en.json'

/** The only two locales this build serves, the same two the application
 *  carries. German is the source of truth: this is calbridge's public
 *  face, on calbridge.de. */
export const SUPPORTED_LOCALES = ['de', 'en'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export const DEFAULT_LOCALE: Locale = 'de'

export function isLocale(value: string): value is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

/** detectPreferredLocale picks English when it is the visitor's highest
 *  weighted browser language, German otherwise. */
export function detectPreferredLocale(languages: readonly string[]): Locale {
  const primary = languages[0]?.split('-')[0]?.toLowerCase()
  return primary === 'en' ? 'en' : DEFAULT_LOCALE
}

/** localeStorageKey is where a chosen locale persists across visits, kept
 *  under its own key rather than the application's: the two are served
 *  from different hosts and a visitor's choice on one says nothing about
 *  the other. Nothing writes it yet; a later locale toggle is the first
 *  writer. */
const localeStorageKey = 'calbridge-landing-locale'

/** readStoredLocale reads a previously chosen locale back out of storage,
 *  or undefined when none was stored, the key held something other than
 *  a supported locale, or storage itself is unreachable (a private
 *  window, cleared site data, or storage blocked outright). */
export function readStoredLocale(
  storage: Pick<Storage, 'getItem'> = localStorage,
): Locale | undefined {
  try {
    const stored = storage.getItem(localeStorageKey)
    return stored && isLocale(stored) ? stored : undefined
  } catch {
    return undefined
  }
}

/** createAppI18n builds one vue-i18n instance. English falls back to
 *  German for any key not yet translated, rather than showing the raw
 *  key: fallbackLocale is what gives that for free, since German is
 *  authored as the complete set of messages. */
export function createAppI18n(locale: Locale = DEFAULT_LOCALE) {
  return createI18n({
    legacy: false,
    locale,
    fallbackLocale: DEFAULT_LOCALE,
    missingWarn: true,
    fallbackWarn: false,
    messages: { de, en },
  })
}
