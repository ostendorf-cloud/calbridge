// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { createRouter, createWebHistory, type Router } from 'vue-router'

// HomeView lives at "/": its own mount check (GET /api/v1/me) is the
// "simple check" that sends an unauthenticated visitor back to LoginView,
// which is also why there is no separate navigation guard here that would
// only duplicate that same call before the view ever got to make it.
const routes = [
  {
    path: '/',
    name: 'home',
    component: () => import('../views/HomeView.vue'),
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('../views/LoginView.vue'),
  },
  {
    path: '/canvas',
    name: 'canvas',
    component: () => import('../views/CanvasView.vue'),
  },
  {
    path: '/grants',
    name: 'grants',
    component: () => import('../views/GrantsView.vue'),
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
