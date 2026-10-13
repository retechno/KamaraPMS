<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { RoomingImport, RoomingRow } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t, te } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The rooming list of a group (docs/architecture/30-group-folio.md, part A): one row for each room booked for it, with the name, e-mail and phone of its guest, editable in place, and an import
 * from a CSV that is checked first (a dry run that reports every wrong row) and applied all or nothing. A guest made by a bulk booking is a placeholder, shown muted with a badge until it is named.
 */
const props = defineProps<{ groupId: number; reload?: number }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

interface Edit {
  name: string
  email: string
  phone: string
}
const rows = ref<RoomingRow[]>([])
const edits = ref<Record<number, Edit>>({})
const loading = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const csv = ref('')
const fileName = ref('')
const check = ref<RoomingImport | null>(null)
const importing = ref(false)

const pid = computed(() => property.currentId)
const canRead = computed(() => auth.can('reservation.read', pid.value) && auth.can('guest.read', pid.value))
const canEdit = computed(() => auth.can('reservation.update', pid.value) && auth.can('guest.write', pid.value))
const editable = (r: RoomingRow): boolean => canEdit.value && !!r.guest_id && r.line_status !== 'CANCELLED' && r.line_status !== 'NO_SHOW'

const edit = (r: RoomingRow): Edit => (edits.value[r.line_id] ??= { name: r.name, email: r.email, phone: r.phone })
const dirty = (r: RoomingRow): boolean => {
  const e = edits.value[r.line_id]
  return !!e && (e.name.trim() !== r.name || e.email.trim() !== r.email || e.phone.trim() !== r.phone)
}
const dirtyRows = computed(() => rows.value.filter(dirty))
const fieldMessage = (i: number, field: string): string | undefined => error.value?.fieldMessage(`rows[${i}].${field}`)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups/{id}/rooming-list', { params: { path: { propertyId, id: props.groupId } } })
    rows.value = data?.data ?? []
    edits.value = {}
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}
watch(() => [pid.value, props.groupId, props.reload], () => void load(), { immediate: true })

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !dirtyRows.value.length) return
  saving.value = true
  error.value = null
  notice.value = ''
  try {
    const sent = dirtyRows.value
    const { data } = await api.PATCH('/api/v1/properties/{propertyId}/groups/{id}/rooming-list', {
      params: { path: { propertyId, id: props.groupId } },
      body: { rows: sent.map((r) => ({ line_id: r.line_id, name: edit(r).name.trim(), email: edit(r).email.trim(), phone: edit(r).phone.trim() })) },
    })
    notice.value = data && data.updated ? t('rooming.saved', { n: data.updated }) : t('rooming.nothing')
    await load()
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function onFile(ev: Event): Promise<void> {
  const file = (ev.target as HTMLInputElement).files?.[0]
  check.value = null
  error.value = null
  if (!file) {
    csv.value = fileName.value = ''
    return
  }
  fileName.value = file.name
  csv.value = await file.text()
}

async function sendCsv(dryRun: boolean): Promise<RoomingImport | null> {
  const propertyId = pid.value
  if (propertyId === null || !csv.value) return null
  const { data } = await api.POST('/api/v1/properties/{propertyId}/groups/{id}/rooming-list/import', {
    params: { path: { propertyId, id: props.groupId }, query: { dry_run: dryRun } },
    body: csv.value as never,
    bodySerializer: (b: unknown) => String(b),
    headers: { 'Content-Type': 'text/csv' },
  })
  return data ?? null
}

async function checkFile(): Promise<void> {
  importing.value = true
  error.value = null
  notice.value = ''
  try {
    check.value = await sendCsv(true)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    importing.value = false
  }
}

async function applyFile(): Promise<void> {
  importing.value = true
  error.value = null
  try {
    const done = await sendCsv(false)
    notice.value = t('rooming.applied', { n: done?.changes.length ?? 0 })
    check.value = null
    csv.value = fileName.value = ''
    await load()
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    const problems = (e instanceof ApiError ? (e.context as { problems?: RoomingImport['problems'] } | undefined)?.problems : undefined) ?? []
    if (problems.length) check.value = { dry_run: false, rows: problems.length, changes: [], problems, applied: false }
  } finally {
    importing.value = false
  }
}

const problemText = (code: string, message: string): string => (te(`rooming.code.${code}`) ? t(`rooming.code.${code}` as never) : message)

/** The list as a CSV the person can fill and import back: the confirmation number is the key of a row. */
function download(): void {
  const q = (v: string): string => `"${v.replace(/"/g, '""')}"`
  const lines = ['confirmation_number,name,email,phone', ...rows.value.map((r) => [r.confirmation_number, q(r.is_placeholder ? '' : r.name), q(r.email), q(r.phone)].join(','))]
  const url = URL.createObjectURL(new Blob([`${lines.join('\n')}\n`], { type: 'text/csv' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'rooming-list.csv'
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <Card v-if="canRead" class="mb-4" data-testid="rooming-list">
    <CardHeader class="flex-row items-start justify-between gap-3">
      <div>
        <CardTitle>{{ t('rooming.title') }}</CardTitle>
        <p class="m-0 mt-1 text-sm text-muted-foreground">{{ t('rooming.hint') }}</p>
      </div>
      <Button v-if="rows.length" type="button" variant="outline" size="sm" data-testid="rooming-download" @click="download">{{ t('rooming.download') }}</Button>
    </CardHeader>
    <CardContent>
      <ErrorNotice v-if="error && !check" :error="error" inline data-testid="rooming-error" />
      <p v-if="notice" class="alert" role="status" data-testid="rooming-notice">{{ notice }}</p>
      <p v-if="!rows.length && !loading" class="m-0 text-sm text-muted-foreground" data-testid="rooming-empty">{{ t('rooming.empty') }}</p>

      <div v-else class="grid gap-3" data-testid="rooming-rows">
        <div
          v-for="(r, i) in rows" :key="r.line_id" :data-testid="`rooming-row-${r.confirmation_number}`"
          :class="cn('grid gap-2 rounded-lg border border-border p-3 md:grid-cols-[11rem_1fr_1fr_1fr_1fr] md:items-start', dirty(r) && 'border-primary')"
        >
          <div class="min-w-0 text-sm">
            <RouterLink :to="`/reservations/${r.reservation_id}`" class="font-medium text-primary hover:underline">{{ r.confirmation_number }}</RouterLink>
            <div class="text-xs text-muted-foreground">{{ r.room_type }}<template v-if="r.room_number"> · {{ r.room_number }}</template></div>
            <div class="text-xs text-muted-foreground">{{ $date(r.arrival_date) }} – {{ $date(r.departure_date) }}</div>
          </div>
          <label class="grid gap-1 text-xs text-muted-foreground">
            <span class="flex items-center gap-2">{{ t('rooming.name') }} <Badge v-if="r.is_placeholder && !dirty(r)" variant="outline" :data-testid="`placeholder-${r.confirmation_number}`">{{ t('rooming.placeholder') }}</Badge></span>
            <Input
              v-model="edit(r).name" :name="`name_${i}`" :disabled="!editable(r)" :aria-invalid="!!fieldMessage(i, 'name')"
              :class="r.is_placeholder && !dirty(r) ? 'text-muted-foreground' : ''" :data-testid="`rooming-name-${r.confirmation_number}`"
            />
            <span v-if="fieldMessage(i, 'name')" class="text-destructive">{{ fieldMessage(i, 'name') }}</span>
          </label>
          <label class="grid gap-1 text-xs text-muted-foreground">
            <span>{{ t('rooming.email') }}</span>
            <Input v-model="edit(r).email" :name="`email_${i}`" type="email" :disabled="!editable(r)" :aria-invalid="!!fieldMessage(i, 'email')" :data-testid="`rooming-email-${r.confirmation_number}`" />
            <span v-if="fieldMessage(i, 'email')" class="text-destructive">{{ fieldMessage(i, 'email') }}</span>
          </label>
          <label class="grid gap-1 text-xs text-muted-foreground">
            <span>{{ t('rooming.phone') }}</span>
            <Input v-model="edit(r).phone" :name="`phone_${i}`" :disabled="!editable(r)" :aria-invalid="!!fieldMessage(i, 'phone')" :data-testid="`rooming-phone-${r.confirmation_number}`" />
            <span v-if="fieldMessage(i, 'phone')" class="text-destructive">{{ fieldMessage(i, 'phone') }}</span>
          </label>
        </div>
      </div>
      <div v-if="canEdit && rows.length" class="mt-3 flex justify-end">
        <Button type="button" :disabled="saving || !dirtyRows.length" data-testid="rooming-save" @click="save">{{ t('rooming.save') }}</Button>
      </div>

      <section v-if="canEdit && rows.length" class="mt-5 border-t border-border pt-4" data-testid="rooming-import">
        <h3 class="m-0 text-sm font-semibold">{{ t('rooming.import') }}</h3>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('rooming.importHint') }}</p>
        <div class="mt-2 flex flex-wrap items-center gap-2">
          <input type="file" accept=".csv,text/csv" name="rooming_file" :aria-label="t('rooming.file')" class="max-w-full text-sm" data-testid="rooming-file" @change="onFile" />
          <Button type="button" variant="outline" size="sm" :disabled="!csv || importing" data-testid="rooming-check" @click="checkFile">{{ t('rooming.check') }}</Button>
          <Button type="button" size="sm" :disabled="!csv || importing || !check || !!check.problems.length || !check.changes.length" data-testid="rooming-apply" @click="applyFile">{{ t('rooming.apply') }}</Button>
        </div>
        <div v-if="check" class="mt-3" aria-live="polite" data-testid="rooming-check-result">
          <p class="m-0 text-sm font-medium">{{ t('rooming.checked', { rows: check.rows, changes: check.changes.length, problems: check.problems.length }) }}</p>
          <p v-if="error && error.code === 'ROOMING_IMPORT_INVALID'" class="alert mt-2">{{ t('rooming.allWrong') }}</p>
          <div v-if="check.problems.length" class="mt-2" data-testid="rooming-problems">
            <p class="m-0 text-xs font-semibold text-destructive">{{ t('rooming.problems') }}</p>
            <ul class="m-0 list-none p-0 text-sm">
              <li v-for="(p, k) in check.problems" :key="k" class="text-destructive" :data-testid="`rooming-problem-${p.row}`">
                {{ t('rooming.line', { row: p.row }) }}<template v-if="p.column"> · {{ p.column }}</template>: {{ problemText(p.code, p.message) }}
              </li>
            </ul>
          </div>
          <div v-if="check.changes.length" class="mt-2" data-testid="rooming-changes">
            <p class="m-0 text-xs font-semibold text-muted-foreground">{{ t('rooming.changes') }}</p>
            <ul class="m-0 list-none p-0 text-sm">
              <li v-for="c in check.changes" :key="c.line_id">{{ c.confirmation_number }}: {{ c.from_name }} → {{ c.to_name }}</li>
            </ul>
          </div>
        </div>
      </section>
    </CardContent>
  </Card>
</template>
