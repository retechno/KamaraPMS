<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, BanquetEvent, BanquetItem, BanquetLine, BanquetPackage } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { formatDateShort } from '@/utils/format'

/**
 * Sells an item or a package in a session, or changes a line that is not posted (docs/architecture/36-banquet.md 5, 6.5). The price and the mode start from the master and the setting of the property and are
 * frozen on the line. A discount above the limit of the property asks for the approval "Banquet discount": the line is sent first without one, and when the server says it is needed the approval dialog opens
 * and the same line is sent again with it.
 */
const props = defineProps<{ open: boolean; propertyId: number; event: BanquetEvent; sessionId: number | null; line: BanquetLine | null; items: BanquetItem[]; packages: BanquetPackage[]; defaultMode: 'EXCLUSIVE' | 'INCLUSIVE' }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const sessions = computed(() => props.event.sessions.filter((s) => s.status === 'ACTIVE'))
const form = ref({ session_id: 0, what: '' as string, quantity: '', unit_price: '', mode: 'EXCLUSIVE' as 'EXCLUSIVE' | 'INCLUSIVE', description: '', discount: '' as '' | 'pct' | 'amount', discount_value: '' })
const error = ref<ApiError | null>(null)
const busy = ref(false)
const approving = ref(false)
const approveError = ref<ApiError | null>(null)

watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  approving.value = false
  const l = props.line
  if (l) {
    form.value = {
      session_id: l.session_id, what: l.package_id ? `p${l.package_id}` : `i${l.item_id}`, quantity: l.quantity, unit_price: l.unit_price, mode: l.price_mode, description: l.description,
      discount: l.discount_pct ? 'pct' : l.discount_amount ? 'amount' : '', discount_value: l.discount_pct ?? l.discount_amount ?? '',
    }
  } else {
    form.value = { session_id: props.sessionId ?? sessions.value[0]?.id ?? 0, what: '', quantity: '', unit_price: '', mode: props.defaultMode, description: '', discount: '', discount_value: '' }
  }
})

const pkg = computed(() => (form.value.what.startsWith('p') ? props.packages.find((p) => p.id === Number(form.value.what.slice(1))) : undefined))
const item = computed(() => (form.value.what.startsWith('i') ? props.items.find((i) => i.id === Number(form.value.what.slice(1))) : undefined))
// Choosing the item or the package fills the price and the mode of the master; the person may change them before saving
watch(() => form.value.what, () => {
  if (props.line) return
  const p = pkg.value
  const i = item.value
  if (p) Object.assign(form.value, { unit_price: p.price_per_pax, quantity: '', mode: p.price_mode === 'EXCLUSIVE' || p.price_mode === 'INCLUSIVE' ? p.price_mode : props.defaultMode })
  else if (i) Object.assign(form.value, { unit_price: i.list_price, quantity: '', mode: props.defaultMode })
})
const session = computed(() => sessions.value.find((s) => s.id === form.value.session_id))
const pax = computed(() => session.value?.pax_guaranteed ?? session.value?.pax_estimated ?? 0)
const perPax = computed(() => !!pkg.value || item.value?.unit_basis === 'PER_PAX')

function body(approval?: Approval) {
  const f = form.value
  const discount = f.discount && f.discount_value.trim() ? (f.discount === 'pct' ? { discount_pct: f.discount_value.trim() } : { discount_amount: f.discount_value.trim() }) : {}
  return {
    quantity: f.quantity.trim() || null, unit_price: f.unit_price.trim() || null, price_mode: f.mode, description: f.description.trim() || null, ...discount, approval,
  }
}

