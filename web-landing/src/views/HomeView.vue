<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The landing page itself: the hero states the problem, the three
// detail levels are explained as the thing calbridge actually does, and
// the closing call to action points at the configured application
// host, never a literal one. See appUrl.ts for where that host comes
// from.
import { GovueButton, GovueCard } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'

import { appUrl } from '../appUrl'

const { t } = useI18n()

function openApplication(): void {
  const target = appUrl()
  if (target) {
    window.location.assign(target)
  }
}
</script>

<template>
  <!-- The skip link's destination. GovuePage is not used here: the hero's
       centered, full-width sections do not fit its single reading or
       shell column, but it still needs the one #main landmark every
       other view gets from that component. -->
  <main id="main" class="flex flex-col gap-10 px-4 py-10 sm:px-6">
    <section class="mx-auto flex max-w-reading flex-col gap-4 text-center">
      <h1 class="text-3xl font-semibold text-text sm:text-4xl">{{ t('home.hero.title') }}</h1>
      <p class="text-lg text-text-muted">{{ t('home.hero.lead') }}</p>
      <div class="flex justify-center">
        <GovueButton variant="primary" @click="openApplication">
          {{ t('home.hero.cta') }}
        </GovueButton>
      </div>
    </section>

    <section class="mx-auto flex max-w-reading flex-col gap-2">
      <h2 class="text-xl font-semibold text-text">{{ t('home.problem.title') }}</h2>
      <p class="text-base text-text-muted">{{ t('home.problem.body') }}</p>
    </section>

    <section class="mx-auto flex w-full max-w-shell flex-col gap-4">
      <div class="flex flex-col gap-2 text-center">
        <h2 class="text-xl font-semibold text-text">{{ t('home.detailLevels.title') }}</h2>
        <p class="text-base text-text-muted">{{ t('home.detailLevels.lead') }}</p>
      </div>

      <div class="grid gap-4 sm:grid-cols-3">
        <GovueCard :title="t('home.detailLevels.full.title')">
          <p class="text-sm text-text-muted">{{ t('home.detailLevels.full.body') }}</p>
        </GovueCard>
        <GovueCard :title="t('home.detailLevels.titleTime.title')">
          <p class="text-sm text-text-muted">{{ t('home.detailLevels.titleTime.body') }}</p>
        </GovueCard>
        <GovueCard :title="t('home.detailLevels.busyBlock.title')">
          <p class="text-sm text-text-muted">{{ t('home.detailLevels.busyBlock.body') }}</p>
        </GovueCard>
      </div>
    </section>

    <section class="mx-auto flex max-w-reading flex-col items-center gap-3 text-center">
      <GovueCard :title="t('home.cta.title')">
        <p class="text-base text-text-muted">{{ t('home.cta.body') }}</p>
        <template #footer>
          <GovueButton variant="primary" @click="openApplication">
            {{ t('home.cta.button') }}
          </GovueButton>
        </template>
      </GovueCard>
    </section>
  </main>
</template>
