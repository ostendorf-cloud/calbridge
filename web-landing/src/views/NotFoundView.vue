<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// What an address outside the site's own pages shows. The server answers
// the same shell with a 404 status, so a crawler and a link checker see
// the failure and the visitor sees this page, not a copy of the start page.
import { GovuePage } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <GovuePage :title="t('notFound.title')" :lead="t('notFound.body')">
    <p>
      <a href="/" class="text-accent-text hover:underline">{{ t('notFound.home') }}</a>
    </p>
  </GovuePage>
</template>
