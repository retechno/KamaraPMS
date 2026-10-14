<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, GroupHolding, PaymentMethod } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { isPositive } from '@/utils/checkin'
import { formatBalance, formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * What the cancelled reservations of a group still hold (docs/architecture/30-group-folio.md part F): a deposit that was kept, a fee that was not paid. It stays here, and in the list of folios and in the
 * warnings of the night audit, until each is settled. For each reservation a person decides: keep it (nothing is done, it stays listed), refund it (the refund of a payment, with an approver) or
 * turn it into the fee of the cancellation (a manual fee: the person gives the amount, nothing is calculated).
 */
const props = defineProps<{ groupId: number; reload?: number }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const can = (permission: string) => auth.can(permission, pid.value)
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const refundMethods = computed(() => (property.current?.refund_methods?.length ? property.current.refund_methods : ['CASH']))

const rows = ref<GroupHolding[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)

type Decision = 'KEEP' | 'REFUND' | 'FEE'
const decision = reactive<Record<number, Decision>>({})
const form = reactive<Record<number, { payment: number; amount: string; method: string; reason: string }>>({})
const approving = ref<number | null>(null)
const dialogError = ref<ApiError | null>(null)
let key = newIdempotencyKey()

const visible = computed(() => can('folio.read'))

let latest = 0
async function load(): Promise<void> {
  if (!visible.value || pid.value === null) return
  const mine = ++latest
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups/{id}/holdings', { params: { path: { propertyId: pid.value, id: props.groupId } } })
    if (mine !== latest) return
    rows.value = data?.data ?? []
  } catch (e) {
    if (mine === latest) error.value = e instanceof ApiError ? e : null
  } finally {
    if (mine === latest) loaded.value = true
  }
}
watch([() => props.groupId, pid, () => props.reload, visible], () => void load(), { immediate: true })

const decisionOf = (h: GroupHolding): Decision => decision[h.reservation_id] ?? 'KEEP'
const formOf = (h: GroupHolding) => {
  let f = form[h.reservation_id]
  if (!f) {
    const first = h.deposits[0]
    f = { payment: first?.payment_id ?? 0, amount: first?.refundable ?? '', method: refundMethods.value[0] ?? 'CASH', reason: '' }
    form[h.reservation_id] = f
  }
  return f
}
function choose(h: GroupHolding, d: Decision): void {
  decision[h.reservation_id] = d
  const f = formOf(h)
  f.amount = d === 'REFUND' ? (h.deposits.find((x) => x.payment_id === f.payment)?.refundable ?? '') : ''
  f.reason = ''
  error.value = null
}
function pickPayment(h: GroupHolding, id: number): void {
  const f = formOf(h)
  f.payment = id
  f.amount = h.deposits.find((x) => x.payment_id === id)?.refundable ?? ''
}
const refundable = (h: GroupHolding): string => h.deposits.find((x) => x.payment_id === formOf(h).payment)?.refundable ?? '0'
const canSubmit = (h: GroupHolding): boolean => {
  const f = formOf(h)
  if (!f.reason.trim() || !isPositive(f.amount, decimals.value)) return false
  return decisionOf(h) !== 'REFUND' || Number(f.amount) <= Number(refundable(h))
}

