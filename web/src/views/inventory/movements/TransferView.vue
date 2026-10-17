<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryItem, InventoryRequisition, InventoryTransfer, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'
import { postedVariant } from '@/utils/movements'

/**
 * A transfer between warehouses (docs/architecture/35-inventory.md 4.5, 5.4, D9, mockup 23): one step, the goods are in the destination when it is posted, the value goes with the goods (the average of the
 * source, or the layers it took), no journal. It answers an approved requisition (some of it, the rest stays on the requisition) or is free. The source may not go below zero; the destination may, and the
 * transfer settles what was sold there. A posted transfer is read only; it is reversed on its own day.
 */
interface Line {
  item_id: number
  unit_id: number
  qty: string
  left: string | null // what the requisition has left of the item, in stock units
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const transferId = computed(() => (route.params.id ? Number(route.params.id) : null))
const srQuery = computed(() => (route.query.sr ? Number(route.query.sr) : null))
const isNew = computed(() => transferId.value === null)

const transfer = ref<InventoryTransfer | null>(null)
const sr = ref<InventoryRequisition | null>(null)
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive({ from_warehouse_id: 0, to_warehouse_id: 0, note: '' })
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())
const reverseOpen = ref(false)
const reverseReason = ref('')

const itemOf = (id: number): InventoryItem | undefined => items.value.find((i) => i.id === id)
const stockOf = (itemId: number, warehouseId: number): string => {
  const it = itemOf(itemId)
  return String(it?.stock.filter((s) => s.warehouse_id === warehouseId).reduce((a, s) => a + Number(s.qty), 0) ?? 0)
}
const freeItems = (own: number): InventoryItem[] => items.value.filter((i) => i.id === own || !lines.value.some((l) => l.item_id === i.id))
const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (transferId.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/transfers/{id}', { params: { path: { propertyId, id: transferId.value } } })
      transfer.value = data ?? null
    } else {
      transfer.value = null
      const path = { params: { path: { propertyId } } }
      const [w, i] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
        api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
      ])
      warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
      items.value = i.data?.items ?? []
      if (srQuery.value) {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/requisitions/{id}', { params: { path: { propertyId, id: srQuery.value } } })
        sr.value = data ?? null
        if (sr.value) {
          form.from_warehouse_id = sr.value.from_warehouse_id
          form.to_warehouse_id = sr.value.to_warehouse_id
          lines.value = sr.value.lines
            .filter((l) => Number(l.qty_left_stock) > 0)
            .map((l) => {
              const have = Number(stockOf(l.item_id, sr.value!.from_warehouse_id))
              const send = Math.max(Math.min(Number(l.qty_left_stock), have), 0)
              return { item_id: l.item_id, unit_id: 0, qty: send > 0 ? trimZeros(String(Number(send.toFixed(6)))) : '', left: l.qty_left_stock }
            })
        }
      } else {
        sr.value = null
        lines.value = []
        form.from_warehouse_id = warehouses.value.find((x) => x.kind === 'MAIN')?.id ?? warehouses.value[0]?.id ?? 0
        form.to_warehouse_id = warehouses.value.find((x) => x.id !== form.from_warehouse_id)?.id ?? 0
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, transferId, srQuery], load, { immediate: true })

