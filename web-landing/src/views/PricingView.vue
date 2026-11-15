<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// Says today what pricing will later say with numbers: calbridge is
// privately operated and has no prices yet (acceptance criteria), with
// no checkout and no payment processing anywhere in this build (out of
// scope for issue #44).
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main id="main" class="mx-auto flex max-w-reading flex-col gap-4 px-4 py-10 sm:px-6">
    <h1 class="text-2xl font-semibold text-text">{{ t('pricing.title') }}</h1>
    <p class="text-lg text-text-muted">{{ t('pricing.lead') }}</p>
    <p class="text-base text-text-muted">{{ t('pricing.body') }}</p>
  </main>
</template>
