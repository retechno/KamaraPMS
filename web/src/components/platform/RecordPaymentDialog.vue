<script setup lang="ts">
import { ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { DateInput } from '@/components/ui/date-input'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { todayIn } from '@/utils/dates'
import { formatDate } from '@/utils/format'
import { platformMoney } from '@/utils/platformMoney'

/**
 * Record the payment of an invoice (docs/architecture/39-platform-console.md 11, P6): the invoice, the date and the reference. "After saving" says what the server does with a paid
 * invoice (the paid-through date becomes at least the end of the period, the plan becomes the invoice's); what the hotel's status becomes it works out itself, so it is not promised here.
 * The date starts on the operator's own day (the browser's, in its time zone); it is read here, in one place, so that a test can fake the clock.
 */
const props = defineProps<{ open: boolean; invoice: components['schemas']['PlatformInvoiceRow'] }>()
const emit = defineEmits<{ 'update:open': [boolean]; paid: [components['schemas']['PlatformPayResult']] }>()

const operatorToday = (): string => todayIn(Intl.DateTimeFormat().resolvedOptions().timeZone) ?? new Date().toISOString().slice(0, 10)

const paidOn = ref(operatorToday())
const reference = ref('')
const error = ref<ApiError | null>(null)
const busy = ref(false)

watch(
  () => props.open,
  (now) => {
    if (!now) return
    paidOn.value = operatorToday()
    reference.value = ''
    error.value = null
    busy.value = false
  },
  { immediate: true },
)

const fieldError = (field: string) => error.value?.fieldMessage(field)

async function submit(): Promise<void> {
  if (busy.value || !paidOn.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await platformApi.POST('/api/v1/platform/invoices/{number}/pay', {
      params: { path: { number: props.invoice.number } },
      body: { paid_on: paidOn.value, reference: reference.value.trim() || undefined },
    })
    if (data) {
      emit('paid', data)
      emit('update:open', false)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-w-lg p-5" data-testid="record-payment-dialog">
      <DialogTitle>{{ t('platform.invoices.recordPaymentDialog.title') }}</DialogTitle>
      <DialogDescription class="sr-only">{{ t('platform.invoices.recordPaymentDialog.description') }}</DialogDescription>

      <dl class="m-0 mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-md bg-muted p-3 text-sm" data-testid="pay-summary">
        <dt class="text-muted-foreground">{{ t('platform.invoices.recordPaymentDialog.invoice') }}</dt>
        <dd class="m-0 font-medium">{{ invoice.number }}</dd>
        <dt class="text-muted-foreground">{{ t('platform.invoices.recordPaymentDialog.tenant') }}</dt>
        <dd class="m-0 font-medium">{{ invoice.tenant_name }} ({{ invoice.tenant_code }})</dd>
        <dt class="text-muted-foreground">{{ t('platform.invoices.recordPaymentDialog.period') }}</dt>
        <dd class="m-0 font-medium">{{ formatDate(invoice.period_start) }} – {{ formatDate(invoice.period_end) }} · {{ invoice.plan_code }}</dd>
        <dt class="text-muted-foreground">{{ t('platform.invoices.recordPaymentDialog.amount') }}</dt>
        <dd class="m-0 font-semibold tabular-nums">{{ platformMoney(invoice.currency_code, invoice.amount) }}</dd>
      </dl>

      <form class="mt-4 grid gap-4" novalidate data-testid="pay-form" @submit.prevent="submit">
        <ErrorNotice :error="error" inline data-testid="pay-error" />
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField :label="t('platform.invoices.recordPaymentDialog.paidOn')" required :error="fieldError('paid_on')">
            <template #default="{ id, describedBy, invalid }">
              <DateInput :id="id" v-model="paidOn" v-autofocus name="paid_on" :aria-describedby="describedBy" :aria-invalid="invalid" />
            </template>
          </FormField>
          <FormField :label="t('platform.invoices.recordPaymentDialog.reference')" :error="fieldError('reference')">
            <template #default="{ id, describedBy, invalid }">
              <Input :id="id" v-model="reference" name="reference" maxlength="200" :placeholder="t('platform.invoices.recordPaymentDialog.referencePlaceholder')" :aria-describedby="describedBy" :aria-invalid="invalid" />
            </template>
          </FormField>
        </div>

        <div class="rounded-md border border-border p-3 text-sm" data-testid="pay-after">
          <strong class="block">{{ t('platform.invoices.recordPaymentDialog.after') }}</strong>
          <p class="m-0 mt-1">{{ t('platform.invoices.recordPaymentDialog.afterBody', { date: formatDate(invoice.period_end), plan: invoice.plan_code }) }}</p>
        </div>
        <p class="m-0 text-xs text-muted-foreground">{{ t('platform.invoices.recordPaymentDialog.final') }}</p>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('platform.invoices.recordPaymentDialog.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !paidOn" data-testid="pay-submit">{{ t('platform.invoices.recordPaymentDialog.submit') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
