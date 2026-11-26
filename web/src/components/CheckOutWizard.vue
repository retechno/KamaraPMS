<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CheckOutPreview, CheckOutResult, StayDetail } from '@/api/types'
import CheckoutPayForm from '@/components/folio/CheckoutPayForm.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

const props = defineProps<{ detail: StayDetail }>()
const emit = defineEmits<{ done: [result: CheckOutResult]; cancel: []; failed: [error: ApiError]; paid: [] }>()
const property = usePropertyStore()
const auth = useAuthStore()

type Step = 'review' | 'done'
const step = ref<Step>('review')
const confirmEarly = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const result = ref<CheckOutResult | null>(null)
let key = newIdempotencyKey()

// What the wizard shows is its own copy of the stay: a payment taken here changes the balances, and the stay is read again.
const live = ref<StayDetail>(props.detail)
const paidHere = ref(false)
// A page that reads the stay again (the header with the balance) hands the new copy down.
watch(() => props.detail, (d) => { live.value = d })
const canPay = computed(() => property.currentId !== null && auth.can('payment.post', property.currentId))
type Folio = StayDetail['folios'][number]
const isCompany = (f: Folio): boolean => (f as { folio_type?: string }).folio_type === 'COMPANY'

// The check-out settles the departure, posts the room charges still due (the night of arrival, a night never posted, the
// night of the business date after midnight), then needs every guest folio at zero. What it will post is not worked out
// here: the server's check-out preview applies the same rules (frontdesk CheckOutPreview, KD-06) and says, per folio, what
// must be paid or refunded first. Before, the wizard added the night audit's dry run of the booked stay, so an early
// leaver who arrived before today was asked for tonight, which the check-out never posts.
const preview = ref<CheckOutPreview | null>(null)
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const previewOf = (f: Folio) => preview.value?.folios.find((p) => p.folio_id === f.id)
// The amount still to settle on a folio, as the server says it (a decimal string); without a preview, its balance.
const dueText = (f: Folio): string => previewOf(f)?.due ?? f.balance
const roomChargesOf = (f: Folio): string | null => {
  const rc = previewOf(f)?.room_charges
  return rc && Number(rc) !== 0 ? rc : null
}
const sign = (amount: string): number => Math.sign(Number(amount)) // only the sign is read: the amounts stay strings
const owesMoney = (f: Folio): boolean => sign(dueText(f)) > 0
const inCredit = (f: Folio): boolean => sign(dueText(f)) < 0
const refundText = (f: Folio): string => dueText(f).replace(/^-/, '')

async function loadPending(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}/check-out/preview', {
      params: { path: { propertyId, id: props.detail.stay.id } },
    })
    preview.value = data ?? null
  } catch {
    preview.value = null // without the preview the wizard shows the balances; the check-out still decides
  }
}
onMounted(loadPending)

async function reload(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  paidHere.value = true
  error.value = null // what the check-out said before the payment is out of date
  emit('paid') // the page reads its own summary again (the balance in its header)
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: props.detail.stay.id } } })
    if (data) live.value = data
    await loadPending()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

const businessDate = computed(() => property.clock?.business_date ?? '')
const early = computed(() => preview.value?.early_departure ?? live.value.stay.departure_date > businessDate.value)
const endsOn = computed(() => preview.value?.departure_date ?? businessDate.value)
const unbalanced = computed(() => live.value.folios.filter((f) => f.status === 'OPEN' && sign(dueText(f)) !== 0))
// What is owed is what the preview says (`dueText`): the balance plus the room charges the check-out will post.
// A guest folio that is not at zero is not checked out: the reason stands by the button (the server refuses it too).
const guestDue = computed(() => unbalanced.value.filter((f) => !isCompany(f)))
const dueReason = computed(() => {
  const f = guestDue.value[0]
  if (!f) return ''
  return owesMoney(f) ? t('checkout.payFirst', { amount: formatMoney(dueText(f)) }) : t('checkout.refundCreditFirst', { amount: formatMoney(refundText(f)) })
})
const blocked = computed(() => (early.value && !confirmEarly.value) || busy.value || guestDue.value.length > 0)

