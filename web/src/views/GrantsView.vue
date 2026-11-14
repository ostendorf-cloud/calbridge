<script setup lang="ts">
// GrantsView is where a person grants another one access to a calendar they
// own, and sees both what they granted and what was granted to them.
//
// Revoking a grant has a consequence beyond the row it sits in: it tears
// down every connection that grant covers and deletes the events those
// connections wrote. GovueConfirmDialog states that in the confirmation
// itself, in the person's own language, rather than in a document nobody
// reads before clicking.
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  isApiError,
  GovueButton,
  GovueCard,
  GovueConfirmDialog,
  GovueEmptyState,
  GovueField,
  GovueInput,
  GovueNav,
  GovuePage,
  GovueSelect,
  useForm,
} from '@ostendorf-cloud/govue'
import type { FieldErrors } from '@ostendorf-cloud/govue'

import { apiClient, csrfToken } from '../api'

interface MeResponse {
  csrf_token: string
}

interface CalendarDto {
  id: string
  account_id: string | null
  name: string
  managed: boolean
  shared: boolean
  owner_email: string | null
  max_detail_level: string | null
}

interface DetailLevelDto {
  level: string
  fields: string[]
}

interface GrantDto {
  id: string
  calendar_id: string
  counterparty_email: string
  max_detail_level: string
}

interface GraphResponse {
  calendars: CalendarDto[]
  detail_levels: DetailLevelDto[]
  grants: { given: GrantDto[]; received: GrantDto[] }
}

const i18n = useI18n()
const { t } = i18n
const router = useRouter()

const navItems = computed(() => [
  { href: '/', label: t('nav.home') },
  { href: '/canvas', label: t('nav.canvas') },
  { href: '/grants', label: t('nav.grants'), current: true },
])

const loading = ref(true)
const loadError = ref('')

const calendars = ref<CalendarDto[]>([])
const detailLevels = ref<DetailLevelDto[]>([])
const given = ref<GrantDto[]>([])
const received = ref<GrantDto[]>([])

onMounted(load)

/** load obtains the session's CSRF token the same way HomeView does, from
 *  GET /api/v1/me, before the graph itself: GrantsView is reachable by a
 *  bookmark or a reload, never only by following HomeView's link, so it
 *  cannot rely on HomeView having filled the token already. */
async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const me = await apiClient.get<MeResponse>('/me')
    csrfToken.value = me.csrf_token
    const graph = await apiClient.get<GraphResponse>('/graph')
    calendars.value = graph.calendars
    detailLevels.value = graph.detail_levels
    given.value = graph.grants.given
    received.value = graph.grants.received
  } catch (error) {
    if (isApiError(error) && error.isUnauthenticated) {
      await router.push({ name: 'login' })
      return
    }
    loadError.value = t('grants.loadError')
  } finally {
    loading.value = false
  }
}

function calendarName(calendarId: string): string {
  return calendars.value.find((calendar) => calendar.id === calendarId)?.name ?? calendarId
}

function detailLevelLabel(level: string): string {
  const key = `canvas.detailLevel.${level}`
  return i18n.te(key) ? t(key) : level
}

const ownCalendars = computed(() =>
  calendars.value.filter((calendar) => calendar.account_id !== null),
)
const calendarOptions = computed(() =>
  ownCalendars.value.map((calendar) => ({ value: calendar.id, label: calendar.name })),
)
const detailLevelOptions = computed(() =>
  detailLevels.value.map((level) => ({ value: level.level, label: detailLevelLabel(level.level) })),
)

interface GrantFormFields {
  calendarId: string
  granteeEmail: string
  maxDetailLevel: string
}

const grantForm = useForm<GrantFormFields>({
  initial: { calendarId: '', granteeEmail: '', maxDetailLevel: '' },
  validate: (values) => {
    const errors: FieldErrors<GrantFormFields> = {}
    if (!values.calendarId) {
      errors.calendarId = t('grants.form.validation.calendar')
    }
    if (values.granteeEmail.trim() === '') {
      errors.granteeEmail = t('grants.form.validation.email')
    }
    if (!values.maxDetailLevel) {
      errors.maxDetailLevel = t('grants.form.validation.maxDetailLevel')
    }
    return errors
  },
  submit: async (values) => {
    const created = await apiClient.post<GrantDto>('/grants', {
      grantee_email: values.granteeEmail,
      calendar_id: values.calendarId,
      max_detail_level: values.maxDetailLevel,
    })
    given.value = [...given.value, created]
    grantForm.reset()
  },
})

/* ---- revocation: the same control and the same consequence sentence for
   a grant you gave and one you received ---- */
type GrantList = 'given' | 'received'

const revokeDialogOpen = ref(false)
const revokeError = ref('')
const revokeLoading = ref(false)
const revokeTarget = ref<{ id: string; list: GrantList } | undefined>(undefined)

const revokingGrant = computed(() => {
  if (!revokeTarget.value) {
    return undefined
  }
  const list = revokeTarget.value.list === 'given' ? given.value : received.value
  return list.find((grant) => grant.id === revokeTarget.value?.id)
})

watch(revokeDialogOpen, (open) => {
  if (!open) {
    revokeTarget.value = undefined
  }
})

