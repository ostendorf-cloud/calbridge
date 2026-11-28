// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { createApp } from 'vue'
import { useTheme } from '@ostendorf-cloud/govue'

import App from './App.vue'
import { createAppI18n, detectPreferredLocale, readStoredLocale } from './i18n'
import { createAppRouter } from './router'
import './styles.css'

// The theme never varies: this app ships exactly one brand (see
// src/theme/calbridge.css). Mode starts at "system", the visitor's own
// choice only overrides it once a later step adds a toggle, matching
// useTheme's own documented default.
useTheme({ theme: 'calbridge' })

const initialLocale =
  readStoredLocale() ?? detectPreferredLocale(navigator.languages ?? [navigator.language])

// The lang attribute index.html ships statically has to be set from the
// resolved locale before any content renders, or an English visitor gets
// the English strings inside a document still declared as German.
document.documentElement.lang = initialLocale

const i18n = createAppI18n(initialLocale)
const router = createAppRouter()

const app = createApp(App)
app.use(router)
app.use(i18n)
app.mount('#app')
