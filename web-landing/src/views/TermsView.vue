<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The terms of use of this deployment. calbridge has no price today, so
// the text covers use, availability and liability and carries no payment
// term. It has no dispute resolution section and no clause on a court
// or a governing law: the owner's rule for every site keeps those out.
// The operator is whoever the imprint names, so this view reads no
// operator data of its own.
import { GovueLegalPage } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const sections = [
  'intro',
  'scope',
  'account',
  'use',
  'availability',
  'liability',
  'termination',
  'changes',
  'contact',
]
</script>

<template>
  <GovueLegalPage
    page="terms"
    :title="t('terms.title')"
    back-href="/"
    :skip-link="false"
    :language-switch="false"
  >
    <p class="text-lg text-text-muted">{{ t('terms.lead') }}</p>

    <section v-for="section in sections" :key="section" class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t(`terms.${section}.heading`) }}</h2>
      <p class="text-base text-text-muted">{{ t(`terms.${section}.body`) }}</p>
    </section>
  </GovueLegalPage>
</template>
