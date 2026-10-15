<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { PosBill, PosOutlet, PosRoomLookup } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { toMinor } from '@/utils/checkin'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * The payment of a bill (docs/architecture/34-pos.md 7, mockup 12): cash, card, QRIS, transfer, a room and a company in one request, the rest that must reach zero, the cash rounded as the outlet rounds
 * it, the change due. A charge to a room is advised by the lookup (the lock, the limit and what is used) and decided by the server; a refusal is shown with what the front desk has to do.
 */
const props = defineProps<{ bill: PosBill }>()
const emit = defineEmits<{ paid: [bill: PosBill, message: string]; close: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const decimals = computed(() => property.current?.currency_decimals ?? 0)

type Method = 'CASH' | 'CARD' | 'QRIS' | 'BANK_TRANSFER' | 'OTHER' | 'ROOM' | 'COMPANY'
interface Row {
  key: number
  method: Method
  amount: string
  reference: string
  label: string
  tendered: string
  stay_id: number | null
  company_id: number | null
  room: PosRoomLookup | null
}

const outlet = ref<PosOutlet | null>(null)
const rows = ref<Row[]>([])
const nextKey = ref(1)
const error = ref<ApiError | null>(null)
const busy = ref(false)
let idempotencyKey = newIdempotencyKey()

// who may do what
const canRoom = computed(() => auth.can('pos.charge_room', pid.value) && outlet.value?.allow_room_charge === true)
const canCompany = computed(() => auth.can('cityledger.transfer', pid.value) && outlet.value?.allow_company_charge === true)
const methods = computed<Method[]>(() => {
  const m: Method[] = []
  if (outlet.value?.drawer) m.push('CASH')
  m.push('CARD', 'QRIS', 'BANK_TRANSFER', 'OTHER')
  if (canRoom.value) m.push('ROOM')
  if (canCompany.value) m.push('COMPANY')
  return m
})
const hasMethod = (m: Method): boolean => rows.value.some((r) => r.method === m)

const minor = (v: string): number => Number(toMinor(v || '0', decimals.value))
const unitsOf = (n: number): string => (decimals.value === 0 ? String(n) : (n / 10 ** decimals.value).toFixed(decimals.value))
const payable = computed(() => minor(props.bill.totals.payable))
const cashPayable = computed(() => minor(props.bill.totals.cash_payable))
const allCash = computed(() => rows.value.length > 0 && rows.value.every((r) => r.method === 'CASH'))
const sum = computed(() => rows.value.reduce((n, r) => n + minor(r.amount), 0))
// Cash that is all of what is paid and that comes to the rounded amount settles the bill; anything else is measured against the amount itself (a part, or a mix with another method).
const target = computed(() => (allCash.value && sum.value === cashPayable.value ? cashPayable.value : payable.value))
const rest = computed(() => target.value - sum.value)
const rounding = computed(() => (allCash.value ? cashPayable.value - payable.value : 0))
const change = computed(() => rows.value.reduce((n, r) => (r.method === 'CASH' && r.tendered ? n + Math.max(0, minor(r.tendered) - minor(r.amount)) : n), 0))

onMounted(async () => {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}', { params: { path: { propertyId, id: props.bill.outlet_id } } })
    outlet.value = data ?? null
    add(data?.drawer ? 'CASH' : 'CARD')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})

function add(method: Method): void {
  const first = rows.value.length === 0
  const left = Math.max(0, payable.value - sum.value)
  rows.value.push({ key: nextKey.value++, method, amount: unitsOf(first ? (method === 'CASH' ? cashPayable.value : payable.value) : left), reference: '', label: '', tendered: '', stay_id: null, company_id: null, room: null })
  if (method === 'COMPANY') void loadCompanies()
}

function remove(key: number): void {
  rows.value = rows.value.filter((r) => r.key !== key)
}

function exact(r: Row): void {
  const others = rows.value.filter((x) => x.key !== r.key).reduce((n, x) => n + minor(x.amount), 0)
  const whole = others === 0 && r.method === 'CASH' ? cashPayable.value : payable.value
  r.amount = unitsOf(Math.max(0, whole - others))
}

