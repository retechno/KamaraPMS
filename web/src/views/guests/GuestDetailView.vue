<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GuestHistoryItem, GuestView, PatchGuestRequest } from '@/api/types'
import GuestFields from '@/components/GuestFields.vue'
import { blankGuestForm } from '@/components/guestForm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { formatBusinessDate } from '@/utils/dates'

const props = defineProps<{ id: string }>()

const guest = ref<GuestView | null>(null)
const history = ref<GuestHistoryItem[]>([])
const hidden = ref(0)
const nextCursor = ref<string | undefined>()
const form = reactive(blankGuestForm())
const error = ref<ApiError | null>(null)
const notFound = ref(false)
const saving = ref(false)
const saved = ref(false)

const columns = computed<Column<GuestHistoryItem>[]>(() => [
  { key: 'property_code', label: t('guest.property') },
  { key: 'type', label: t('guest.type') },
  { key: 'number', label: t('guest.number') },
  { key: 'role', label: t('guest.role') },
  { key: 'dates', label: t('guest.dates') },
  { key: 'status', label: t('setup.status') },
])

const guestId = computed(() => Number(props.id))
const title = computed(() => (guest.value ? [guest.value.first_name, guest.value.last_name].filter(Boolean).join(' ') : t('guest.fallback')))

function fill(g: GuestView): void {
  guest.value = g
  for (const k of Object.keys(form) as (keyof typeof form)[]) form[k] = (g[k] as string | undefined) ?? ''
}

async function load(): Promise<void> {
  try {
    const { data } = await api.GET('/api/v1/guests/{id}', { params: { path: { id: guestId.value } } })
    if (data) fill(data)
    await loadHistory()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'GUEST_NOT_FOUND') notFound.value = true
    else error.value = e instanceof ApiError ? e : null
  }
}

async function loadHistory(more = false): Promise<void> {
  const { data } = await api.GET('/api/v1/guests/{id}/history', {
    params: { path: { id: guestId.value }, query: { limit: 50, cursor: more ? nextCursor.value : undefined } },
  })
  history.value = more ? [...history.value, ...(data?.data ?? [])] : (data?.data ?? [])
  hidden.value = data?.hidden_count ?? 0
  nextCursor.value = data?.next_cursor
}

/** Every field is sent: an empty string clears it on the server. */
async function save(): Promise<void> {
  saving.value = true
  saved.value = false
  error.value = null
  try {
    const { data } = await api.PATCH('/api/v1/guests/{id}', {
      params: { path: { id: guestId.value } },
      body: { ...form } as PatchGuestRequest,
    })
    if (data) fill(data)
    saved.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <p v-if="notFound" class="muted" data-testid="not-found">{{ t('guest.notFound') }}</p>
  <template v-else>
    <PageHeader :title="title">
      <template #marks><code v-if="guest" class="text-sm text-muted-foreground">{{ guest.code }}</code></template>
    </PageHeader>

    <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <p v-if="saved" class="muted" role="status" data-testid="saved">{{ t('guest.saved') }}</p>

    <Card v-if="guest" class="mb-4">
      <form novalidate @submit.prevent="save">
        <CardHeader><CardTitle>{{ t('guest.profile') }}</CardTitle></CardHeader>
        <CardContent>
          <GuestFields v-model="form" :error="error" :disabled="!guest.can_edit" />
          <p v-if="!guest.can_edit" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="read-only">{{ t('guest.readOnly') }}</p>
          <div v-else class="mt-4 flex justify-end">
            <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardHeader><CardTitle>{{ t('guest.history') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="!history.length" class="m-0 text-sm text-muted-foreground" data-testid="no-history">{{ t('guest.noHistory') }}</p>
        <DataTable v-else :columns="columns" :rows="history" :row-key="(h) => `${h.type}-${h.id}`" :caption="t('guest.history')">
          <template #cell-type="{ row }">{{ row.type === 'STAY' ? t('guest.stay') : t('guest.reservation') }}</template>
          <template #cell-role="{ row }">{{ row.role.toLowerCase() }}</template>
          <template #cell-dates="{ row }">{{ t('guest.dateRange', { from: formatBusinessDate(row.arrival_date), to: formatBusinessDate(row.departure_date) }) }}</template>
        </DataTable>
        <p v-if="hidden" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="hidden">
          {{ hidden === 1 ? t('guest.hiddenOne') : t('guest.hiddenMany', { n: hidden }) }}
        </p>
        <div v-if="nextCursor" class="mt-3 flex justify-center">
          <Button type="button" variant="outline" @click="loadHistory(true)">{{ t('guest.more') }}</Button>
        </div>
      </CardContent>
    </Card>
  </template>
</template>
