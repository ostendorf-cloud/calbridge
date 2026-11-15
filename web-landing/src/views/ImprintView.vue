<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The imprint is deployment-specific: this build carries a fixed,
// named placeholder for every field a real imprint needs (operator
// name, address, contact, representative, register entry, VAT id,
// content responsibility, dispute resolution), rather than inventing a
// company, an address or a contact that was never given. The owner of a
// real deployment fills these in before publishing the site; see
// AGENTS.md's note on where that happens.
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const fields = [
  'operator',
  'address',
  'representative',
  'register',
  'vat',
  'responsibleContent',
  'disputeResolution',
] as const
</script>

<template>
  <main id="main" class="mx-auto flex max-w-reading flex-col gap-6 px-4 py-10 sm:px-6">
    <div class="flex flex-col gap-2">
      <h1 class="text-2xl font-semibold text-text">{{ t('imprint.title') }}</h1>
      <p class="text-base text-text-muted">{{ t('imprint.lead') }}</p>
    </div>

    <dl class="flex flex-col gap-4">
      <div v-for="field in fields" :key="field">
        <dt class="text-sm font-medium text-text-muted">{{ t(`imprint.${field}.heading`) }}</dt>
        <dd class="text-base text-text">{{ t(`imprint.${field}.placeholder`) }}</dd>
      </div>

      <div>
        <dt class="text-sm font-medium text-text-muted">{{ t('imprint.contact.heading') }}</dt>
        <dd class="text-base text-text">{{ t('imprint.contact.emailPlaceholder') }}</dd>
        <dd class="text-base text-text">{{ t('imprint.contact.phonePlaceholder') }}</dd>
      </div>
    </dl>
  </main>
</template>
