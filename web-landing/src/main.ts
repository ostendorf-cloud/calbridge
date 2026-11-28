// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { createApp } from 'vue'
import { useTheme } from '@ostendorf-cloud/govue'

import App from './App.vue'
import { createAppI18n } from './i18n'
import { createAppRouter } from './router'
import './styles.css'

// The theme never varies: this build ships exactly one brand, the same
// one web/src/main.ts selects for the application. Mode starts at
// "system", matching useTheme's own documented default. The visitor's
// light or dark choice comes from the theme switch in the header.
useTheme({ theme: 'calbridge' })

// govue's language layer resolves the language and sets the lang attribute
// of the document before anything renders, so an English visitor never
// gets English strings inside a document still declared as German.
const { i18n, govue } = createAppI18n()
const router = createAppRouter(govue.language)

const app = createApp(App)
app.use(router)
app.use(i18n)
app.use(govue)

// Mount after the first navigation: a prefixed address sets its own
// language in the router guard, and the first render has to be in it.
void router.isReady().then(() => {
  app.mount('#app')
})
