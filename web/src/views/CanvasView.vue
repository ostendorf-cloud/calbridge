<script setup lang="ts">
// CanvasView renders the sync topology: calendars as Vue Flow nodes grouped
// by account, with calendars shared with the user in their own group, and
// edges between them that each carry a detail level.
//
// Every action the canvas offers is also reachable without a pointer drag.
// The accessible form below the canvas (two selects for source and target,
// a detail level select, a submit button) is the one path that creates an
// edge; a calendar node's "connect from here" control does not open a
// second way to do that, it only fills the form's source field and
// announces the change, so dragging a handle and using the keyboard end up
// calling the same code.
import '@vue-flow/core/dist/style.css'
import '../theme/vue-flow.css'

import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  Handle,
  Position,
  VueFlow,
  type Connection,
  type Edge as FlowEdge,
  type EdgeMouseEvent,
  type Node as FlowNode,
} from '@vue-flow/core'
import {
  isApiError,
  GovueBadge,
  GovueButton,
  GovueCard,
  GovueDialog,
  GovueEmptyState,
  GovueField,
  GovueInput,
  GovuePage,
  GovueSelect,
  useForm,
} from '@ostendorf-cloud/govue'
import type { FieldErrors } from '@ostendorf-cloud/govue'

import { apiClient } from '../api'

/* ---- the GET /graph shape, the parts this view needs ---- */
interface AccountDto {
  id: string
  provider: string
  display_name: string
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

interface EdgeDto {
  id: string
  source_calendar_id: string
  target_calendar_id: string
  detail_level: string
}

interface DetailLevelDto {
  level: string
  fields: string[]
}

interface GraphResponse {
  accounts: AccountDto[]
  calendars: CalendarDto[]
  edges: EdgeDto[]
  detail_levels: DetailLevelDto[]
}

interface CreatedEdgeResponse extends EdgeDto {
  created_calendar?: CalendarDto
}

const i18n = useI18n()
const { t } = i18n
const router = useRouter()

const loading = ref(true)
const loadError = ref('')
const canvasError = ref('')

const accounts = ref<AccountDto[]>([])
const calendars = ref<CalendarDto[]>([])
const edges = ref<EdgeDto[]>([])
const detailLevels = ref<DetailLevelDto[]>([])

onMounted(load)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const graph = await apiClient.get<GraphResponse>('/graph')
    accounts.value = graph.accounts
    calendars.value = graph.calendars
    edges.value = graph.edges
    detailLevels.value = graph.detail_levels
  } catch (error) {
    if (isApiError(error) && error.isUnauthenticated) {
      await router.push({ name: 'login' })
      return
    }
    loadError.value = t('canvas.loadError')
  } finally {
    loading.value = false
  }
}

function calendarById(id: string): CalendarDto | undefined {
  return calendars.value.find((calendar) => calendar.id === id)
}

function detailLevelLabel(level: string): string {
  const key = `canvas.detailLevel.${level}`
  return i18n.te(key) ? t(key) : level
}

function fieldLabel(field: string): string {
  const key = `canvas.field.${field}`
  return i18n.te(key) ? t(key) : field
}

/** allowedLevels is the detail_levels slice a connection between these two
 *  calendars may pick from: detail_levels is sent widest first, and a
 *  shared calendar's own max_detail_level cuts off everything wider than
 *  the grant that shared it allows. */
function allowedLevels(source?: CalendarDto, target?: CalendarDto): DetailLevelDto[] {
  const levels = detailLevels.value
  const caps = [source?.max_detail_level, target?.max_detail_level].filter((cap): cap is string =>
    Boolean(cap),
  )
  const capIndexes = caps
    .map((cap) => levels.findIndex((level) => level.level === cap))
    .filter((index) => index >= 0)
  if (capIndexes.length === 0) {
    return levels
  }
  return levels.slice(Math.max(...capIndexes))
}

