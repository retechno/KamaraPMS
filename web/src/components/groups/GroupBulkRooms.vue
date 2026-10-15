<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, Group, GroupRoomBatchResult, GroupRoomPreview, RatePlan, RoomType } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t, te } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * "Add rooms in bulk" of a group (docs/architecture/30-group-folio.md, part A). A form of rows (room type, number of rooms, dates, rate plan, rate per night, party), a preview that is
 * made again when the rows change (what each row costs, the rooms left per night after the whole request, the row that is short), and one save that books one reservation for each room, all or
 * nothing. A rate that differs from the price in force needs one reason and one approval for the whole request. The Idempotency-Key is kept until the server has answered, so a lost answer
 * that is sent again books nothing twice.
 */
const MAX_ROOMS = 100
const MAX_ROWS = 20

const props = defineProps<{ group: Group }>()
const emit = defineEmits<{ saved: []; close: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

interface Row {
  key: number
  typeId: number
  planId: number
  count: number
  arrival: string
  departure: string
  adults: number
  children: number
  rate: string
  rateTouched: boolean
}

const types = ref<RoomType[]>([])
const plans = ref<RatePlan[]>([])
let nextKey = 1
const rows = ref<Row[]>([])
const confirmNow = ref(true)
const reason = ref('')
const preview = ref<GroupRoomPreview | null>(null)
const previewFor = ref('')
const checking = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const result = ref<GroupRoomBatchResult | null>(null)
const savedSignature = ref('') // the rows right after a save: the "booked" message stays until the person changes them
const approving = ref(false)
const dialogError = ref<ApiError | null>(null)
let idempotencyKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const activeTypes = computed(() => types.value.filter((x) => x.is_active))
const activePlans = computed(() => plans.value.filter((x) => x.is_active))
const canApprove = computed(() => auth.can('reservation.override_rate_approve', pid.value))
const businessDate = computed(() => property.clock?.business_date ?? '')

const newRow = (over: Partial<Row> = {}): Row => ({
  key: nextKey++,
  typeId: activeTypes.value[0]?.id ?? 0,
  planId: activePlans.value[0]?.id ?? 0,
  count: 1,
  // the dates of the group, but never before the business date: a room cannot be booked for a night that is over
  arrival: businessDate.value && businessDate.value > props.group.arrival_date ? businessDate.value : props.group.arrival_date,
  departure: props.group.departure_date,
  adults: 2,
  children: 0,
  rate: '',
  rateTouched: false,
  ...over,
})

const totalRooms = computed(() => rows.value.reduce((n, r) => n + (r.count > 0 ? r.count : 0), 0))
const rowComplete = (r: Row): boolean => r.typeId > 0 && r.planId > 0 && r.count >= 1 && r.count <= MAX_ROOMS && !!r.arrival && !!r.departure && r.departure > r.arrival && r.adults >= 1 && r.children >= 0
const complete = computed(() => rows.value.length > 0 && rows.value.length <= MAX_ROWS && totalRooms.value <= MAX_ROOMS && rows.value.every(rowComplete))
const overLimit = computed(() => rows.value.length > MAX_ROWS || totalRooms.value > MAX_ROOMS)

const requestBody = () => ({
  rows: rows.value.map((r) => ({
    room_type_id: r.typeId, rate_plan_id: r.planId, count: r.count, arrival_date: r.arrival, departure_date: r.departure, adult_count: r.adults, child_count: r.children,
    rate_per_night: r.rateTouched && r.rate.trim() !== '' ? r.rate.trim() : undefined,
  })),
  confirm: confirmNow.value,
})
const signature = computed(() => JSON.stringify(requestBody()))
const stale = computed(() => complete.value && signature.value !== savedSignature.value && previewFor.value !== signature.value)
const anyOverride = computed(() => !!preview.value?.rows.some((r) => r.override))
const previewRow = (i: number) => (preview.value && preview.value.rows.length === rows.value.length ? preview.value.rows[i] : undefined)

let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0
async function makePreview(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !complete.value) return
  const sig = signature.value
  const mine = ++sequence
  checking.value = true
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/groups/{id}/room-batches/preview', { params: { path: { propertyId, id: props.group.id } }, body: requestBody() })
    if (mine !== sequence) return // a newer request is on its way
    preview.value = data ?? null
    previewFor.value = sig
    error.value = null
  } catch (e) {
    if (mine !== sequence) return
    preview.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    if (mine === sequence) checking.value = false
  }
}

watch(signature, () => {
  clearTimeout(timer)
  if (signature.value === savedSignature.value) {
    // the fresh row after a save: nothing to preview yet (the rooms just booked would make it look short)
    preview.value = null
    return
  }
  result.value = null
  if (!complete.value) {
    preview.value = null
    return
  }
  timer = setTimeout(() => void makePreview(), 400)
})
onBeforeUnmount(() => clearTimeout(timer))