function addLine(): void {
  const it = freeItems(0)[0]
  if (it) lines.value.push({ item_id: it.id, unit_id: 0, qty: '', left: null })
}
const sending = computed(() => lines.value.filter((l) => isDecimal(l.qty, 6) && Number(l.qty) > 0))
const complete = computed(() => form.from_warehouse_id > 0 && form.to_warehouse_id > 0 && form.from_warehouse_id !== form.to_warehouse_id && sending.value.length > 0)
/** The source cannot go below zero: a line that asks for more than the source holds says so before the server does. */
const tooMuch = (l: Line): boolean => isDecimal(l.qty, 6) && Number(l.qty) > Number(stockOf(l.item_id, form.from_warehouse_id))
const shortAtSource = (l: Line): boolean => l.left !== null && Number(stockOf(l.item_id, form.from_warehouse_id)) < Number(l.left)

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/transfers', {
      params: { path: { propertyId } },
      body: {
        client_uuid: clientUuid.value,
        requisition_id: sr.value?.id ?? null,
        from_warehouse_id: form.from_warehouse_id,
        to_warehouse_id: form.to_warehouse_id,
        note: form.note.trim() || null,
        lines: sending.value.map((l) => ({ item_id: l.item_id, qty: l.qty.trim() })),
      },
    })
    if (data) await router.replace(`/inventory/transfers/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !transfer.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/transfers/{id}/reverse', { params: { path: { propertyId, id: transfer.value.id } }, body: { reason: reverseReason.value.trim() } })
    if (data) {
      transfer.value = data
      notice.value = t('inventory.transfer.reversed')
      reverseOpen.value = false
      reverseReason.value = ''
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.transfer.newTitle') : t('inventory.transfer.viewTitle', { number: transfer?.trf_number ?? '' })" :description="isNew && sr ? t('inventory.transfer.answering', { number: sr.sr_number, who: sr.requester_name }) : ''">
    <template v-if="transfer" #marks><Badge :variant="postedVariant(transfer.status)" data-testid="transfer-status">{{ t(`inventory.postedStatus.${transfer.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink :to="{ path: '/inventory/requisitions', query: { tab: 'transfers' } }" data-testid="back">{{ t('inventory.transfer.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <!-- a new transfer: the requisition it answers, or a free one -->
  <form v-else-if="loaded && isNew" novalidate class="flex flex-col gap-4" data-testid="transfer-form" @submit.prevent="post">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="transfer-error" />
    <p v-if="srQuery && !sr" class="alert warning" data-testid="sr-missing">{{ t('inventory.transfer.srMissing') }}</p>
    <div class="grid gap-3 sm:grid-cols-3">
      <FormField :label="t('inventory.transfer.businessDate')"><template #default="{ id }"><Input :id="id" :model-value="property.clock?.business_date ?? ''" readonly name="business_date" /></template></FormField>
      <FormField :label="t('inventory.transfer.from')" required>
        <template #default="{ id }">
          <Input v-if="sr" :id="id" :model-value="sr.from_warehouse_name" readonly name="from" />
          <NativeSelect v-else :id="id" v-model.number="form.from_warehouse_id" name="from_warehouse_id"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.transfer.to')" required>
        <template #default="{ id }">
          <Input v-if="sr" :id="id" :model-value="sr.to_warehouse_name" readonly name="to" />
          <NativeSelect v-else :id="id" v-model.number="form.to_warehouse_id" name="to_warehouse_id"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
        </template>
      </FormField>
    </div>
    <FormField :label="t('inventory.request.note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>

    <div class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="transfer-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th>
            <th v-if="sr" class="py-1 pr-2 text-right font-medium">{{ t('inventory.transfer.requestedLeft') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.transfer.toStock') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.transfer.fromStock') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.transfer.send') }}</th><th v-if="sr" class="py-1 pr-2 text-right font-medium">{{ t('inventory.transfer.remaining') }}</th><th v-if="!sr" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="(l, i) in lines" :key="l.item_id" class="border-t border-border align-top" :data-testid="`line-${i}`">
            <td class="py-2 pr-2">
              <template v-if="sr">{{ itemOf(l.item_id)?.name }} <span class="text-xs text-muted-foreground">{{ itemOf(l.item_id)?.code }}</span></template>
              <NativeSelect v-else v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.request.item')">
                <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
              </NativeSelect>
            </td>
            <td v-if="sr" class="py-2 pr-2 text-right">{{ formatQty(l.left ?? '') }} {{ itemOf(l.item_id)?.stock_unit_code }}</td>
            <td class="py-2 pr-2 text-right" :class="Number(stockOf(l.item_id, form.to_warehouse_id)) < 0 ? 'text-destructive' : 'text-muted-foreground'" :data-testid="`to-stock-${i}`">{{ formatQty(stockOf(l.item_id, form.to_warehouse_id)) }}</td>
            <td class="py-2 pr-2 text-right" :data-testid="`from-stock-${i}`">{{ formatQty(stockOf(l.item_id, form.from_warehouse_id)) }} {{ itemOf(l.item_id)?.stock_unit_code }}</td>
            <td class="py-2 pr-2">
              <QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.transfer.send')" :aria-invalid="tooMuch(l) || !!lineError(i, 'qty')" />
              <p v-if="tooMuch(l)" class="m-0 mt-1 text-xs text-destructive" :data-testid="`too-much-${i}`">{{ t('inventory.transfer.tooMuch') }}</p>
              <p v-else-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
              <p v-else-if="shortAtSource(l)" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('inventory.transfer.partial') }}</p>
            </td>
            <td v-if="sr" class="py-2 pr-2 text-right">{{ formatQty(String(Math.max(Number(l.left ?? 0) - (isDecimal(l.qty, 6) ? Number(l.qty) : 0), 0))) }}</td>
            <td v-if="!sr" class="py-2 text-right"><Button type="button" variant="ghost" size="sm" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="muted text-sm">{{ t('inventory.transfer.rule') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <Button v-if="!sr" type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.request.addLine') }}</Button><span v-else />
      <div class="flex flex-wrap gap-2">
        <Button as-child variant="outline"><RouterLink :to="sr ? `/inventory/requisitions/${sr.id}` : '/inventory/requisitions?tab=transfers'">{{ t('common.cancel') }}</RouterLink></Button>
        <Button type="submit" :disabled="busy || !complete || sending.some(tooMuch)" data-testid="transfer-post">{{ t('inventory.transfer.post') }}</Button>
      </div>
    </div>
  </form>

  <!-- a posted transfer -->
  <section v-else-if="loaded && transfer" class="flex flex-col gap-4" data-testid="transfer-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="transfer-error" />
    <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted-foreground">{{ t('inventory.transfer.from') }}</dt><dd class="m-0">{{ transfer.from_warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.transfer.to') }}</dt><dd class="m-0">{{ transfer.to_warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.transfer.businessDate') }}</dt><dd class="m-0">{{ formatDate(transfer.business_date) }}</dd>
      <dt v-if="transfer.requisition_number" class="text-muted-foreground">{{ t('inventory.movements.answers') }}</dt>
      <dd v-if="transfer.requisition_number" class="m-0"><RouterLink :to="`/inventory/requisitions/${transfer.requisition_id}`" class="text-primary hover:underline" data-testid="sr-link">{{ transfer.requisition_number }}</RouterLink></dd>
      <dt v-if="transfer.note" class="text-muted-foreground">{{ t('inventory.request.note') }}</dt><dd v-if="transfer.note" class="m-0">{{ transfer.note }}</dd>
      <dt v-if="transfer.reverse_reason" class="text-muted-foreground">{{ t('inventory.transfer.reverseReason') }}</dt><dd v-if="transfer.reverse_reason" class="m-0">{{ transfer.reverse_reason }}</dd>
    </dl>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[32rem] text-sm" data-testid="transfer-lines-read">
        <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.transfer.sent') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.transfer.value') }}</th></tr></thead>
        <tbody>
          <tr v-for="l in transfer.lines" :key="l.line_no" class="border-t border-border" :data-testid="`read-line-${l.line_no}`">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty_stock) }} {{ l.stock_unit_code }}</td><td class="py-2 text-right">{{ formatMoney(l.value) }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td colspan="2" class="py-2 pr-2 text-right font-medium">{{ t('inventory.transfer.total') }}</td><td class="py-2 text-right font-semibold" data-testid="transfer-total">{{ formatMoney(transfer.total_value) }}</td></tr></tfoot>
      </table>
    </div>
    <p class="muted text-sm">{{ t('inventory.transfer.noJournal') }}</p>
    <div v-if="transfer.reversible && can('inventory.move')" class="flex justify-end">
      <Button type="button" variant="outline" data-testid="transfer-reverse" @click="reverseOpen = true">{{ t('inventory.transfer.reverse') }}</Button>
    </div>
  </section>

  <Dialog :open="reverseOpen" @update:open="(v: boolean) => (reverseOpen = v)">
    <DialogContent class="max-w-md p-5" data-testid="reverse-dialog">
      <DialogTitle>{{ t('inventory.transfer.reverseTitle', { number: transfer?.trf_number ?? '' }) }}</DialogTitle>
      <DialogDescription class="mt-2">{{ t('inventory.transfer.reverseHint') }}</DialogDescription>
      <ErrorNotice v-if="formError" :error="formError" inline class="mt-2" />
      <FormField class="mt-3" :label="t('inventory.receipt.reverseReasonLabel')" required><template #default="{ id }"><Input :id="id" v-model="reverseReason" name="reason" maxlength="300" data-testid="reverse-reason" /></template></FormField>
      <div class="mt-5 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="reverseOpen = false">{{ t('common.cancel') }}</Button>
        <Button type="button" variant="destructive" :disabled="busy || !reverseReason.trim()" data-testid="reverse-confirm" @click="reverse">{{ t('inventory.transfer.reverse') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