async function submit(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/stays/{id}/check-out', {
      params: { path: { propertyId, id: props.detail.stay.id }, header: { 'Idempotency-Key': key } },
      body: { version: live.value.stay.version, confirm_early_departure: confirmEarly.value },
    })
    if (data) {
      result.value = data
      step.value = 'done'
      emit('done', data)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) {
      key = newIdempotencyKey()
      emit('failed', e) // the page that holds the wizard may need to read again (a stay that moved on under it)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="mb-4" data-testid="checkout-wizard">
    <CardHeader><CardTitle>{{ t('checkout.title') }}</CardTitle></CardHeader>
    <CardContent>
      <ErrorNotice v-if="error" :error="error" inline data-testid="checkout-error">
<template v-if="error.code === 'FOLIO_NOT_BALANCED'"> {{ t('checkout.notBalanced') }}</template>
</ErrorNotice>

      <p v-if="paidHere && error" class="alert warning" role="status" data-testid="checkout-payment-kept">{{ t('checkout.paymentKept') }}</p>
      <p v-else-if="paidHere && step === 'review'" class="notice" role="status" data-testid="checkout-paid">{{ t('checkout.paid') }}</p>

      <template v-if="step === 'review'">
        <ol class="m-0 grid gap-2 pl-5">
          <li :class="{ 'marker:text-primary': !early || confirmEarly }" data-testid="step-departure">
            {{ t('checkout.departure', { date: $date(detail.stay.departure_date) }) }}
            <template v-if="early">
              {{ t('checkout.early', { date: $date(endsOn) }) }}
              <Checkbox v-model="confirmEarly" name="confirm_early" label-class="ml-2 text-sm">{{ t('checkout.confirmEarly') }}</Checkbox>
            </template>
            <template v-else>{{ t('checkout.matches') }}</template>
          </li>
          <li :class="{ 'marker:text-primary': !unbalanced.length }" data-testid="step-folios">
            <template v-if="!unbalanced.length">{{ t('checkout.balanced') }}</template>
            <template v-else>
              {{ t('checkout.withBalance') }}
              <div v-for="f in unbalanced" :key="f.id" class="mt-2" :data-testid="`folio-due-${f.id}`">
                <RouterLink :to="`/folios/${f.id}`" class="text-primary hover:underline" :data-testid="`open-folio-${f.id}`">{{ f.folio_number }}</RouterLink> ({{ $money(dueText(f)) }})
                <p v-if="roomChargesOf(f)" class="m-0 mt-1 text-sm text-muted-foreground" :data-testid="`tonight-charge-${f.id}`">
                  {{ t('checkout.tonightCharge', { amount: $money(roomChargesOf(f) ?? '0') }) }}
                </p>
                <p v-if="isCompany(f)" class="m-0 mt-1 text-sm text-muted-foreground" :data-testid="`company-folio-${f.id}`">
                  {{ t('checkout.companyFolio') }} <RouterLink :to="`/folios/${f.id}`" class="text-primary hover:underline">{{ t('checkout.openFolio') }}</RouterLink>
                </p>
                <p v-else-if="inCredit(f)" class="alert warning m-0 mt-1" role="status" :data-testid="`refund-first-${f.id}`">
                  {{ t('checkout.refundFirst', { amount: $money(refundText(f)) }) }} <RouterLink :to="`/folios/${f.id}`" class="text-primary hover:underline">{{ t('checkout.openFolio') }}</RouterLink>
                </p>
                <CheckoutPayForm v-else-if="owesMoney(f) && canPay" :key="`${f.id}-${dueText(f)}`" :folio-id="f.id" :balance="dueText(f)" @paid="reload" />
              </div>
            </template>
          </li>
        </ol>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="checkout-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</Button>
          <span v-if="dueReason" class="mr-auto self-center text-sm text-muted-foreground" data-testid="checkout-due-reason">{{ dueReason }}</span>
          <Button type="button" :disabled="blocked" data-testid="checkout-submit" @click="submit">{{ t('checkout.submit') }}</Button>
        </div>
      </template>

      <template v-else-if="result">
        <p class="mt-0" data-testid="checkout-done">{{ t('checkout.done') }} <strong>{{ result.housekeeping }}</strong>.</p>
        <ul class="m-0 pl-5 text-sm">
          <li v-for="c in result.posted_room_charges" :key="c.service_date">{{ t('checkout.roomCharge', { date: $date(c.service_date), total: $money(c.total) }) }}</li>
          <li v-for="f in result.folios" :key="f.id">{{ t('checkout.folioStatus', { number: f.folio_number, status: f.status }) }}</li>
        </ul>
      </template>
    </CardContent>
  </Card>
</template>