function applyCreatedEdge(created: CreatedEdgeResponse): void {
  edges.value = [
    ...edges.value,
    {
      id: created.id,
      source_calendar_id: created.source_calendar_id,
      target_calendar_id: created.target_calendar_id,
      detail_level: created.detail_level,
    },
  ]
  if (created.created_calendar) {
    calendars.value = [...calendars.value, created.created_calendar]
  }
}

/* ---- the Vue Flow graph: an account group per account, calendars inside
   it, plus one group for the calendars shared with the user ---- */
const GROUP_WIDTH = 260
const GROUP_GAP_X = 300
const GROUP_HEADER = 44
const GROUP_PADDING = 16
const NODE_WIDTH = 220
const NODE_HEIGHT = 72
const NODE_GAP = 12
const SHARED_GROUP_ID = 'shared-with-you'

function groupNodeId(accountId: string): string {
  return `account:${accountId}`
}

function calendarNodeId(calendarId: string): string {
  return `calendar:${calendarId}`
}

function calendarIdFromNodeId(nodeId: string): string {
  return nodeId.replace('calendar:', '')
}

/** groupLabel reads a group node's heading from its id rather than from a
 *  typed-as-any slot payload, so the template needs no unsafe cast. */
function groupLabel(nodeId: string): string {
  if (nodeId === SHARED_GROUP_ID) {
    return t('canvas.sharedGroup.title')
  }
  const accountId = nodeId.replace('account:', '')
  return accounts.value.find((account) => account.id === accountId)?.display_name ?? ''
}

/** calendarForNode returns a node's calendar as a single-element array, so
 *  the template can bind it with v-for instead of a non-null assertion on
 *  a slot payload typed as any. */
function calendarForNode(nodeId: string): CalendarDto[] {
  const calendar = calendarById(calendarIdFromNodeId(nodeId))
  return calendar ? [calendar] : []
}

function groupHeight(childCount: number): number {
  return (
    GROUP_HEADER +
    GROUP_PADDING * 2 +
    childCount * NODE_HEIGHT +
    Math.max(childCount - 1, 0) * NODE_GAP
  )
}

const sharedCalendars = computed(() => calendars.value.filter((calendar) => calendar.shared))

const flowNodes = computed<FlowNode[]>(() => {
  const nodes: FlowNode[] = []

  accounts.value.forEach((account, groupIndex) => {
    const owned = calendars.value.filter((calendar) => calendar.account_id === account.id)
    const groupId = groupNodeId(account.id)
    nodes.push({
      id: groupId,
      type: 'group',
      position: { x: groupIndex * (GROUP_WIDTH + GROUP_GAP_X), y: 0 },
      style: { width: `${GROUP_WIDTH}px`, height: `${groupHeight(owned.length)}px` },
      draggable: false,
      selectable: false,
      focusable: false,
    })
    owned.forEach((calendar, index) => {
      nodes.push({
        id: calendarNodeId(calendar.id),
        type: 'calendar',
        parentNode: groupId,
        extent: 'parent',
        position: {
          x: GROUP_PADDING,
          y: GROUP_HEADER + GROUP_PADDING + index * (NODE_HEIGHT + NODE_GAP),
        },
        style: { width: `${NODE_WIDTH}px`, height: `${NODE_HEIGHT}px` },
        draggable: false,
      })
    })
  })

  if (sharedCalendars.value.length > 0) {
    nodes.push({
      id: SHARED_GROUP_ID,
      type: 'group',
      position: { x: accounts.value.length * (GROUP_WIDTH + GROUP_GAP_X), y: 0 },
      style: {
        width: `${GROUP_WIDTH}px`,
        height: `${groupHeight(sharedCalendars.value.length)}px`,
      },
      draggable: false,
      selectable: false,
      focusable: false,
    })
    sharedCalendars.value.forEach((calendar, index) => {
      nodes.push({
        id: calendarNodeId(calendar.id),
        type: 'calendar',
        parentNode: SHARED_GROUP_ID,
        extent: 'parent',
        position: {
          x: GROUP_PADDING,
          y: GROUP_HEADER + GROUP_PADDING + index * (NODE_HEIGHT + NODE_GAP),
        },
        style: { width: `${NODE_WIDTH}px`, height: `${NODE_HEIGHT}px` },
        draggable: false,
      })
    })
  }

  return nodes
})

