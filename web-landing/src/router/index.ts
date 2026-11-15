// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { createRouter, createWebHistory, type Router } from 'vue-router'

const routes = [
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
  {
    path: '/imprint',
    name: 'imprint',
    component: () => import('../views/ImprintView.vue'),
  },
  {
    path: '/privacy',
    name: 'privacy',
    component: () => import('../views/PrivacyView.vue'),
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: { name: 'home' },
  },
]

export function createAppRouter(): Router {
  return createRouter({
    history: createWebHistory(),
    routes,
  })
}