async function save(approval?: Approval): Promise<void> {
  busy.value = true
  error.value = null
  approveError.value = null
  const path = { propertyId: props.propertyId, id: props.event.id }
  try {
    if (props.line) {
      const d = body(approval)
      await api.PATCH('/api/v1/properties/{propertyId}/banquet/events/{id}/lines/{lid}', { params: { path: { ...path, lid: props.line.id } }, body: { ...d, clear_discount: !form.value.discount || !form.value.discount_value.trim() } })
    } else {
      const w = form.value.what
      await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/lines', {
        params: { path }, body: { session_id: form.value.session_id, item_id: w.startsWith('i') ? Number(w.slice(1)) : null, package_id: w.startsWith('p') ? Number(w.slice(1)) : null, ...body(approval) },
      })
    }
    approving.value = false
    emit('saved')
    emit('update:open', false)
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    if (approving.value) approveError.value = err
    else if (err?.code === 'APPROVAL_REQUIRED') approving.value = true
    else error.value = err
  } finally {
    busy.value = false
  }
}

</script>

<template>
  <Dialog :open="open && !approving" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-xl overflow-y-auto p-5" data-testid="line-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save()">
        <DialogTitle>{{ line ? t('banquet.line.editTitle') : t('banquet.line.newTitle') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.line.hint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField :label="t('banquet.line.session')" required :error="error?.fieldMessage('session_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.session_id" name="session_id" :disabled="line !== null">
              <option v-for="s in sessions" :key="s.id" :value="s.id">{{ s.name }} · {{ formatDateShort(s.session_date) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('banquet.line.what')" required :error="error?.fieldMessage('item_id') ?? error?.fieldMessage('package_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.what" name="what" :disabled="line !== null">
              <option value="">{{ t('banquet.line.choose') }}</option>
              <optgroup :label="t('banquet.line.packages')"><option v-for="p in packages.filter((x) => x.is_active)" :key="`p${p.id}`" :value="`p${p.id}`">{{ p.name }}</option></optgroup>
              <optgroup :label="t('banquet.line.items')"><option v-for="i in items.filter((x) => x.is_active)" :key="`i${i.id}`" :value="`i${i.id}`">{{ i.name }}</option></optgroup>
            </NativeSelect>
          </template>
        </FormField>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('banquet.line.mode')">
            <template #default="{ id }"><NativeSelect :id="id" v-model="form.mode" name="price_mode"><option value="INCLUSIVE">{{ t('banquet.line.nett') }}</option><option value="EXCLUSIVE">{{ t('banquet.line.plusPlus') }}</option></NativeSelect></template>
          </FormField>
          <FormField :label="pkg ? t('banquet.line.pricePerPax') : t('banquet.line.price')" :error="error?.fieldMessage('unit_price')">
            <template #default="{ id }"><Input :id="id" v-model="form.unit_price" name="unit_price" inputmode="decimal" /></template>
          </FormField>
          <FormField :label="perPax ? t('banquet.line.paxMultiplier') : t('banquet.line.quantity')" :error="error?.fieldMessage('quantity')">
            <template #default="{ id }"><Input :id="id" v-model="form.quantity" name="quantity" inputmode="decimal" :placeholder="perPax ? t('banquet.line.paxOfSession', { n: pax }) : '1'" :disabled="!!pkg" /></template>
          </FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.line.discount')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.discount" name="discount_kind"><option value="">{{ t('banquet.line.noDiscount') }}</option><option value="pct">{{ t('banquet.line.discountPct') }}</option><option value="amount">{{ t('banquet.line.discountAmount') }}</option></NativeSelect>
            </template>
          </FormField>
          <FormField v-if="form.discount" :label="t('banquet.line.discountValue')" :error="error?.fieldMessage('discount_pct') ?? error?.fieldMessage('discount_amount')">
            <template #default="{ id }"><Input :id="id" v-model="form.discount_value" name="discount_value" inputmode="decimal" /></template>
          </FormField>
        </div>
        <FormField :label="t('banquet.line.description')"><template #default="{ id }"><Input :id="id" v-model="form.description" name="description" maxlength="200" /></template></FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ pkg ? t('banquet.line.packageHint') : t('banquet.line.frozenHint') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || (!line && !form.what)" data-testid="line-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog
    v-if="open && approving" kind="BANQUET_DISCOUNT" :title="t('banquet.line.approvalTitle')" :message="t('banquet.line.approvalMessage')" :busy="busy" :error="approveError"
    @approve="(a) => save(a)" @cancel="approving = false"
  />
</template>
