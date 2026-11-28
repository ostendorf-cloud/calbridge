<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Elastic-2.0
-->

<script setup lang="ts">
// The imprint names whoever the deployment configures as its operator,
// read from the page the server serves (see operator.ts). This build
// carries no operator data of its own. An optional field, such as a
// register entry or a VAT id, shows only where the deployment set it, so
// a private operator has no empty heading to explain. The page has no
// dispute resolution section, by the owner's rule for every site.
import { computed } from 'vue'
import { GovueLegalPage } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'

import { addressLines, readOperator } from '../operator'

const { t } = useI18n()

interface Row {
  key: string
  label: string
  lines: { text: string; href?: string }[]
}

const operator = readOperator()

const rows = computed<Row[]>(() => {
  if (!operator) {
    return []
  }

  const contact: Row['lines'] = [
    {
      text: `${t('imprint.field.email')}: ${operator.email}`,
      href: `mailto:${operator.email}`,
    },
  ]
  if (operator.phone) {
    contact.push({ text: `${t('imprint.field.phone')}: ${operator.phone}` })
  }

  const list: Row[] = [
    { key: 'operator', label: t('imprint.field.operator'), lines: [{ text: operator.name }] },
    {
      key: 'address',
      label: t('imprint.field.address'),
      lines: addressLines(operator).map((text) => ({ text })),
    },
    { key: 'contact', label: t('imprint.field.contact'), lines: contact },
  ]

  const optional: [string, string | undefined, string][] = [
    ['representative', operator.representative, 'imprint.field.representative'],
    ['register', operator.register, 'imprint.field.register'],
    ['vat', operator.vatId, 'imprint.field.vat'],
    ['responsibleContent', operator.contentResponsible, 'imprint.field.responsibleContent'],
  ]
  for (const [key, value, label] of optional) {
    if (value) {
      list.push({ key, label: t(label), lines: [{ text: value }] })
    }
  }

  return list
})
</script>

<template>
  <GovueLegalPage
    page="imprint"
    :title="t('imprint.title')"
    back-href="/"
    :skip-link="false"
    :language-switch="false"
  >
    <p class="text-base text-text-muted">{{ t('imprint.lead') }}</p>

    <dl v-if="operator" class="flex flex-col gap-4">
      <div v-for="row in rows" :key="row.key">
        <dt class="text-sm font-medium text-text-muted">{{ row.label }}</dt>
        <dd v-for="line in row.lines" :key="line.text" class="text-base text-text">
          <a v-if="line.href" :href="line.href" class="text-accent-text hover:underline">
            {{ line.text }}
          </a>
          <template v-else>{{ line.text }}</template>
        </dd>
      </div>
    </dl>
    <p v-else class="text-base text-text">{{ t('operator.unavailable') }}</p>
  </GovueLegalPage>
</template>
