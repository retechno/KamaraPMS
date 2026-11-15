<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { DateInput } from '@/components/ui/date-input'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'

type Plan = components['schemas']['PlatformPlan']
type Preview = components['schemas']['PlatformInvoicePreview']

/**
 * Issue an invoice for the next period of a tenant. The period, the next number, the amount (periods x price) and the due date come from the preview of the server
 * (GET `/tenants/{code}/invoices/preview`), which is asked again when the plan or the number of periods changes; an amount or a due date the operator has typed is kept.
 * The preview also says when an invoice of the tenant is still open, and that is shown as a notice (the server decides whether the new one may be issued).
 */
const props = defineProps<{ open: boolean; tenantCode: string; currentPlan: string; plans: Plan[] }>()
const emit = defineEmits<{ 'update:open': [boolean]; issued: [] }>()

const plan = ref('')
const periods = ref<string | number>('1')
const preview = ref<Preview | null>(null)
const amount = ref('')
const due = ref<string | null>('')
const notes = ref('')
const amountTouched = ref(false)
const dueTouched = ref(false)
const busy = ref(false)
const loadingPreview = ref(false)
const error = ref<ApiError | null>(null)
const invalid = ref<Record<string, string>>({})
let ticket = 0

const planOptions = computed(() => props.plans.filter((p) => p.is_active || p.code === props.currentPlan))
const periodsValid = computed(() => /^\d+$/.test(String(periods.value).trim()) && Number(periods.value) >= 1 && Number(periods.value) <= 36)

/** "7500000.000" -> "7500000": the field keeps the plain decimal text without the zeros the server pads. */
const plain = (v: string): string => (v.includes('.') ? v.replace(/0+$/, '').replace(/\.$/, '') : v)

async function loadPreview(): Promise<void> {
  if (!periodsValid.value) return
  const mine = ++ticket
  loadingPreview.value = true
  try {
    const { data } = await platformApi.GET('/api/v1/platform/tenants/{code}/invoices/preview', {
      params: { path: { code: props.tenantCode }, query: { plan: plan.value, periods: Number(periods.value) } },
    })
    if (mine !== ticket || !data) return
    preview.value = data
    error.value = null
    if (!amountTouched.value) amount.value = plain(data.amount)
    if (!dueTouched.value) due.value = data.due_on
  } catch (e) {
    if (mine !== ticket) return
    if (!(e instanceof ApiError)) throw e
    preview.value = null
    error.value = e
  } finally {
    if (mine === ticket) loadingPreview.value = false
  }
}

watch(
  () => props.open,
  (now) => {
    if (!now) return
    plan.value = props.currentPlan
    periods.value = '1'
    preview.value = null
    amount.value = ''
    due.value = ''
    notes.value = ''
    amountTouched.value = false
    dueTouched.value = false
    error.value = null
    invalid.value = {}
    void loadPreview()
  },
  { immediate: true },
)

// The amount follows the plan and the periods until the operator has typed one of their own.
watch([plan, periods], () => {
  if (props.open) void loadPreview()
})

function validate(): boolean {
  const out: Record<string, string> = {}
  if (!periodsValid.value) out.periods = t('platform.tenant.issue.periodsInvalid')
  if (amount.value.trim() === '') out.amount = t('platform.tenant.issue.amountInvalid')
  invalid.value = out
  return Object.keys(out).length === 0
}

async function submit(): Promise<void> {
  if (busy.value || !validate()) return
  busy.value = true
  error.value = null
  try {
    await platformApi.POST('/api/v1/platform/tenants/{code}/invoices', {
      params: { path: { code: props.tenantCode } },
      body: {
        plan_code: plan.value,
        periods: Number(periods.value),
        amount: amount.value,
        ...(due.value ? { due_on: due.value } : {}),
        ...(notes.value.trim() ? { notes: notes.value.trim() } : {}),
      },
    })
    emit('update:open', false)
    emit('issued')
  } catch (e) {
    if (!(e instanceof ApiError)) throw e
    error.value = e
  } finally {
    busy.value = false
  }
}