// ---- a room
const query = ref<Record<number, string>>({})
const found = ref<Record<number, PosRoomLookup[]>>({})
async function lookup(r: Row): Promise<void> {
  const propertyId = pid.value
  const q = (query.value[r.key] ?? '').trim()
  if (propertyId === null || !q) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/room-lookup', { params: { path: { propertyId, id: props.bill.outlet_id }, query: { q } } })
    found.value[r.key] = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
function chooseRoom(r: Row, room: PosRoomLookup): void {
  r.stay_id = room.stay_id
  r.room = room
  // the amount is at most what remains of the limit
  if (room.open && room.remaining !== null && room.remaining !== undefined) {
    const cap = minor(room.remaining)
    if (minor(r.amount) > cap) r.amount = unitsOf(cap)
  }
}

// ---- a company
const companies = ref<{ id: number; name: string }[]>([])
async function loadCompanies(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || companies.value.length > 0) return
  try {
    const list = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    companies.value = list.filter((c) => c.pos_billing_allowed).map((c) => ({ id: c.id, name: c.name }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

const refusal = computed(() => {
  const e = error.value
  if (!e) return null
  const c = e.context as Record<string, unknown>
  if (e.code === 'OUTLET_FOLIO_LOCKED') return { text: t('posPay.locked', { reason: t(`outletLock.reason_${String(c.reason ?? 'NEVER_OPENED')}`) }) }
  if (e.code === 'OUTLET_LIMIT_EXCEEDED') return { text: t('posPay.exceeded', { remaining: formatMoney(String(c.remaining ?? '0')) }), remaining: String(c.remaining ?? '') }
  if (e.code === 'OUTLET_NOT_ALLOWED') return { text: t('posPay.notAllowed') }
  return null
})

function useRemaining(): void {
  const rem = refusal.value?.remaining
  const r = rows.value.find((x) => x.method === 'ROOM')
  if (!rem || !r) return
  r.amount = rem.includes('.') ? rem.replace(/\.?0+$/, '') : rem
  error.value = null
  add(outlet.value?.drawer ? 'CASH' : 'CARD')
  exact(rows.value[rows.value.length - 1]!)
}

const valid = computed(() => rows.value.length > 0 && rows.value.every((r) => minor(r.amount) > 0 && (r.method !== 'ROOM' || r.stay_id !== null) && (r.method !== 'COMPANY' || r.company_id !== null)) && rest.value >= 0 && (rest.value === 0 || sum.value > 0))

async function pay(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !valid.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/pay', {
      params: { path: { propertyId, id: props.bill.id }, header: { 'Idempotency-Key': idempotencyKey } },
      body: {
        payments: rows.value.map((r) => ({
          method: r.method, amount: r.amount, ...(r.reference ? { reference: r.reference } : {}), ...(r.label ? { label: r.label } : {}),
          ...(r.stay_id ? { stay_id: r.stay_id } : {}), ...(r.company_id ? { company_id: r.company_id } : {}), ...(r.tendered ? { tendered: r.tendered } : {}),
        })),
      },
    })
    if (data) emit('paid', data.bill, data.closed ? (data.change !== '0' && Number(data.change) > 0 ? t('posPay.doneChange', { change: formatMoney(data.change) }) : t('posPay.done')) : t('posPay.partDone'))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) idempotencyKey = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="true" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent class="max-h-[90vh] max-w-xl overflow-y-auto p-5" data-testid="pay-dialog" @interact-outside.prevent>
      <form novalidate @submit.prevent="pay">
        <DialogTitle>{{ t('posPay.title', { bill: bill.bill_number }) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t('posPay.hint') }}</DialogDescription>

        <p class="m-0 mb-3 flex items-baseline justify-between rounded-lg bg-muted px-3 py-2" data-testid="pay-total">
          <span>{{ t('posBill.toPay') }}</span><strong class="text-xl tabular-nums">{{ formatMoney(unitsOf(target)) }}</strong>
        </p>
        <p v-if="rounding !== 0" class="m-0 mb-2 text-xs text-muted-foreground" data-testid="pay-rounding">{{ t('posPay.rounded', { from: formatMoney(unitsOf(payable)), to: formatMoney(unitsOf(cashPayable)) }) }}</p>

        <ErrorNotice v-if="error" :error="error" inline data-testid="pay-error">
          <p v-if="refusal" class="m-0 mt-1 text-sm" data-testid="pay-refusal">{{ refusal.text }}</p>
          <Button v-if="refusal?.remaining" type="button" size="sm" variant="outline" class="mt-2" data-testid="use-remaining" @click="useRemaining">{{ t('posPay.useRemaining') }}</Button>
        </ErrorNotice>

        <ul class="m-0 mb-3 list-none p-0" data-testid="pay-rows">
          <li v-for="r in rows" :key="r.key" class="mb-2 rounded-lg border border-border p-2" :data-testid="`pay-row-${r.method}`">
            <div class="flex flex-wrap items-end gap-2">
              <strong class="w-28 pb-2 text-sm">{{ t(`posPay.method_${r.method}`) }}</strong>
              <FormField :label="t('posPay.amount')" class="min-w-32 flex-1">
                <template #default="{ id }"><MoneyInput :id="id" v-model="r.amount" :decimals="decimals" :name="`amount_${r.method}`" :data-testid="`amount-${r.method}`" /></template>
              </FormField>
              <Button type="button" size="sm" variant="outline" :data-testid="`exact-${r.method}`" @click="exact(r)">{{ t('posPay.exact') }}</Button>
              <Button type="button" size="sm" variant="ghost" :aria-label="t('posBill.remove')" :data-testid="`remove-${r.method}`" @click="remove(r.key)">×</Button>
            </div>
            <div v-if="r.method === 'CASH'" class="mt-2 grid grid-cols-2 gap-2">
              <FormField :label="t('posPay.tendered')"><template #default="{ id }"><MoneyInput :id="id" v-model="r.tendered" :decimals="decimals" name="tendered" data-testid="tendered" /></template></FormField>
              <FormField :label="t('posPay.label')"><template #default="{ id }"><Input :id="id" v-model="r.label" name="label" autocomplete="off" :placeholder="t('posPay.labelHint')" /></template></FormField>
            </div>
            <div v-else-if="r.method === 'ROOM'" class="mt-2" data-testid="room-box">
              <div class="flex gap-2">
                <Input v-model="query[r.key]" name="room" class="flex-1" :placeholder="t('posPay.roomSearch')" :aria-label="t('posPay.roomSearch')" data-testid="room-query" @keydown.enter.prevent="lookup(r)" />
                <Button type="button" variant="outline" data-testid="room-find" @click="lookup(r)">{{ t('posPay.find') }}</Button>
              </div>
              <p v-if="r.room" class="m-0 mt-2 rounded-md bg-primary/10 px-3 py-2 text-sm" data-testid="room-chosen">
                <strong>{{ r.room.room }}</strong> · {{ r.room.guest }}<template v-if="r.room.target_type === 'MASTER'"> · {{ t('posPay.toMaster', { folio: r.room.target_number ?? '' }) }}</template>
              </p>
              <ul v-if="found[r.key]?.length" class="m-0 mt-2 list-none p-0" data-testid="room-results">
                <li v-for="room in found[r.key]" :key="room.stay_id">
                  <button type="button" class="flex min-h-11 w-full flex-wrap items-center gap-x-3 border-t border-border px-1 py-1.5 text-left text-sm hover:bg-accent" :data-testid="`room-${room.stay_id}`" @click="chooseRoom(r, room)">
                    <strong>{{ room.room }}</strong><span class="min-w-0 flex-1 truncate">{{ room.guest }}</span>
                    <span v-if="room.open" class="rounded bg-success/15 px-1.5 text-xs text-success-text">{{ t('posPay.open') }} · {{ t('posPay.remaining', { amount: formatMoney(room.remaining ?? '') }) }}</span>
                    <span v-else class="rounded bg-warning/20 px-1.5 text-xs">{{ t('posPay.locked', { reason: t(`outletLock.reason_${room.reason || 'NEVER_OPENED'}`) }) }}</span>
                    <span v-if="room.limit" class="basis-full text-xs text-muted-foreground">{{ t('posPay.limitUsed', { limit: formatMoney(room.limit), used: formatMoney(room.used ?? '0') }) }}</span>
                  </button>
                </li>
              </ul>
              <p v-else-if="query[r.key] && found[r.key] && found[r.key]!.length === 0" class="m-0 mt-2 text-sm text-muted-foreground">{{ t('posPay.noRoom') }}</p>
            </div>
            <div v-else-if="r.method === 'COMPANY'" class="mt-2">
              <FormField :label="t('posPay.company')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="r.company_id" name="company_id" data-testid="company-select">
                    <option :value="null" disabled>{{ t('posPay.chooseCompany') }}</option>
                    <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.name }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <p v-if="companies.length === 0" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('posPay.noCompany') }}</p>
            </div>
            <div v-else class="mt-2">
              <FormField :label="t('posPay.reference')"><template #default="{ id }"><Input :id="id" v-model="r.reference" name="reference" autocomplete="off" /></template></FormField>
            </div>
          </li>
        </ul>

        <div class="mb-3 flex flex-wrap gap-2" data-testid="pay-add">
          <Button v-for="m in methods" :key="m" type="button" size="sm" variant="outline" :disabled="(m === 'ROOM' || m === 'COMPANY') && hasMethod(m)" :data-testid="`add-${m}`" @click="add(m)">+ {{ t(`posPay.method_${m}`) }}</Button>
        </div>

        <dl class="m-0 mb-4 grid grid-cols-[1fr_auto] gap-x-3 text-sm" data-testid="pay-summary">
          <dt>{{ t('posPay.entered') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(unitsOf(sum)) }}</dd>
          <dt :class="rest !== 0 && 'font-semibold'">{{ rest < 0 ? t('posPay.over') : t('posPay.rest') }}</dt>
          <dd class="m-0 text-right tabular-nums" :class="rest < 0 && 'text-destructive'" data-testid="pay-rest">{{ formatMoney(unitsOf(Math.abs(rest))) }}</dd>
          <template v-if="change > 0"><dt>{{ t('posPay.change') }}</dt><dd class="m-0 text-right font-semibold tabular-nums" data-testid="pay-change">{{ formatMoney(unitsOf(change)) }}</dd></template>
        </dl>
        <p v-if="rest > 0 && sum > 0" class="m-0 mb-3 text-xs text-muted-foreground" data-testid="pay-part">{{ t('posPay.partNote') }}</p>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !valid" data-testid="pay-submit">{{ rest === 0 ? t('posPay.settle') : t('posPay.takePart') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
