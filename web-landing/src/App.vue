<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The one shell every route renders inside: a header carrying the brand,
// the site's own navigation, the language and theme switches and the call
// to action toward the application, and a footer carrying the legal page
// links every page is required to reach from. appUrl() is read once here,
// not per view, because the header's call to action is the one link every
// page shows regardless of which route is active.
import { computed } from 'vue'
import {
  GovueButton,
  GovueFooter,
  GovueHeader,
  GovueLanguageSwitch,
  GovueNav,
  GovueSkipLink,
  isLocaleCode,
  LEGAL_PAGES,
  legalPath,
  type LegalPage,
  type NavItem,
} from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { appUrl } from './appUrl'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const navItems = computed<NavItem[]>(() => [
  { href: '/', label: t('nav.home'), current: route.path === '/' },
  { href: '/pricing', label: t('nav.pricing'), current: route.path === '/pricing' },
])

// The language switch moves a legal page to the same page in the other
// language. The target is built from the page list and the language list,
// never from the address bar. The other pages carry no language in their
// path, so the switch alone changes their language.
function onLanguageChange(code: string): void {
  const page = route.meta.legalPage
  if (isLocaleCode(code) && typeof page === 'string' && LEGAL_PAGES.includes(page as LegalPage)) {
    void router.push(legalPath(page as LegalPage, code))
  }
}

// There is no fetch here and nothing to await: openApplication only ever
// navigates the browser away, the same shape LoginView's provider sign-in
// uses in the application itself.
function openApplication(): void {
  const target = appUrl()
  if (target) {
    window.location.assign(target)
  }
}
</script>

<template>
  <GovueSkipLink>{{ t('common.skipToContent') }}</GovueSkipLink>

  <GovueHeader theme-switch>
    <template #brand>
      <a href="/" class="hover:underline">{{ t('common.appName') }}</a>
    </template>

    <GovueNav :items="navItems" :aria-label="t('nav.ariaLabel')" />

    <template #actions>
      <GovueLanguageSwitch @change="onLanguageChange" />
      <GovueButton variant="primary" @click="openApplication">
        {{ t('nav.openApp') }}
      </GovueButton>
    </template>
  </GovueHeader>

  <RouterView />

  <!-- The footer links every legal page in the active language. govue builds
       the links and their labels from its fixed page list. -->
  <GovueFooter :legal="LEGAL_PAGES" :note="t('footer.note')" :aria-label="t('footer.ariaLabel')" />
</template>