const fieldError = (name: string): string | undefined => invalid.value[name] ?? error.value?.fieldMessage(name)
const optionText = (p: Plan): string => t('platform.tenant.issue.planOption', { plan: p.code, price: `${p.currency_code === 'IDR' ? 'Rp' : p.currency_code} ${formatMoney(p.price.replace(/\.0+$/, ''))}`, n: p.billing_period_months })
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[85vh] max-w-xl overflow-y-auto p-5" data-testid="issue-invoice-dialog" :aria-describedby="undefined" @interact-outside.prevent>
      <form novalidate @submit.prevent="submit">
        <div class="mb-3 flex items-start justify-between gap-3">
          <DialogTitle>{{ t('platform.tenant.issue.title', { code: tenantCode }) }}</DialogTitle>
          <Button type="button" variant="ghost" size="icon" :aria-label="t('common.close')" data-testid="dialog-close" @click="emit('update:open', false)"><X /></Button>
        </div>
        <ErrorNotice v-if="error" :error="error" inline class="mb-3" />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('platform.tenant.issue.plan')" :error="fieldError('plan_code')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="plan" name="plan_code" data-autofocus>
                <option v-for="p in planOptions" :key="p.code" :value="p.code">{{ optionText(p) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('platform.tenant.issue.periods')" :error="fieldError('periods')">
            <template #default="{ id, describedBy, invalid: bad }">
              <Input :id="id" v-model="periods" name="periods" type="number" min="1" max="36" inputmode="numeric" :aria-describedby="describedBy" :aria-invalid="bad || undefined" />
            </template>
          </FormField>
        </div>
        <dl class="m-0 mt-3 grid gap-3 rounded-md bg-muted p-3 text-sm sm:grid-cols-2" data-testid="invoice-preview" :aria-busy="loadingPreview">
          <div>
            <dt class="text-muted-foreground">{{ t('platform.tenant.issue.period') }}</dt>
            <dd class="m-0 font-semibold" data-testid="preview-period">{{ preview ? `${formatDate(preview.period_start)} – ${formatDate(preview.period_end)}` : '–' }}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">{{ t('platform.tenant.issue.nextNumber') }}</dt>
            <dd class="m-0 font-semibold" data-testid="preview-number">{{ preview?.next_number ?? '–' }}</dd>
          </div>
        </dl>
        <p v-if="preview?.open_invoice" class="m-0 mt-3 rounded-md border border-warning bg-warning/15 p-3 text-sm" role="status" data-testid="open-invoice-conflict">
          {{ t('platform.tenant.issue.conflict', { number: preview.open_invoice.number, start: formatDate(preview.open_invoice.period_start), end: formatDate(preview.open_invoice.period_end) }) }}
        </p>
        <div class="mt-3 grid gap-3 sm:grid-cols-2">
          <FormField :label="t('platform.tenant.issue.amount')" :hint="t('platform.tenant.issue.amountHint')" :error="fieldError('amount')">
            <template #default="{ id, describedBy, invalid: bad }">
              <MoneyInput :id="id" v-model="amount" name="amount" :decimals="3" :aria-describedby="describedBy" :aria-invalid="bad || undefined" @update:model-value="amountTouched = true" />
            </template>
          </FormField>
          <FormField :label="t('platform.tenant.issue.due')" :error="fieldError('due_on')">
            <template #default="{ id, describedBy, invalid: bad }">
              <DateInput :id="id" v-model="due" name="due_on" :aria-describedby="describedBy" :aria-invalid="bad || undefined" @change="dueTouched = true" />
            </template>
          </FormField>
        </div>
        <div class="mt-3">
          <FormField :label="t('platform.tenant.issue.notes')" :hint="t('platform.tenant.issue.optional')" :error="fieldError('notes')">
            <template #default="{ id, describedBy }">
              <textarea
                :id="id"
                v-model="notes"
                name="notes"
                rows="2"
                :aria-describedby="describedBy"
                class="w-full rounded-md border border-border bg-card p-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </template>
          </FormField>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="dialog-cancel" @click="emit('update:open', false)">{{ t('platform.tenant.issue.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="dialog-submit">{{ t('platform.tenant.issue.submit') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
