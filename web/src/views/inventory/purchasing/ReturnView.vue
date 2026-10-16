<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryReceipt, InventoryReturn } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'

/**
 * A return to the supplier (docs/architecture/35-inventory.md 4.6, 5.3.6): goods of a receipt go back at the cost of the receipt line, which only goods on the shelf can do. If the receipt was billed, the supplier's
 * credit note (entered in the payables against the bill line) is linked to the return here, and a difference between the credit and the cost of the goods is cleared into the purchase price variance.
 */
interface Line {
  line_no: number
  qty: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const returnId = computed(() => (route.params.id ? Number(route.params.id) : null))
const receiptId = computed(() => (route.query.receipt ? Number(route.query.receipt) : null))
const isNew = computed(() => returnId.value === null)

const receipt = ref<InventoryReceipt | null>(null)
const posted = ref<InventoryReturn | null>(null)
const credits = ref<{ id: number; credit_number: string; supplier_credit_number: string; total: string }[]>([])
const creditId = ref(0)
const reason = ref('')
const lines = ref<Line[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)
const clientUuid = ref(crypto.randomUUID())

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (isNew.value) {
      if (receiptId.value) {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/receipts/{id}', { params: { path: { propertyId, id: receiptId.value } } })
        receipt.value = data ?? null
        lines.value = (data?.lines ?? []).map((l) => ({ line_no: l.line_no, qty: '' }))
      }
      posted.value = null
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/returns/{id}', { params: { path: { propertyId, id: returnId.value! } } })
      posted.value = data ?? null
      if (data && !data.credit_note_id && can('inventory.bill') && can('payables.view')) {
        try {
          const list = await api.GET('/api/v1/properties/{propertyId}/payables/credit-notes', { params: { path: { propertyId }, query: { supplier_id: data.supplier_id, status: 'POSTED' } } })
          credits.value = (list.data?.data ?? []).map((c) => ({ id: c.id, credit_number: c.credit_number, supplier_credit_number: c.supplier_credit_number, total: c.total }))
        } catch {
          credits.value = []
        }
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, returnId, receiptId], load, { immediate: true })

const lineOf = (no: number) => receipt.value?.lines.find((l) => l.line_no === no)
/** What of a receipt line can still go back, in the unit it was received in. */
function returnable(no: number): string {
  const l = lineOf(no)
  if (!l || Number(l.qty_stock) === 0) return '0'
  const left = Number(l.qty_stock) - Number(l.qty_returned_stock)
  return String(Number(((left * Number(l.qty)) / Number(l.qty_stock)).toFixed(6)))
}
const chosen = computed(() => lines.value.filter((l) => isDecimal(l.qty, 6) && Number(l.qty) > 0))
const complete = computed(() => !!receipt.value && reason.value.trim() !== '' && chosen.value.length > 0)

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !receipt.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/returns', {
      params: { path: { propertyId } },
      body: { client_uuid: clientUuid.value, receipt_id: receipt.value.id, reason: reason.value.trim(), lines: chosen.value.map((l) => ({ receipt_line_no: l.line_no, qty: l.qty.trim() })) },
    })
    if (data) await router.replace(`/inventory/returns/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function link(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !posted.value || !creditId.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/returns/{id}/link-credit-note', { params: { path: { propertyId, id: posted.value.id } }, body: { credit_note_id: creditId.value } })
    if (data) {
      posted.value = data
      notice.value = t('inventory.return.linked')
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.return.newTitle') : t('inventory.return.viewTitle', { number: posted?.return_number ?? '' })" :description="isNew ? t('inventory.return.hint') : (posted?.supplier_name ?? '')">
    <template #actions>
      <Button as-child variant="outline"><RouterLink :to="{ path: '/inventory/purchasing', query: { tab: 'returns' } }" data-testid="back">{{ t('inventory.return.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <form v-else-if="loaded && isNew" novalidate class="flex flex-col gap-4" data-testid="return-form" @submit.prevent="post">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="return-error" />
    <p v-if="!receipt" class="muted" data-testid="return-no-receipt">{{ t('inventory.return.chooseReceipt') }} <RouterLink :to="{ path: '/inventory/purchasing', query: { tab: 'receipts' } }" class="text-primary hover:underline">{{ t('inventory.purchasing.tab.receipts') }}</RouterLink></p>
    <template v-else>
      <dl class="m-0 grid max-w-xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted-foreground">{{ t('inventory.return.receipt') }}</dt><dd class="m-0">{{ receipt.receipt_number }} · {{ formatDate(receipt.received_on) }}</dd>
        <dt class="text-muted-foreground">{{ t('inventory.return.supplier') }}</dt><dd class="m-0">{{ receipt.supplier_name }}</dd>
        <dt class="text-muted-foreground">{{ t('inventory.return.warehouse') }}</dt><dd class="m-0">{{ receipt.warehouse_name }}</dd>
      </dl>
      <FormField class="max-w-xl" :label="t('inventory.return.reason')" :error="fieldError('reason')" required><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="300" /></template></FormField>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[34rem] text-sm" data-testid="return-lines">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.return.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.return.received') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.return.returnable') }}</th>
              <th class="py-1 font-medium">{{ t('inventory.return.qty') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(l, i) in lines" :key="l.line_no" class="border-t border-border align-top" :data-testid="`line-${i}`">
              <td class="py-2 pr-2">{{ lineOf(l.line_no)?.item_name }} <span class="text-xs text-muted-foreground">{{ lineOf(l.line_no)?.unit_code }}</span></td>
              <td class="py-2 pr-2 text-right">{{ formatQty(lineOf(l.line_no)?.qty ?? '') }}</td>
              <td class="py-2 pr-2 text-right">{{ formatQty(returnable(l.line_no)) }}</td>
              <td class="py-2"><QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-28" :aria-label="t('inventory.return.qty')" :aria-invalid="!!lineError(i, 'qty')" /><p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="muted text-sm">{{ t('inventory.return.costHint') }}</p>
      <div class="flex justify-end gap-2">
        <Button as-child variant="outline"><RouterLink :to="`/inventory/receipts/${receipt.id}`">{{ t('common.cancel') }}</RouterLink></Button>
        <Button type="submit" :disabled="busy || !complete" data-testid="return-post">{{ t('inventory.return.post') }}</Button>
      </div>
    </template>
  </form>

  <section v-else-if="loaded && posted" class="flex flex-col gap-4" data-testid="return-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="return-error" />
    <dl class="m-0 grid max-w-xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted-foreground">{{ t('inventory.return.receipt') }}</dt><dd class="m-0"><RouterLink :to="`/inventory/receipts/${posted.receipt_id}`" class="text-primary hover:underline">{{ posted.receipt_number }}</RouterLink></dd>
      <dt class="text-muted-foreground">{{ t('inventory.return.warehouse') }}</dt><dd class="m-0">{{ posted.warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.return.reason') }}</dt><dd class="m-0">{{ posted.reason }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.return.creditNote') }}</dt><dd class="m-0" data-testid="return-credit">{{ posted.credit_number ?? t('inventory.return.noCredit') }}</dd>
    </dl>
    <table class="w-full max-w-2xl text-sm" data-testid="return-lines-read">
      <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('inventory.return.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.return.qtyStock') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.return.value') }}</th></tr></thead>
      <tbody>
        <tr v-for="l in posted.lines" :key="l.line_no" class="border-t border-border"><td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td><td class="py-2 pr-2 text-right">{{ formatQty(l.qty_stock) }} {{ l.stock_unit_code }}</td><td class="py-2 text-right">{{ formatMoney(l.value) }}</td></tr>
      </tbody>
      <tfoot><tr class="border-t border-border"><td colspan="2" class="py-2 pr-2 text-right font-medium">{{ t('inventory.return.total') }}</td><td class="py-2 text-right font-semibold" data-testid="return-total">{{ formatMoney(posted.total_value) }}</td></tr></tfoot>
    </table>
    <section v-if="!posted.credit_note_id && can('inventory.bill')" class="rounded-xl border border-border bg-card p-4" data-testid="credit-link">
      <h2 class="m-0 text-base font-semibold">{{ t('inventory.return.linkTitle') }}</h2>
      <p class="muted mt-1 text-sm">{{ t('inventory.return.linkHint') }}</p>
      <div class="mt-2 flex flex-wrap items-end gap-2">
        <FormField class="max-w-sm" :label="t('inventory.return.creditNote')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="creditId" name="credit_note_id"><option :value="0">—</option><option v-for="c in credits" :key="c.id" :value="c.id">{{ c.credit_number }} · {{ c.supplier_credit_number }} · {{ formatMoney(c.total) }}</option></NativeSelect>
          </template>
        </FormField>
        <Button type="button" :disabled="busy || !creditId" data-testid="credit-link-button" @click="link">{{ t('inventory.return.link') }}</Button>
      </div>
    </section>
  </section>
</template>
