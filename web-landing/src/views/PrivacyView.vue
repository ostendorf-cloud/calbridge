<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// Every claim on this page is traceable to a specific package: see the
// locale files' own "privacy" keys for the exact wording and
// docs/concepts/calbridge.md / docs/adr for the two decisions a user can
// notice (overwriting a hand-edited copy, a revoked grant deleting the
// edge). operatorPlaceholder fields stay named placeholders: this
// deployment carries no real postal address, phone number or email
// address of its own, and none is invented here.
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main id="main" class="mx-auto flex max-w-reading flex-col gap-8 px-4 py-10 sm:px-6">
    <div class="flex flex-col gap-2">
      <h1 class="text-2xl font-semibold text-text">{{ t('privacy.title') }}</h1>
      <p class="text-lg text-text-muted">{{ t('privacy.lead') }}</p>
    </div>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.intro.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.intro.body') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.notInOperation.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.notInOperation.body') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.operator.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.operator.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.operator.contactPlaceholder') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.operator.dpoPlaceholder') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.account.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.account.body') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.providerSignIn.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.providerSignIn.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.providerSignIn.linkBody') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">
        {{ t('privacy.calendarConnections.heading') }}
      </h2>
      <p class="text-base text-text-muted">{{ t('privacy.calendarConnections.body') }}</p>
      <p class="text-base text-text-muted">
        {{ t('privacy.calendarConnections.encryptionBody') }}
      </p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.syncData.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.syncData.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.syncData.busyBlockBody') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.handEdits.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.handEdits.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.handEdits.exceptionBody') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.sharing.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.sharing.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.sharing.teardownBody') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.deletion.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.deletion.body') }}</p>
      <p class="text-base text-text-muted">{{ t('privacy.deletion.limitBody') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.noAnalytics.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.noAnalytics.body') }}</p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.session.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.session.body') }}</p>
    </section>
  </main>
</template>
