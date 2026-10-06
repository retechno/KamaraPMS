<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import CheckOutWizard from '@/components/CheckOutWizard.vue'
import StayActions from '@/components/StayActions.vue'
import type { CheckOutResult, StayDetail } from '@/api/types'
import { documentPath, openPdf } from '@/utils/documents'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const detail = ref<StayDetail | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const reversing = ref(false)
const reason = ref('')
const busy = ref(false)
const checkingOut = ref(false)

type Night = StayDetail['nightly_rates'][number]
const nightColumns = computed<Column<Night>[]>(() => [
  { key: 'date', label: t('stay.night'), format: 'date' as const },
  { key: 'amount', label: t('stay.amount'), align: 'right', format: 'money' as const },
  { key: 'posted', label: t('stay.charged') },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const canReverse = computed(() => can('frontdesk.reverse_checkin') && detail.value?.stay.status === 'OPEN' && detail.value.stay.arrival_date === businessDate.value && detail.value.segments.length === 1)
const canCheckOut = computed(() => can('frontdesk.checkout') && detail.value?.stay.status === 'OPEN')
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('reservation.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: Number(props.id) } } })
    detail.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !detail.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/reverse-check-in', {
      params: { path: { propertyId, id: Number(props.id) } }, body: { version: detail.value.stay.version, reason: reason.value },
    })
    notice.value = t('stay.reversed')
    reversing.value = false
    reason.value = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (error.value?.code === 'VERSION_CONFLICT') await load()
  } finally {
    busy.value = false
  }
}

async function printCard(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    await openPdf(documentPath.registrationCard(propertyId, Number(props.id)))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function changed(message: string): Promise<void> {
  if (message) notice.value = message
  await load()
}

async function checkedOut(result: CheckOutResult): Promise<void> {
  notice.value = t('stay.checkedOut', { n: result.posted_room_charges.length })
  await load()
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="detail ? t('stay.title', { number: detail.stay.stay_number }) : t('stay.titlePlain')">
    <template #actions><RouterLink to="/in-house" class="text-sm text-primary hover:underline">{{ t('stay.back') }}</RouterLink></template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('reservation.read')" class="muted" data-testid="no-access">{{ t('stay.noAccess') }}</p>

  <template v-else-if="detail">
    <Card class="mb-4" data-testid="summary">
      <CardContent class="pt-4">
        <p class="m-0 flex flex-wrap items-center gap-2">
          <Badge variant="secondary" data-testid="stay-status">{{ detail.stay.status }}</Badge>
          <span>
            {{ detail.guest.first_name }} {{ detail.guest.last_name }} · {{ $date(detail.stay.arrival_date) }} &rarr; {{ $date(detail.stay.departure_date) }} ·
            {{ t('stay.adults', { n: detail.stay.adult_count, c: detail.stay.child_count }) }} · {{ detail.line.room_type_code }}
          </span>
        </p>
        <p class="mb-0 mt-2 text-sm text-muted-foreground">
          {{ t('stay.reservation') }} <RouterLink :to="`/reservations/${detail.line.reservation_id}`" class="text-primary hover:underline">{{ detail.line.confirmation_number }}</RouterLink>
          <template v-for="f in detail.folios" :key="f.id"> · {{ t('stay.folio') }}<small v-if="f.folio_type === 'COMPANY'" class="text-muted-foreground"> ({{ t('billingInstructions.company') }})</small> <RouterLink :to="`/folios/${f.id}`" class="text-primary hover:underline" :data-testid="`folio-${f.id}`">{{ f.folio_number }}</RouterLink> ({{ t('stay.balance', { amount: $money(f.balance) }) }})</template>
        </p>
        <p v-if="detail.guests.length" class="mb-0 mt-2 text-sm text-muted-foreground" data-testid="companions">{{ t('stay.with', { names: detail.guests.map((g) => `${g.first_name ?? ''} ${g.last_name}`.trim()).join(', ') }) }}</p>
        <div class="mt-4 flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" data-testid="print-card" @click="printCard">{{ t('stay.registrationCard') }}</Button>
          <Button v-if="canReverse && !reversing" type="button" variant="outline" data-testid="reverse" @click="reversing = true">{{ t('stay.reverseCheckIn') }}</Button>
          <Button v-if="canCheckOut && !checkingOut" type="button" data-testid="checkout" @click="checkingOut = true">{{ t('stay.checkOut') }}</Button>
        </div>
        <form v-if="reversing" class="mt-3 flex flex-wrap items-end gap-3" novalidate data-testid="reverse-form" @submit.prevent="reverse">
          <FormField class="w-80" :label="t('stay.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <Button type="submit" :disabled="busy || !reason.trim()">{{ t('stay.reverse') }}</Button>
          <Button type="button" variant="outline" @click="reversing = false">{{ t('stay.keep') }}</Button>
        </form>
      </CardContent>
    </Card>

    <CheckOutWizard v-if="checkingOut && detail.stay.status === 'OPEN'" :detail="detail" @cancel="checkingOut = false" @done="checkedOut" />
    <StayActions v-if="!checkingOut" :detail="detail" @changed="changed" />

    <Card class="mb-4">
      <CardHeader><CardTitle>{{ t('stay.rooms') }}</CardTitle></CardHeader>
      <CardContent>
        <ul class="m-0 pl-5 text-sm" data-testid="segments">
          <li v-for="s in detail.segments" :key="s.id">{{ t('stay.segment', { room: s.room_number, from: $date(s.start_business_date) }) }}<template v-if="s.end_business_date">{{ t('stay.segmentTo', { to: $date(s.end_business_date) }) }}</template><template v-else>{{ t('stay.segmentCurrent') }}</template></li>
        </ul>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle>{{ t('stay.nights') }}</CardTitle></CardHeader>
      <CardContent>
        <DataTable :columns="nightColumns" :rows="detail.nightly_rates" row-key="date" :caption="t('stay.nights')" data-testid="nights">
          <template #cell-posted="{ row }">{{ row.posted ? t('stay.yes') : t('stay.notYet') }}</template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