const flowEdges = computed<FlowEdge[]>(() =>
  edges.value.map((edge) => ({
    id: edge.id,
    source: calendarNodeId(edge.source_calendar_id),
    target: calendarNodeId(edge.target_calendar_id),
    type: 'smoothstep',
    label: detailLevelLabel(edge.detail_level),
  })),
)

/* ---- the accessible path: a node's "connect from here" control only
   fills this form, it never creates an edge on its own ---- */
const connectAnnouncement = ref('')

function connectFrom(calendar: CalendarDto): void {
  connectForm.values.sourceId = calendar.id
  connectAnnouncement.value = t('canvas.connectAnnouncement', { name: calendar.name })
}

interface ConnectFormFields {
  sourceId: string
  targetMode: 'existing' | 'new'
  targetId: string
  newTargetAccountId: string
  newTargetName: string
  detailLevel: string
}

const connectForm = useForm<ConnectFormFields>({
  initial: {
    sourceId: '',
    targetMode: 'existing',
    targetId: '',
    newTargetAccountId: '',
    newTargetName: '',
    detailLevel: '',
  },
  validate: (values) => {
    const errors: FieldErrors<ConnectFormFields> = {}
    if (!values.sourceId) {
      errors.sourceId = t('canvas.form.validation.source')
    }
    if (values.targetMode === 'existing') {
      if (!values.targetId) {
        errors.targetId = t('canvas.form.validation.target')
      }
    } else {
      if (!values.newTargetAccountId) {
        errors.newTargetAccountId = t('canvas.form.validation.newTargetAccount')
      }
      if (values.newTargetName.trim() === '') {
        errors.newTargetName = t('canvas.form.validation.newTargetName')
      }
    }
    if (!values.detailLevel) {
      errors.detailLevel = t('canvas.form.validation.detailLevel')
    }
    return errors
  },
  submit: async (values) => {
    const body: Record<string, unknown> = {
      source_calendar_id: values.sourceId,
      detail_level: values.detailLevel,
    }
    if (values.targetMode === 'existing') {
      body.target_calendar_id = values.targetId
    } else {
      body.target = { account_id: values.newTargetAccountId, name: values.newTargetName }
    }
    const created = await apiClient.post<CreatedEdgeResponse>('/edges', body)
    applyCreatedEdge(created)
    connectAnnouncement.value = ''
    connectForm.reset()
  },
})

function calendarOptionLabel(calendar: CalendarDto): string {
  const account = accounts.value.find((candidate) => candidate.id === calendar.account_id)
  const group = account?.display_name ?? t('canvas.sharedGroup.title')
  return `${calendar.name} (${group})`
}

const sourceOptions = computed(() =>
  calendars.value.map((calendar) => ({ value: calendar.id, label: calendarOptionLabel(calendar) })),
)

const ownCalendars = computed(() =>
  calendars.value.filter((calendar) => calendar.account_id !== null),
)

const targetCalendarOptions = computed(() =>
  ownCalendars.value
    .filter((calendar) => calendar.id !== connectForm.values.sourceId)
    .map((calendar) => ({ value: calendar.id, label: calendarOptionLabel(calendar) })),
)

const accountOptions = computed(() =>
  accounts.value.map((account) => ({ value: account.id, label: account.display_name })),
)

const targetModeOptions = computed(() => [
  { value: 'existing', label: t('canvas.form.targetMode.existing') },
  { value: 'new', label: t('canvas.form.targetMode.new') },
])

const formAllowedLevels = computed(() => {
  const source = calendarById(connectForm.values.sourceId)
  const target =
    connectForm.values.targetMode === 'existing'
      ? calendarById(connectForm.values.targetId)
      : undefined
  return allowedLevels(source, target)
})

const formDetailLevelOptions = computed(() =>
  formAllowedLevels.value.map((level) => ({
    value: level.level,
    label: detailLevelLabel(level.level),
  })),
)

