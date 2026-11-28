<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// Every claim on this page is traceable to a specific package: see the
// locale files' own "privacy" keys for the exact wording and
// docs/concepts/calbridge.md / docs/adr for the two decisions a user can
// notice (overwriting a hand-edited copy, a revoked grant deleting the
// edge). The controller is whoever the deployment configures as its
// operator, read from the served page (see operator.ts). This build
// carries no operator data of its own, and none is invented here.
//
// A section that describes something not in operation yet says so in its
// own text, and notInOperation says it once for the whole policy. When
// the engine is wired, those sentences change with it, see
// docs/deliberate-gaps.md.
import { GovueLegalPage } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'

import { addressLines, readOperator } from '../operator'

const { t } = useI18n()

interface Section {
  id: string
  paragraphs: string[]
}

const operator = readOperator()

// The sections before the controller, then after it. The controller sits
// third because everything below it is the controller's own processing.
const opening: Section[] = [
  { id: 'intro', paragraphs: ['body'] },
  { id: 'notInOperation', paragraphs: ['body'] },
]

const processing: Section[] = [
  { id: 'account', paragraphs: ['body'] },
  { id: 'providerSignIn', paragraphs: ['body', 'linkBody'] },
  { id: 'calendarConnections', paragraphs: ['body', 'encryptionBody'] },
  { id: 'calendarAccess', paragraphs: ['body', 'tokensBody'] },
  { id: 'syncData', paragraphs: ['body', 'busyBlockBody'] },
  { id: 'handEdits', paragraphs: ['body', 'exceptionBody'] },
  { id: 'sharing', paragraphs: ['body', 'teardownBody'] },
  { id: 'deletion', paragraphs: ['body', 'limitBody'] },
  { id: 'retention', paragraphs: ['body'] },
  { id: 'revocation', paragraphs: ['body', 'providerBody'] },
  { id: 'googleLimitedUse', paragraphs: ['body'] },
  { id: 'noAnalytics', paragraphs: ['body'] },
  { id: 'session', paragraphs: ['body'] },
  { id: 'preferences', paragraphs: ['body', 'themeBody'] },
]
</script>

<template>
  <GovueLegalPage
    page="privacy"
    :title="t('privacy.title')"
    back-href="/"
    :skip-link="false"
    :language-switch="false"
  >
    <p class="text-lg text-text-muted">{{ t('privacy.lead') }}</p>

    <section v-for="section in opening" :key="section.id" class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t(`privacy.${section.id}.heading`) }}</h2>
      <p v-for="paragraph in section.paragraphs" :key="paragraph" class="text-base text-text-muted">
        {{ t(`privacy.${section.id}.${paragraph}`) }}
      </p>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t('privacy.operator.heading') }}</h2>
      <p class="text-base text-text-muted">{{ t('privacy.operator.body') }}</p>
      <address v-if="operator" class="flex flex-col text-base not-italic text-text">
        <span>{{ operator.name }}</span>
        <span v-for="line in addressLines(operator)" :key="line">{{ line }}</span>
        <a :href="`mailto:${operator.email}`" class="text-accent-text hover:underline">
          {{ operator.email }}
        </a>
      </address>
      <p v-else class="text-base text-text">{{ t('operator.unavailable') }}</p>
      <p v-if="operator?.dataProtection" class="text-base text-text-muted">
        {{ t('privacy.operator.dataProtection') }} {{ operator.dataProtection }}
      </p>
    </section>

    <section v-for="section in processing" :key="section.id" class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold text-text">{{ t(`privacy.${section.id}.heading`) }}</h2>
      <p v-for="paragraph in section.paragraphs" :key="paragraph" class="text-base text-text-muted">
        {{ t(`privacy.${section.id}.${paragraph}`) }}
      </p>
    </section>
  </GovueLegalPage>
</template>
