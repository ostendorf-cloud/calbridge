// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { localeFromPath, legalRoutes, type LanguageState } from '@ostendorf-cloud/govue'
import { createRouter, createWebHistory, type Router, type RouterHistory } from 'vue-router'

/** What the router needs of govue's language layer: the active language and
 *  a way to follow the address. */
type Language = Pick<LanguageState, 'locale' | 'setLocale'>

/** createAppRouter builds the router. The legal pages live under
 *  language prefixed paths, /de/imprint and /en/imprint and so on, from
 *  govue's own route list. The short paths /imprint, /privacy and /terms
 *  and the German aliases are redirected by the server before the client
 *  ever loads, so none of them is a route here.
 *
 *  Every path outside the list renders the not found view, and the server
 *  answers it with a 404 as well. The list is kept in Go too, as
 *  internal/landingapi's spaPages, and router/index.test.ts pins this
 *  side of it. strict and sensitive keep the client
 *  from rendering a page for a path the server called unknown, a trailing
 *  slash or an upper case prefix. */
export function createAppRouter(
  language: Language,
  history: RouterHistory = createWebHistory(),
): Router {
  const router = createRouter({
    history,
    strict: true,
    sensitive: true,
    routes: [
      {
        path: '/',
        name: 'home',
        component: () => import('../views/HomeView.vue'),
      },
      {
        path: '/pricing',
        name: 'pricing',
        component: () => import('../views/PricingView.vue'),
      },
      ...legalRoutes({
        imprint: () => import('../views/ImprintView.vue'),
        privacy: () => import('../views/PrivacyView.vue'),
        terms: () => import('../views/TermsView.vue'),
      }),
      {
        path: '/:pathMatch(.*)*',
        name: 'not-found',
        component: () => import('../views/NotFoundView.vue'),
      },
    ],
  })

  // A prefixed page is in the language its address names, whatever the
  // visitor chose before. The language follows the address without being
  // stored: arriving from a consent screen link must not set a cookie the
  // visitor never asked for. A deliberate choice with the switch stores it.
  router.beforeEach((to) => {
    const fromAddress = localeFromPath(to.path)
    if (fromAddress !== undefined && fromAddress !== language.locale.value) {
      language.setLocale(fromAddress, { persist: false })
    }
  })

  return router
}
