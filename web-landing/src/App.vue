<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The one shell every route renders inside: a header carrying the brand,
// the site's own navigation and the call to action toward the
// application, and a footer carrying the imprint and privacy policy
// links every page is required to reach from. appUrl() is read once
// here, not per view, because the header's call to action is the one
// link every page shows regardless of which route is active.
import { computed } from 'vue'
import {
  GovueButton,
  GovueFooter,
  GovueHeader,
  GovueNav,
  GovueSkipLink,
  type FooterLink,
  type NavItem,
} from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { appUrl } from './appUrl'

const { t } = useI18n()
const route = useRoute()

const navItems = computed<NavItem[]>(() => [
  { href: '/', label: t('nav.home'), current: route.path === '/' },
  { href: '/pricing', label: t('nav.pricing'), current: route.path === '/pricing' },
])

const footerLinks = computed<FooterLink[]>(() => [
  { href: '/imprint', label: t('nav.imprint') },
  { href: '/privacy', label: t('nav.privacy') },
])

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

  <GovueHeader>
    <template #brand>
      <a href="/" class="hover:underline">{{ t('common.appName') }}</a>
    </template>

    <GovueNav :items="navItems" :aria-label="t('nav.ariaLabel')" />

    <template #actions>
      <GovueButton variant="primary" @click="openApplication">
        {{ t('nav.openApp') }}
      </GovueButton>
    </template>
  </GovueHeader>

  <RouterView />

  <GovueFooter :links="footerLinks" :note="t('footer.note')" :aria-label="t('footer.ariaLabel')" />
</template>