async function postFee(h: GroupHolding): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || busy.value || !canSubmit(h)) return
  const f = formOf(h)
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/fees', {
      params: { path: { propertyId, id: h.reservation_id } },
      body: { type: 'CANCEL_FEE', reservation_room_id: null, amount: f.amount.trim(), reason: f.reason.trim() },
    })
    notice.value = t('groupHoldings.feePosted', { folio: data?.folio_number ?? '', amount: formatMoney(data?.item.debit ?? '') })
    await settled(h)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function refund(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const h = rows.value.find((x) => x.reservation_id === approving.value)
  if (propertyId === null || !h || busy.value) return
  const f = formOf(h)
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/payments/{id}/refunds', {
      params: { path: { propertyId, id: f.payment }, header: { 'Idempotency-Key': key } },
      body: { amount: f.amount.trim(), reason: f.reason.trim(), approval, payment_method: f.method as PaymentMethod },
    })
    key = newIdempotencyKey()
    approving.value = null
    notice.value = t('groupHoldings.refunded', { number: h.confirmation_number })
    await settled(h)
  } catch (e) {
    // the dialog stays open with the reason, so a mistyped password can be retried
    dialogError.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

async function settled(h: GroupHolding): Promise<void> {
  delete decision[h.reservation_id]
  delete form[h.reservation_id]
  await load()
  emit('changed')
}
const methodLabel = (m: string): string => t(`cashier.${m}` as never)
</script>

<template>
  <Card v-if="visible && (rows.length || notice)" class="mt-4" data-testid="group-holdings">
    <CardHeader><CardTitle>{{ t('groupHoldings.title') }}</CardTitle></CardHeader>
    <CardContent>
      <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('groupHoldings.hint') }}</p>
      <ErrorNotice v-if="error" :error="error" inline data-testid="group-holdings-error" />
      <p v-if="notice" class="notice" role="status" data-testid="group-holdings-notice">{{ notice }}</p>
      <ul class="m-0 list-none space-y-3 p-0">
        <li v-for="h in rows" :key="h.reservation_id" class="rounded-md border border-border p-3" :data-testid="`holding-${h.reservation_id}`">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <p class="m-0 text-sm">
              <RouterLink :to="`/reservations/${h.reservation_id}`" class="font-medium text-primary hover:underline">{{ h.confirmation_number }}</RouterLink>
              <span v-if="h.guest_name" class="text-muted-foreground"> · {{ h.guest_name }}</span>
            </p>
            <p class="m-0 text-sm tabular-nums" :data-testid="`holding-balance-${h.reservation_id}`">
              {{ Number(h.held_balance) < 0 ? t('groupHoldings.heldDeposit') : t('groupHoldings.owes') }} <strong>{{ formatBalance(Number(h.held_balance) < 0 ? String(-Number(h.held_balance)) : h.held_balance) }}</strong>
              <template v-for="d in h.deposits" :key="d.payment_id"> · <RouterLink :to="`/folios/${d.folio_id}`" class="text-primary hover:underline">{{ d.payment_number }}</RouterLink> {{ methodLabel(d.payment_method) }}</template>
            </p>
          </div>
          <div class="mt-2 flex flex-wrap items-center gap-2" role="group" :aria-label="t('groupHoldings.decide')">
            <Button type="button" size="sm" :variant="decisionOf(h) === 'KEEP' ? 'default' : 'outline'" :data-testid="`decide-keep-${h.reservation_id}`" @click="choose(h, 'KEEP')">{{ t('groupHoldings.keep') }}</Button>
            <Button v-if="h.deposits.length && can('payment.refund')" type="button" size="sm" :variant="decisionOf(h) === 'REFUND' ? 'default' : 'outline'" :data-testid="`decide-refund-${h.reservation_id}`" @click="choose(h, 'REFUND')">{{ t('groupHoldings.refund') }}</Button>
            <Button v-if="can('folio.post_charge')" type="button" size="sm" :variant="decisionOf(h) === 'FEE' ? 'default' : 'outline'" :data-testid="`decide-fee-${h.reservation_id}`" @click="choose(h, 'FEE')">{{ t('groupHoldings.fee') }}</Button>
            <span v-if="decisionOf(h) === 'KEEP'" class="text-xs text-muted-foreground" :data-testid="`keep-note-${h.reservation_id}`">{{ t('groupHoldings.keepNote') }}</span>
          </div>
          <form v-if="decisionOf(h) !== 'KEEP'" class="mt-3 grid gap-3 sm:grid-cols-2" novalidate :data-testid="`decision-form-${h.reservation_id}`" @submit.prevent="decisionOf(h) === 'REFUND' ? (approving = h.reservation_id) : postFee(h)">
            <FormField v-if="decisionOf(h) === 'REFUND' && h.deposits.length > 1" :label="t('groupHoldings.payment')">
              <template #default="{ id }">
                <NativeSelect :id="id" :model-value="formOf(h).payment" :name="`holding_payment_${h.reservation_id}`" @update:model-value="pickPayment(h, Number($event))">
                  <option v-for="d in h.deposits" :key="d.payment_id" :value="d.payment_id">{{ d.payment_number }} · {{ formatMoney(d.refundable) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="decisionOf(h) === 'REFUND' ? t('groupHoldings.refundAmount') : t('groupHoldings.feeAmount')" :hint="decisionOf(h) === 'REFUND' ? t('groupHoldings.refundMax', { amount: formatMoney(refundable(h)) }) : t('groupHoldings.feeHint')">
              <template #default="{ id }"><MoneyInput :id="id" v-model="formOf(h).amount" :decimals="decimals" :name="`holding_amount_${h.reservation_id}`" /></template>
            </FormField>
            <FormField v-if="decisionOf(h) === 'REFUND'" :label="t('folio.refundBy')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="formOf(h).method" :name="`holding_method_${h.reservation_id}`">
                  <option v-for="m in refundMethods" :key="m" :value="m">{{ methodLabel(m) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('folio.reason')">
              <template #default="{ id }"><Input :id="id" v-model="formOf(h).reason" :name="`holding_reason_${h.reservation_id}`" maxlength="500" /></template>
            </FormField>
            <div class="flex items-end justify-end gap-2 sm:col-span-2">
              <Button type="button" variant="outline" @click="choose(h, 'KEEP')">{{ t('common.cancel') }}</Button>
              <Button type="submit" :disabled="busy || !canSubmit(h)" :data-testid="`decision-save-${h.reservation_id}`">{{ decisionOf(h) === 'REFUND' ? t('groupHoldings.continueApproval') : t('groupHoldings.postFee') }}</Button>
            </div>
          </form>
        </li>
      </ul>
    </CardContent>
  </Card>
  <ApprovalDialog kind="REFUND" v-if="approving !== null" :title="t('groupHoldings.approveRefund')" :busy="busy" :error="dialogError" @approve="refund" @cancel="approving = null" />
</template>