async function loadBase(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    ;[types.value, plans.value] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    if (!rows.value.length) rows.value = [newRow()]
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(() => [pid.value, props.group.id], () => void loadBase(), { immediate: true })

const addRow = (): void => {
  if (rows.value.length < MAX_ROWS) rows.value.push(newRow())
}
const copyRow = (r: Row): void => {
  if (rows.value.length < MAX_ROWS) rows.value.splice(rows.value.indexOf(r) + 1, 0, { ...r, key: nextKey++ })
}
const removeRow = (r: Row): void => {
  rows.value = rows.value.filter((x) => x !== r)
}
const onRate = (r: Row): void => {
  r.rateTouched = r.rate.trim() !== ''
}

const typeCode = (id: number): string => types.value.find((x) => x.id === id)?.code ?? ''
function problemText(code: string, message: string, detail?: string): string {
  const key = `groupRooms.problem.${code}`
  return te(key) ? t(key as never, { detail: detail ?? '' }) : message
}

/** What a row sells at, in words: one price when every night has it, else the total of a room and the range per night. */
function priceText(i: number): string {
  const p = previewRow(i)
  if (!p) return ''
  const amounts = p.nightly.map((n) => Number(n.amount))
  const uniform = p.override ? p.rate_per_night !== '' : p.uniform
  if (uniform) return t('groupRooms.price.uniform', { rate: formatMoney(p.rate_per_night || p.grid_rate) })
  if (!amounts.length) return ''
  return t('groupRooms.price.varies', { total: formatMoney(p.room_estimate.total), n: p.nights, min: formatMoney(String(Math.min(...amounts))), max: formatMoney(String(Math.max(...amounts))) })
}

const sortedNights = <T extends { date: string }>(nights: T[]): T[] => [...nights].sort((x, y) => x.date.localeCompare(y.date)) // earliest night first
const hasShort = computed(() => !!preview.value?.rows.some((r) => r.short))
/** Why the save button is off, when it is the preview that says so. */
const blocker = computed(() => {
  if (!complete.value || busy.value) return ''
  if (checking.value || stale.value) return t('groupRooms.blocker.checking')
  if (hasShort.value) return t('groupRooms.blocker.short')
  if (preview.value && !preview.value.ok) return t('groupRooms.blocker.problems')
  return ''
})

const canSave = computed(() => complete.value && !stale.value && !checking.value && !busy.value && !!preview.value?.ok && (!anyOverride.value || reason.value.trim() !== ''))

function onSave(): void {
  if (!canSave.value) return
  if (anyOverride.value && !canApprove.value) {
    dialogError.value = null
    approving.value = true
    return
  }
  void submit()
}

async function submit(approval?: Approval): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/groups/{id}/room-batches', {
      params: { path: { propertyId, id: props.group.id }, header: { 'Idempotency-Key': idempotencyKey } },
      body: { ...requestBody(), rate_override_reason: anyOverride.value ? reason.value.trim() : undefined, rate_override_approval: anyOverride.value ? approval : undefined },
    })
    approving.value = false
    result.value = data ?? null
    idempotencyKey = newIdempotencyKey()
    rows.value = [newRow()]
    savedSignature.value = signature.value
    reason.value = ''
    preview.value = null
    emit('saved')
  } catch (e) {
    if (approval) dialogError.value = e instanceof ApiError ? e : null
    else error.value = e instanceof ApiError ? e : null
    // the server has answered: a new attempt is a new request
    if (e instanceof ApiError) idempotencyKey = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

const failedAt = computed(() => {
  const c = error.value?.context as Record<string, unknown> | undefined
  return c && c.row !== undefined ? t('groupRooms.failedAt', { row: Number(c.row) + 1, room: Number(c.room_in_row ?? 1) }) : ''
})
</script>

<template>
  <Card class="mb-4" data-testid="bulk-rooms">
    <CardHeader class="flex-row items-start justify-between gap-3">
      <div>
        <CardTitle>{{ t('groupRooms.title') }}</CardTitle>
        <p class="m-0 mt-1 text-sm text-muted-foreground">{{ t('groupRooms.hint') }}</p>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('groupRooms.limit', { rooms: MAX_ROOMS, rows: MAX_ROWS }) }}</p>
      </div>
      <Button type="button" variant="outline" size="sm" data-testid="bulk-close" @click="emit('close')">{{ t('groupRooms.close') }}</Button>
    </CardHeader>
    <CardContent>
      <ErrorNotice v-if="error" :error="error" inline data-testid="bulk-error">
        <span v-if="failedAt"> {{ failedAt }}</span>
      </ErrorNotice>
      <p v-if="result" class="notice" role="status" data-testid="bulk-done">
        {{ result.replayed ? t('groupRooms.replayed') : t('groupRooms.done', { n: result.rooms, reservations: result.reservations.length }) }}
      </p>

      <div class="grid gap-3" data-testid="bulk-rows">
        <fieldset
          v-for="(r, i) in rows" :key="r.key" :data-testid="`bulk-row-${i}`"
          :class="cn('m-0 min-w-0 rounded-lg border border-border p-3', previewRow(i)?.short && 'border-destructive')"
        >
          <legend class="px-1 text-xs font-semibold text-muted-foreground">{{ t('groupRooms.row', { n: i + 1 }) }}</legend>
          <div class="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
            <FormField :label="t('groupRooms.type')" class="col-span-2 md:col-span-2 xl:col-span-2">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="r.typeId" :name="`type_${i}`">
                  <option v-for="x in activeTypes" :key="x.id" :value="x.id">{{ x.code }} · {{ x.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('groupRooms.count')">
              <template #default="{ id }"><Input :id="id" v-model.number="r.count" :name="`count_${i}`" type="number" min="1" :max="MAX_ROOMS" inputmode="numeric" /></template>
            </FormField>
            <FormField :label="t('groupRooms.plan')" class="col-span-1 xl:col-span-2">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="r.planId" :name="`plan_${i}`">
                  <option v-for="x in activePlans" :key="x.id" :value="x.id">{{ x.code }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('groupRooms.arrival')">
              <template #default="{ id }"><Input :id="id" v-model="r.arrival" :name="`arrival_${i}`" type="date" :min="group.arrival_date" :max="group.departure_date" /></template>
            </FormField>
            <FormField :label="t('groupRooms.departure')">
              <template #default="{ id }"><Input :id="id" v-model="r.departure" :name="`departure_${i}`" type="date" :min="group.arrival_date" :max="group.departure_date" /></template>
            </FormField>
            <FormField :label="t('groupRooms.rate')" class="col-span-2 md:col-span-2 xl:col-span-2">
              <template #default="{ id }">
                <Input
                  :id="id" v-model="r.rate" :name="`rate_${i}`" inputmode="decimal" autocomplete="off" :placeholder="previewRow(i)?.grid_rate ? formatMoney(previewRow(i)?.grid_rate) : ''"
                  @input="onRate(r)"
                />
                <p class="m-0 mt-1 text-xs text-muted-foreground" data-testid="rate-hint">
                  <template v-if="previewRow(i)?.uniform">{{ t('groupRooms.rateGrid', { rate: formatMoney(previewRow(i)?.grid_rate) }) }}</template>
                  <template v-else-if="previewRow(i)">{{ t('groupRooms.rateVaries') }}</template>
                </p>
              </template>
            </FormField>
            <FormField :label="t('groupRooms.adults')">
              <template #default="{ id }"><Input :id="id" v-model.number="r.adults" :name="`adults_${i}`" type="number" min="1" inputmode="numeric" /></template>
            </FormField>
            <FormField :label="t('groupRooms.children')">
              <template #default="{ id }"><Input :id="id" v-model.number="r.children" :name="`children_${i}`" type="number" min="0" inputmode="numeric" /></template>
            </FormField>
          </div>

          <div v-if="previewRow(i)" class="mt-2 text-sm" :data-testid="`bulk-row-summary-${i}`">
            <span class="tabular-nums">{{ t('groupRooms.price.line', { type: typeCode(r.typeId), rooms: previewRow(i)?.rooms ?? 0, nights: previewRow(i)?.nights ?? 0 }) }}</span>
            <span class="ml-2 tabular-nums text-muted-foreground" :data-testid="`bulk-price-${i}`">{{ priceText(i) }}</span>
            <span class="ml-2 font-medium tabular-nums" :data-testid="`bulk-rowtotal-${i}`">{{ t('groupRooms.price.rowTotal', { total: formatMoney(previewRow(i)?.row_estimate.total) }) }}</span>
            <p v-if="previewRow(i)?.short" class="m-0 mt-1 font-medium text-destructive" :data-testid="`bulk-short-${i}`">
              {{ t('groupRooms.preview.short', { n: previewRow(i)?.short?.missing ?? 0, date: $date(previewRow(i)?.short?.date ?? '') }) }}
            </p>
            <ul v-if="previewRow(i)?.problems.length" class="m-0 mt-1 list-none p-0 text-destructive" :data-testid="`bulk-problems-${i}`">
              <li v-for="(p, k) in previewRow(i)?.problems.filter((x) => x.code !== 'ROOM_TYPE_NOT_AVAILABLE')" :key="k">{{ problemText(p.code, p.message, p.message) }}</li>
            </ul>
          </div>

          <div class="mt-2 flex gap-2">
            <Button type="button" variant="ghost" size="sm" :disabled="rows.length >= MAX_ROWS" :data-testid="`bulk-copy-${i}`" @click="copyRow(r)">{{ t('groupRooms.copyRow') }}</Button>
            <Button type="button" variant="ghost" size="sm" :disabled="rows.length <= 1" :data-testid="`bulk-remove-${i}`" @click="removeRow(r)">{{ t('groupRooms.removeRow') }}</Button>
          </div>
        </fieldset>
      </div>

      <div class="mt-3 flex flex-wrap items-center gap-3">
        <Button type="button" variant="outline" size="sm" :disabled="rows.length >= MAX_ROWS" data-testid="bulk-add-row" @click="addRow">{{ t('groupRooms.addRow') }}</Button>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="confirmNow" name="confirm" type="checkbox" class="size-4 accent-primary" />
          <span>{{ t('groupRooms.confirm') }}</span>
        </label>
        <span class="text-xs text-muted-foreground">{{ t('groupRooms.confirmHint') }}</span>
      </div>
      <p v-if="overLimit" class="alert mt-3" data-testid="bulk-limit">{{ t('groupRooms.limit', { rooms: MAX_ROOMS, rows: MAX_ROWS }) }}</p>

      <section class="mt-4" aria-live="polite" data-testid="bulk-preview">
        <h3 class="m-0 mb-2 text-sm font-semibold">{{ t('groupRooms.preview.title') }}</h3>
        <p v-if="!complete" class="m-0 text-sm text-muted-foreground">{{ t('groupRooms.preview.none') }}</p>
        <p v-else-if="checking" class="m-0 text-sm text-muted-foreground" data-testid="bulk-checking">{{ t('groupRooms.preview.checking') }}</p>
        <p v-else-if="stale" class="m-0 text-sm text-muted-foreground">{{ t('groupRooms.preview.stale') }}</p>
        <template v-else-if="preview">
          <p class="m-0 text-sm" data-testid="bulk-totals">
            {{ t('groupRooms.preview.totals', { rooms: preview.rooms, nights: preview.nights }) }} ·
            <span class="font-semibold tabular-nums">{{ t('groupRooms.preview.estimate') }}: {{ formatMoney(preview.total.total) }}</span>
          </p>
          <p v-if="preview.ok" class="m-0 mt-1 text-sm text-success-text" data-testid="bulk-ok">{{ t('groupRooms.preview.ok') }}</p>
          <div v-for="a in preview.availability" :key="a.room_type_id" class="mt-3" :data-testid="`bulk-avail-${a.code}`">
            <p class="m-0 mb-1 text-xs font-semibold text-muted-foreground">{{ a.code }} · {{ t('groupRooms.preview.availability') }}</p>
            <div class="flex gap-1 overflow-x-auto pb-1">
              <div
                v-for="n in sortedNights(a.nights)" :key="n.date" :data-testid="`bulk-night-${a.code}-${n.date}`"
                :class="cn('min-w-16 shrink-0 rounded-md border border-border px-2 py-1 text-center text-xs', n.left < 0 && 'border-destructive bg-destructive/10 text-destructive')"
              >
                <div class="text-muted-foreground">{{ $date(n.date) }}</div>
                <div class="text-sm font-semibold tabular-nums">{{ n.left }} <span class="font-normal">{{ t('groupRooms.preview.left') }}</span></div>
              </div>
            </div>
          </div>
        </template>
      </section>

      <div v-if="anyOverride" class="mt-4 max-w-xl">
        <FormField :label="t('groupRooms.reason')">
          <template #default="{ id }"><Input :id="id" v-model="reason" name="rate_override_reason" maxlength="500" /></template>
        </FormField>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ !reason.trim() ? t('groupRooms.reasonRequired') : t('groupRooms.reasonHint') }}</p>
      </div>

      <div class="mt-4 flex flex-wrap items-center justify-end gap-3">
        <p v-if="blocker" class="m-0 text-sm text-destructive" data-testid="bulk-blocker">{{ blocker }}</p>
        <Button type="button" :disabled="!canSave" data-testid="bulk-save" @click="onSave">{{ busy ? t('groupRooms.saving') : t('groupRooms.save', { n: totalRooms }) }}</Button>
      </div>
    </CardContent>
  </Card>

  <ApprovalDialog kind="RATE_OVERRIDE"
    v-if="approving" :title="t('rateOverride.approvalTitle')" :message="t('rateOverride.approvalMessage')" :busy="busy" :error="dialogError"
    @approve="(a) => submit(a)" @cancel="approving = false"
  />
</template>