const formIsCapped = computed(() => formAllowedLevels.value.length < detailLevels.value.length)

watch(
  formDetailLevelOptions,
  (options) => {
    if (!options.some((option) => option.value === connectForm.values.detailLevel)) {
      connectForm.values.detailLevel = options[0]?.value ?? ''
    }
  },
  { immediate: true },
)

/* ---- creating an edge by dragging a handle onto another node: the same
   request the accessible form sends, with the narrowest allowed detail
   level as a safe default, so there is always something to adjust
   afterwards rather than a choice the drag itself cannot make ---- */
async function onConnect(connection: Connection): Promise<void> {
  canvasError.value = ''
  const sourceId = calendarIdFromNodeId(connection.source)
  const targetId = calendarIdFromNodeId(connection.target)
  const source = calendarById(sourceId)
  const target = calendarById(targetId)
  if (!source || !target) {
    return
  }
  if (target.account_id === null) {
    canvasError.value = t('canvas.form.validation.targetNotOwned')
    return
  }
  const allowed = allowedLevels(source, target)
  const detailLevel = allowed.at(-1)?.level
  if (!detailLevel) {
    return
  }
  try {
    const created = await apiClient.post<CreatedEdgeResponse>('/edges', {
      source_calendar_id: sourceId,
      target_calendar_id: targetId,
      detail_level: detailLevel,
    })
    applyCreatedEdge(created)
    openEditor(created.id)
  } catch (error) {
    canvasError.value = isApiError(error) ? error.message : t('canvas.unexpectedError')
  }
}

/* ---- editing or deleting an existing edge: reachable from the edge list
   below the canvas and, as a shortcut, from clicking the edge itself ---- */
const editorOpen = ref(false)
const editingEdgeId = ref('')
const editorDetailLevel = ref('')
const editorError = ref('')
const editorSaving = ref(false)

const editingEdge = computed(() => edges.value.find((edge) => edge.id === editingEdgeId.value))
const editingSource = computed(() =>
  editingEdge.value ? calendarById(editingEdge.value.source_calendar_id) : undefined,
)
const editingTarget = computed(() =>
  editingEdge.value ? calendarById(editingEdge.value.target_calendar_id) : undefined,
)
const editorAllowedLevels = computed(() => allowedLevels(editingSource.value, editingTarget.value))
const editorDetailLevelOptions = computed(() =>
  editorAllowedLevels.value.map((level) => ({
    value: level.level,
    label: detailLevelLabel(level.level),
  })),
)
const editorFields = computed(
  () => detailLevels.value.find((level) => level.level === editorDetailLevel.value)?.fields ?? [],
)
const editorIsCapped = computed(() => editorAllowedLevels.value.length < detailLevels.value.length)

function openEditor(edgeId: string): void {
  const edge = edges.value.find((candidate) => candidate.id === edgeId)
  editingEdgeId.value = edgeId
  editorDetailLevel.value = edge?.detail_level ?? ''
  editorError.value = ''
  editorOpen.value = true
}

function onEdgeClick({ edge }: EdgeMouseEvent): void {
  openEditor(edge.id)
}

async function saveEditor(): Promise<void> {
  if (!editingEdge.value) {
    return
  }
  editorSaving.value = true
  editorError.value = ''
  try {
    const updated = await apiClient.patch<EdgeDto>(`/edges/${editingEdgeId.value}`, {
      detail_level: editorDetailLevel.value,
    })
    edges.value = edges.value.map((edge) => (edge.id === updated.id ? updated : edge))
    editorOpen.value = false
  } catch (error) {
    editorError.value = isApiError(error) ? error.message : t('canvas.unexpectedError')
  } finally {
    editorSaving.value = false
  }
}

async function deleteEdge(edgeId: string): Promise<void> {
  canvasError.value = ''
  try {
    await apiClient.del(`/edges/${edgeId}`)
    edges.value = edges.value.filter((edge) => edge.id !== edgeId)
  } catch (error) {
    canvasError.value = isApiError(error) ? error.message : t('canvas.unexpectedError')
  }
}
</script>