function askRevoke(id: string, list: GrantList): void {
  revokeError.value = ''
  revokeTarget.value = { id, list }
  revokeDialogOpen.value = true
}

async function confirmRevoke(): Promise<void> {
  if (!revokeTarget.value) {
    return
  }
  revokeLoading.value = true
  revokeError.value = ''
  try {
    await apiClient.del(`/grants/${revokeTarget.value.id}`)
    if (revokeTarget.value.list === 'given') {
      given.value = given.value.filter((grant) => grant.id !== revokeTarget.value?.id)
    } else {
      received.value = received.value.filter((grant) => grant.id !== revokeTarget.value?.id)
    }
    revokeDialogOpen.value = false
  } catch (error) {
    revokeError.value = isApiError(error) ? error.message : t('grants.unexpectedError')
  } finally {
    revokeLoading.value = false
  }
}
</script>

<template>
  <GovuePage :title="t('grants.title')" :lead="t('grants.lead')">
    <GovueNav :items="navItems" :aria-label="t('nav.ariaLabel')" />

    <p v-if="loading">{{ t('grants.loading') }}</p>

    <p v-else-if="loadError" role="alert" class="text-danger-text">{{ loadError }}</p>

    <template v-else>
      <GovueCard :title="t('grants.form.title')">
        <form class="grid gap-4 sm:grid-cols-2" novalidate @submit.prevent="grantForm.handleSubmit">
          <GovueField
            :label="t('grants.form.calendar')"
            :error="grantForm.errorOf('calendarId')"
            required
          >
            <GovueSelect v-model="grantForm.values.calendarId" :options="calendarOptions" />
          </GovueField>

          <GovueField
            :label="t('grants.form.email')"
            :error="grantForm.errorOf('granteeEmail')"
            required
          >
            <GovueInput
              v-model="grantForm.values.granteeEmail"
              type="email"
              @blur="grantForm.blur('granteeEmail')"
            />
          </GovueField>

          <GovueField
            :label="t('grants.form.maxDetailLevel')"
            :error="grantForm.errorOf('maxDetailLevel')"
            required
          >
            <GovueSelect v-model="grantForm.values.maxDetailLevel" :options="detailLevelOptions" />
          </GovueField>

          <p
            v-if="grantForm.problem.value"
            role="alert"
            class="text-sm text-danger-text sm:col-span-2"
          >
            {{ grantForm.problem.value }}
          </p>

          <GovueButton
            type="submit"
            variant="primary"
            :loading="grantForm.submitting.value"
            class="sm:col-span-2"
          >
            {{ t('grants.form.submit') }}
          </GovueButton>
        </form>
      </GovueCard>

      <GovueCard :title="t('grants.given.title')">
        <GovueEmptyState
          v-if="given.length === 0"
          :title="t('grants.given.emptyTitle')"
          :description="t('grants.given.emptyDescription')"
        />
        <ul v-else class="flex flex-col gap-2">
          <li
            v-for="grant in given"
            :key="grant.id"
            class="flex flex-col gap-2 rounded-md border border-border p-3 sm:flex-row sm:items-center sm:justify-between"
          >
            <div>
              <p class="text-sm font-medium text-text">
                {{ calendarName(grant.calendar_id) }} → {{ grant.counterparty_email }}
              </p>
              <p class="text-sm text-text-muted">{{ detailLevelLabel(grant.max_detail_level) }}</p>
            </div>
            <GovueButton type="button" variant="danger" @click="askRevoke(grant.id, 'given')">
              {{ t('grants.revoke') }}
            </GovueButton>
          </li>
        </ul>
      </GovueCard>

      <GovueCard :title="t('grants.received.title')">
        <GovueEmptyState
          v-if="received.length === 0"
          :title="t('grants.received.emptyTitle')"
          :description="t('grants.received.emptyDescription')"
        />
        <ul v-else class="flex flex-col gap-2">
          <li
            v-for="grant in received"
            :key="grant.id"
            class="flex flex-col gap-2 rounded-md border border-border p-3 sm:flex-row sm:items-center sm:justify-between"
          >
            <div>
              <p class="text-sm font-medium text-text">
                {{ calendarName(grant.calendar_id) }} ({{ grant.counterparty_email }})
              </p>
              <p class="text-sm text-text-muted">{{ detailLevelLabel(grant.max_detail_level) }}</p>
            </div>
            <GovueButton type="button" variant="danger" @click="askRevoke(grant.id, 'received')">
              {{ t('grants.revoke') }}
            </GovueButton>
          </li>
        </ul>
      </GovueCard>

      <p v-if="revokeError" role="alert" class="text-sm text-danger-text">{{ revokeError }}</p>
    </template>

    <GovueConfirmDialog
      v-model:open="revokeDialogOpen"
      :title="t('grants.revokeDialog.title')"
      :message="t('grants.revokeDialog.message')"
      :subject="revokingGrant?.counterparty_email ?? ''"
      :confirm-label="t('grants.revokeDialog.confirm')"
      :cancel-label="t('grants.revokeDialog.cancel')"
      :loading="revokeLoading"
      @confirm="confirmRevoke"
    />
  </GovuePage>
</template>