<template>
  <GovuePage :title="t('canvas.title')" :lead="t('canvas.lead')" width="shell">
    <p v-if="loading">{{ t('canvas.loading') }}</p>

    <p v-else-if="loadError" role="alert" class="text-danger-text">{{ loadError }}</p>

    <template v-else>
      <p v-if="canvasError" role="alert" class="text-sm text-danger-text">{{ canvasError }}</p>

      <div
        class="h-[420px] w-full overflow-hidden rounded-lg border border-border bg-bg-subtle sm:h-[520px]"
      >
        <VueFlow
          :nodes="flowNodes"
          :edges="flowEdges"
          @connect="onConnect"
          @edge-click="onEdgeClick"
        >
          <template #node-group="{ id }">
            <div
              class="flex h-full w-full flex-col rounded-lg border border-dashed border-border-strong bg-bg-subtle/70 p-3"
            >
              <p class="text-sm font-semibold text-text">{{ groupLabel(id) }}</p>
            </div>
          </template>

          <template #node-calendar="{ id }">
            <template v-for="calendar in calendarForNode(id)" :key="calendar.id">
              <div
                tabindex="0"
                role="group"
                :aria-label="calendar.name"
                class="nopan nodrag flex h-full w-full flex-col gap-1.5 rounded-md border border-border-strong bg-surface p-2.5 text-left shadow-card"
              >
                <Handle type="target" :position="Position.Top" />
                <p class="truncate text-sm font-medium text-text">{{ calendar.name }}</p>
                <div class="flex flex-wrap gap-1">
                  <GovueBadge v-if="calendar.managed" kind="info">
                    {{ t('canvas.badge.managed') }}
                  </GovueBadge>
                  <GovueBadge v-if="calendar.shared" kind="neutral">
                    {{ t('canvas.badge.sharedBy', { email: calendar.owner_email }) }}
                  </GovueBadge>
                </div>
                <p
                  v-if="calendar.shared && calendar.max_detail_level"
                  class="text-xs text-text-muted"
                >
                  {{
                    t('canvas.badge.maxDetail', {
                      level: detailLevelLabel(calendar.max_detail_level),
                    })
                  }}
                </p>
                <GovueButton
                  type="button"
                  variant="quiet"
                  :aria-label="t('canvas.connectFromHereFor', { name: calendar.name })"
                  @click="connectFrom(calendar)"
                >
                  {{ t('canvas.connectFromHere') }}
                </GovueButton>
                <Handle type="source" :position="Position.Bottom" />
              </div>
            </template>
          </template>
        </VueFlow>
      </div>

      <p aria-live="polite" class="sr-only">{{ connectAnnouncement }}</p>

      <GovueCard :title="t('canvas.form.title')">
        <form
          class="grid gap-4 sm:grid-cols-2"
          novalidate
          @submit.prevent="connectForm.handleSubmit"
        >
          <GovueField
            :label="t('canvas.form.source')"
            :error="connectForm.errorOf('sourceId')"
            required
          >
            <GovueSelect
              v-model="connectForm.values.sourceId"
              :options="sourceOptions"
              :placeholder="t('canvas.form.selectPlaceholder')"
            />
          </GovueField>

          <GovueField :label="t('canvas.form.target')">
            <GovueSelect v-model="connectForm.values.targetMode" :options="targetModeOptions" />
          </GovueField>

          <GovueField
            v-if="connectForm.values.targetMode === 'existing'"
            :label="t('canvas.form.targetCalendar')"
            :error="connectForm.errorOf('targetId')"
            required
          >
            <GovueSelect
              v-model="connectForm.values.targetId"
              :options="targetCalendarOptions"
              :placeholder="t('canvas.form.selectPlaceholder')"
            />
          </GovueField>

          <template v-else>
            <GovueField
              :label="t('canvas.form.newTargetAccount')"
              :error="connectForm.errorOf('newTargetAccountId')"
              required
            >
              <GovueSelect
                v-model="connectForm.values.newTargetAccountId"
                :options="accountOptions"
                :placeholder="t('canvas.form.selectPlaceholder')"
              />
            </GovueField>
            <GovueField
              :label="t('canvas.form.newTargetName')"
              :error="connectForm.errorOf('newTargetName')"
              required
            >
              <GovueInput
                v-model="connectForm.values.newTargetName"
                @blur="connectForm.blur('newTargetName')"
              />
            </GovueField>
          </template>

          <GovueField
            :label="t('canvas.form.detailLevel')"
            :error="connectForm.errorOf('detailLevel')"
            required
          >
            <GovueSelect
              v-model="connectForm.values.detailLevel"
              :options="formDetailLevelOptions"
            />
          </GovueField>

          <p v-if="formIsCapped" class="text-sm text-text-muted sm:col-span-2">
            {{ t('canvas.form.cappedHint') }}
          </p>

          <p
            v-if="connectForm.problem.value"
            role="alert"
            class="text-sm text-danger-text sm:col-span-2"
          >
            {{ connectForm.problem.value }}
          </p>

          <GovueButton
            type="submit"
            variant="primary"
            :loading="connectForm.submitting.value"
            class="sm:col-span-2"
          >
            {{ t('canvas.form.submit') }}
          </GovueButton>
        </form>
      </GovueCard>

      <GovueCard :title="t('canvas.edges.title')">
        <GovueEmptyState
          v-if="edges.length === 0"
          :title="t('canvas.edges.emptyTitle')"
          :description="t('canvas.edges.emptyDescription')"
        />
        <ul v-else class="flex flex-col gap-2">
          <li
            v-for="edge in edges"
            :key="edge.id"
            class="flex flex-col gap-2 rounded-md border border-border p-3 sm:flex-row sm:items-center sm:justify-between"
          >
            <div>
              <p class="text-sm font-medium text-text">
                {{ calendarById(edge.source_calendar_id)?.name }} →
                {{ calendarById(edge.target_calendar_id)?.name }}
              </p>
              <p class="text-sm text-text-muted">{{ detailLevelLabel(edge.detail_level) }}</p>
            </div>
            <div class="flex gap-2">
              <GovueButton type="button" variant="secondary" @click="openEditor(edge.id)">
                {{ t('canvas.edges.edit') }}
              </GovueButton>
              <GovueButton type="button" variant="danger" @click="deleteEdge(edge.id)">
                {{ t('canvas.edges.delete') }}
              </GovueButton>
            </div>
          </li>
        </ul>
      </GovueCard>
    </template>

    <GovueDialog v-model:open="editorOpen" :title="t('canvas.editor.title')" size="sm">
      <div v-if="editingEdge" class="flex flex-col gap-4">
        <GovueField :label="t('canvas.form.detailLevel')">
          <GovueSelect v-model="editorDetailLevel" :options="editorDetailLevelOptions" />
        </GovueField>

        <p v-if="editorIsCapped" class="text-sm text-text-muted">
          {{ t('canvas.form.cappedHint') }}
        </p>

        <details>
          <summary class="cursor-pointer text-sm font-medium text-accent-text">
            {{ t('canvas.editor.fieldsDisclosure') }}
          </summary>
          <ul class="mt-2 flex flex-col gap-1 text-sm text-text-muted">
            <li v-for="field in editorFields" :key="field">{{ fieldLabel(field) }}</li>
          </ul>
        </details>

        <p v-if="editorError" role="alert" class="text-sm text-danger-text">{{ editorError }}</p>
      </div>

      <template #actions>
        <GovueButton variant="secondary" :disabled="editorSaving" @click="editorOpen = false">
          {{ t('canvas.editor.cancel') }}
        </GovueButton>
        <GovueButton variant="primary" :loading="editorSaving" @click="saveEditor">
          {{ t('canvas.editor.save') }}
        </GovueButton>
      </template>
    </GovueDialog>
  </GovuePage>
</template>
