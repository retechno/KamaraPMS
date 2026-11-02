<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, InventoryItem, InventoryRequisition, InventoryRequisitionLineInput, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import ApprovalChain from '@/components/inventory/ApprovalChain.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { DateInput } from '@/components/ui/date-input'
import { guardForm } from '@/utils/forms'
import { NativeSelect } from '@/components/ui/native-select'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'
import { requisitionVariant } from '@/utils/movements'
import { stageText } from '@/utils/purchasing'

/**
 * A store requisition, SR (docs/architecture/35-inventory.md 5.4, 5.8, mockups 22 and 23): an outlet or a department asks a warehouse (the store) for goods. A draft or a returned requisition of the signed-in
 * requester is a form; every other state is read only with the chain of approval beside it and, for who can decide the active level, the box "Keputusan Anda" (the quantity may be changed when the chain says so).
 * Once approved the store answers it with transfers (the button goes to the transfer page) or rejects it with a reason.
 */
interface Line {
  item_id: number
  unit_id: number
  qty: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const srId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => srId.value === null)
const me = computed(() => auth.me?.user.id ?? 0)

const sr = ref<InventoryRequisition | null>(null)
const departments = ref<Department[]>([])
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const detail = reactive<Record<number, InventoryItem>>({})
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive({ to_warehouse_id: 0, from_warehouse_id: 0, department_id: 0, needed_on: '', note: '' })
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())

const editable = computed(() => (isNew.value || (!!sr.value && (sr.value.status === 'DRAFT' || sr.value.status === 'RETURNED') && sr.value.requester_id === me.value)) && can('inventory.request'))
const decision = computed(() => sr.value?.decision ?? null)
const canDecide = computed(() => !!decision.value?.can_decide)
const activeStep = computed(() => sr.value?.steps.find((s) => s.cycle === sr.value?.cycle && s.status === 'ACTIVE') ?? null)
const canAnswer = computed(() => !!sr.value && (sr.value.status === 'APPROVED' || sr.value.status === 'PARTIAL') && can('inventory.move'))

async function loadMasters(propertyId: number): Promise<void> {
  const path = { params: { path: { propertyId } } }
  const [w, i] = await Promise.all([
    api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
    api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
  ])
  warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
  items.value = i.data?.items ?? []
  departments.value = (await loadDepartments(propertyId)).filter((d) => d.is_active)
}

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (!srId.value) {
      await loadMasters(propertyId)
      sr.value = null
      lines.value = []
      form.from_warehouse_id = warehouses.value.find((x) => x.kind === 'MAIN')?.id ?? 0
      form.to_warehouse_id = warehouses.value.find((x) => x.id !== form.from_warehouse_id)?.id ?? 0
      form.department_id = 0
      form.needed_on = ''
      form.note = ''
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/requisitions/{id}', { params: { path: { propertyId, id: srId.value } } })
      sr.value = data ?? null
      if (sr.value && (sr.value.status === 'DRAFT' || sr.value.status === 'RETURNED')) await loadMasters(propertyId)
      if (sr.value) fill(sr.value)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, srId], load, { immediate: true })

const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

function fill(r: InventoryRequisition): void {
  form.to_warehouse_id = r.to_warehouse_id
  form.from_warehouse_id = r.from_warehouse_id
  form.department_id = r.department_id ?? 0
  form.needed_on = r.needed_on ?? ''
  form.note = r.note ?? ''
  lines.value = r.lines.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: trimZeros(l.qty) }))
  for (const l of lines.value) void itemDetail(l.item_id)
  edit.qty = Object.fromEntries(r.lines.map((l) => [l.line_no, trimZeros(l.qty)]))
}

async function itemDetail(id: number): Promise<InventoryItem | undefined> {
  const propertyId = pid.value
  if (detail[id] || propertyId === null) return detail[id]
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id } } })
    if (data) detail[id] = data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
  return detail[id]
}
const itemOf = (id: number): InventoryItem | undefined => detail[id] ?? items.value.find((i) => i.id === id)
const unitsOf = (l: Line) => itemOf(l.item_id)?.units ?? []
const freeItems = (own: number): InventoryItem[] => items.value.filter((i) => i.id === own || !lines.value.some((l) => l.item_id === i.id))
async function itemChanged(l: Line): Promise<void> {
  const it = await itemDetail(l.item_id)
  if (it) l.unit_id = it.stock_unit_id
}
function addLine(): void {
  const it = freeItems(0)[0]
  if (!it) return
  const l: Line = { item_id: it.id, unit_id: it.stock_unit_id, qty: '' }
  lines.value.push(l)
  void itemChanged(l)
}

/** What the store holds of the item now, in stock units (the list of items carries the stock of each warehouse). */
function onHand(l: Line): string {
  const it = items.value.find((i) => i.id === l.item_id)
  if (!it) return ''
  const sum = it.stock.filter((s) => s.warehouse_id === form.from_warehouse_id).reduce((a, s) => a + Number(s.qty), 0)
  return `${formatQty(String(sum))} ${it.stock_unit_code}`
}
const complete = computed(
  () => form.to_warehouse_id > 0 && form.from_warehouse_id > 0 && form.to_warehouse_id !== form.from_warehouse_id && lines.value.length > 0 && lines.value.every((l) => l.item_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0),
)

function body(): { to_warehouse_id: number; from_warehouse_id: number; department_id: number | null; needed_on: string | null; note: string | null; lines: InventoryRequisitionLineInput[] } {
  return {
    to_warehouse_id: form.to_warehouse_id,
    from_warehouse_id: form.from_warehouse_id,
    department_id: form.department_id || null,
    needed_on: form.needed_on || null,
    note: form.note.trim() || null,
    lines: lines.value.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim() })),
  }
}

async function run(action: () => Promise<InventoryRequisition | undefined>, message: string): Promise<boolean> {
  busy.value = true
  formError.value = null
  error.value = null
  try {
    const data = await action()
    if (data) {
      notice.value = message
      sr.value = data
      fill(data)
      if (srId.value !== data.id) await router.replace(`/inventory/requisitions/${data.id}`)
    }
    return true
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

async function save(submit: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const saved = await run(async () => {
    if (isNew.value) return (await api.POST('/api/v1/properties/{propertyId}/inventory/requisitions', { params: { path: { propertyId } }, body: { client_uuid: clientUuid.value, ...body() } })).data
    return (await api.PUT('/api/v1/properties/{propertyId}/inventory/requisitions/{id}', { params: { path: { propertyId, id: srId.value! } }, body: { ...body(), version: sr.value!.version } })).data
  }, t('inventory.sr.saved'))
  if (saved && submit && sr.value) {
    await run(async () => (await api.POST('/api/v1/properties/{propertyId}/inventory/requisitions/{id}/submit', { params: { path: { propertyId, id: sr.value!.id } } })).data, t('inventory.sr.submitted'))
  }
}

async function act(path: 'recall' | 'cancel', message: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !sr.value) return
  const id = sr.value.id
  await run(async () => (await api.POST(`/api/v1/properties/{propertyId}/inventory/requisitions/{id}/${path}` as '/api/v1/properties/{propertyId}/inventory/requisitions/{id}/recall', { params: { path: { propertyId, id } } })).data, message)
}

// ------------------------------------------------------------------------------------------ the decision of the signed-in approver
const edit = reactive<{ on: boolean; note: string; qty: Record<number, string> }>({ on: false, note: '', qty: {} })
const edits = computed(() => {
  const out: { line_no: number; qty: string }[] = []
  for (const l of sr.value?.lines ?? []) {
    const v = edit.qty[l.line_no] ?? ''
    if (v && v !== trimZeros(l.qty)) out.push({ line_no: l.line_no, qty: v })
  }
  return out
})

async function decide(approve: boolean): Promise<void> {
  const propertyId = pid.value
  const r = sr.value
  if (propertyId === null || !r || !decision.value) return
  const level = decision.value.level_no
  await run(async () => {
    const note = edit.note.trim() || null
    if (approve) return (await api.POST('/api/v1/properties/{propertyId}/inventory/requisitions/{id}/approve', { params: { path: { propertyId, id: r.id } }, body: { level_no: level, note, edits: edit.on ? edits.value : [] } })).data
    return (await api.POST('/api/v1/properties/{propertyId}/inventory/requisitions/{id}/return', { params: { path: { propertyId, id: r.id } }, body: { level_no: level, note: edit.note.trim() } })).data
  }, approve ? t('inventory.sr.approved') : t('inventory.sr.returned'))
  edit.note = ''
  edit.on = false
}

// ------------------------------------------------------------------------------------------ the store rejects
const rejectOpen = ref(false)
const rejectReason = ref('')
async function reject(): Promise<void> {
  const propertyId = pid.value
  const r = sr.value
  if (propertyId === null || !r) return
  const ok = await run(async () => (await api.POST('/api/v1/properties/{propertyId}/inventory/requisitions/{id}/reject', { params: { path: { propertyId, id: r.id } }, body: { reason: rejectReason.value.trim() } })).data, t('inventory.sr.rejected'))
  if (ok) {
    rejectOpen.value = false
    rejectReason.value = ''
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.sr.newTitle') : t('inventory.sr.viewTitle', { number: sr?.sr_number ?? '' })" :description="isNew ? t('inventory.sr.hint') : ''">
    <template v-if="sr" #marks><Badge :variant="requisitionVariant(sr.status)" data-testid="sr-status">{{ t(`inventory.requisitionStatus.${sr.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/requisitions" data-testid="back">{{ t('inventory.sr.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="grid gap-4" :class="sr ? 'xl:grid-cols-[minmax(0,1fr)_24rem]' : ''">
    <section class="flex min-w-0 flex-col gap-4">
      <form v-if="editable" novalidate class="flex flex-col gap-4" data-testid="sr-form" @submit.prevent="guardForm($event) && save(false)">
        <ErrorNotice v-if="formError" :error="formError" inline data-testid="sr-error" />
        <p v-if="sr?.status === 'RETURNED' && sr.returned_note" class="alert warning" data-testid="returned-note">{{ t('inventory.request.returnedNote', { note: sr.returned_note }) }}</p>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <FormField :label="t('inventory.sr.toWarehouse')" :error="fieldError('to_warehouse_id')" required>
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.to_warehouse_id" name="to_warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('inventory.sr.fromWarehouse')" :error="fieldError('from_warehouse_id')" required>
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.from_warehouse_id" name="from_warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('inventory.request.department')" :error="fieldError('department_id')" :hint="t('inventory.sr.departmentHint')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.department_id" name="department_id" :aria-invalid="invalid">
                <option :value="0">{{ t('inventory.sr.departmentAuto') }}</option>
                <option v-for="d in departments" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('inventory.request.neededOn')" :error="fieldError('needed_on')"><template #default="{ id }"><DateInput :id="id" v-model="form.needed_on" name="needed_on" /></template></FormField>
        </div>
        <FormField :label="t('inventory.request.note')" :error="fieldError('note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>

        <div class="overflow-x-auto">
          <table class="w-full min-w-[40rem] text-sm" data-testid="sr-lines">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.unit') }}</th>
                <th class="py-1 pr-2 font-medium">{{ t('inventory.sr.storeStock') }}</th><th />
              </tr>
            </thead>
            <tbody>
              <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
                <td class="py-2 pr-2">
                  <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.request.item')" :aria-invalid="!!lineError(i, 'item_id')" @change="itemChanged(l)">
                    <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
                  </NativeSelect>
                  <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
                </td>
                <td class="py-2 pr-2">
                  <QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.request.qty')" :aria-invalid="!!lineError(i, 'qty')" />
                  <p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
                </td>
                <td class="py-2 pr-2">
                  <NativeSelect v-model.number="l.unit_id" :name="`unit_${i}`" class="w-28" :aria-label="t('inventory.request.unit')"><option v-for="u in unitsOf(l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option></NativeSelect>
                </td>
                <td class="py-2 pr-2 text-muted-foreground" :data-testid="`on-hand-${i}`">{{ onHand(l) }}</td>
                <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`line-remove-${i}`" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <Button type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.request.addLine') }}</Button>
          <div class="flex flex-wrap gap-2">
            <Button v-if="sr" type="button" variant="ghost" data-testid="sr-cancel" :disabled="busy" @click="act('cancel', t('inventory.sr.cancelled'))">{{ t('inventory.request.cancel') }}</Button>
            <Button type="submit" variant="outline" :disabled="busy || !complete" data-testid="sr-save">{{ t('inventory.request.saveDraft') }}</Button>
            <Button type="button" :disabled="busy || !complete" data-testid="sr-submit" @click="guardForm($event, ($event.currentTarget as HTMLButtonElement).form) && save(true)">{{ t('inventory.request.submit') }}</Button>
          </div>
        </div>
      </form>

      <template v-else-if="sr">
        <ErrorNotice v-if="formError" :error="formError" inline data-testid="sr-error" />
        <p v-if="sr.status === 'RETURNED' && sr.returned_note" class="alert warning" data-testid="returned-note">{{ t('inventory.request.returnedNote', { note: sr.returned_note }) }}</p>
        <p v-if="sr.status === 'REJECTED' && sr.rejected_note" class="alert warning" data-testid="rejected-note">{{ t('inventory.sr.rejectedNote', { note: sr.rejected_note }) }}</p>
        <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm" data-testid="sr-facts">
          <dt class="text-muted-foreground">{{ t('inventory.request.requester') }}</dt><dd class="m-0">{{ sr.requester_name }}<template v-if="sr.submitted_at"> · {{ t('inventory.request.submittedAt', { date: formatDate(sr.submitted_at.slice(0, 10)) }) }}</template></dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.department') }}</dt><dd class="m-0">{{ sr.department_name ?? '—' }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.sr.toWarehouse') }}</dt><dd class="m-0">{{ sr.to_warehouse_name }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.sr.fromWarehouse') }}</dt><dd class="m-0">{{ sr.from_warehouse_name }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.neededOn') }}</dt><dd class="m-0">{{ sr.needed_on ? formatDate(sr.needed_on) : '—' }}</dd>
          <dt v-if="sr.note" class="text-muted-foreground">{{ t('inventory.request.note') }}</dt><dd v-if="sr.note" class="m-0">{{ sr.note }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.stageLabel') }}</dt><dd class="m-0" data-testid="sr-stage">{{ stageText(sr.stage, ['APPROVED', 'PARTIAL', 'FULFILLED'].includes(sr.status) ? 'APPROVED' : sr.status) }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.sr.estValue') }}</dt><dd class="m-0" data-testid="sr-total">{{ formatMoney(sr.est_total) }}</dd>
        </dl>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[40rem] text-sm" data-testid="sr-lines-read">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.sr.requested') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.sr.sent') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.sr.left') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.sr.storeStock') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.sr.estValue') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="l in sr.lines" :key="l.line_no" class="border-t border-border" :data-testid="`read-line-${l.line_no}`">
                <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
                <td class="py-2 pr-2 text-right">
                  <QuantityInput v-if="canDecide && edit.on && activeStep?.edit_allowed" v-model="edit.qty[l.line_no]!" :name="`edit_qty_${l.line_no}`" class="w-24 text-right" :aria-label="t('inventory.sr.requested')" />
                  <template v-else>{{ formatQty(l.qty) }} {{ l.unit_code }}<span v-if="l.unit_code !== l.stock_unit_code" class="block text-xs text-muted-foreground">({{ formatQty(l.qty_stock) }} {{ l.stock_unit_code }})</span></template>
                </td>
                <td class="py-2 pr-2 text-right">{{ formatQty(l.qty_issued_stock) }} {{ l.stock_unit_code }}</td>
                <td class="py-2 pr-2 text-right" :data-testid="`left-${l.line_no}`">{{ formatQty(l.qty_left_stock) }} {{ l.stock_unit_code }}</td>
                <td class="py-2 pr-2 text-right text-muted-foreground">{{ formatQty(l.on_hand) }} {{ l.stock_unit_code }}</td>
                <td class="py-2 text-right">{{ formatMoney(l.est_value) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="sr.status === 'SUBMITTED' && sr.requester_id === me" class="flex justify-end">
          <Button type="button" variant="outline" :disabled="busy" data-testid="sr-recall" @click="act('recall', t('inventory.sr.recalled'))">{{ t('inventory.sr.recall') }}</Button>
        </div>

        <!-- what answered it: the transfers; and what the store may do -->
        <section v-if="sr.transfers.length || canAnswer" class="rounded-xl border border-border bg-card p-4" data-testid="sr-answers">
          <h2 class="m-0 text-base font-semibold">{{ t('inventory.sr.answers') }}</h2>
          <ul v-if="sr.transfers.length" class="m-0 mt-2 list-none p-0 text-sm">
            <li v-for="x in sr.transfers" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 border-t border-border py-1.5 first:border-0">
              <RouterLink :to="`/inventory/transfers/${x.id}`" class="text-primary hover:underline" :data-testid="`answer-${x.trf_number}`">{{ x.trf_number }}</RouterLink>
              <span class="text-muted-foreground">{{ formatDate(x.business_date) }}</span><span>{{ formatMoney(x.total_value) }}</span><Badge :variant="x.status === 'POSTED' ? 'success' : 'outline'">{{ t(`inventory.postedStatus.${x.status}`) }}</Badge>
            </li>
          </ul>
          <p v-else class="muted mt-1 text-sm">{{ t('inventory.sr.noAnswer') }}</p>
          <div v-if="canAnswer" class="mt-3 flex flex-wrap justify-end gap-2">
            <Button type="button" variant="outline" :disabled="busy" data-testid="sr-reject" @click="rejectOpen = true">{{ t('inventory.sr.reject') }}</Button>
            <Button as-child><RouterLink :to="`/inventory/transfers/new?sr=${sr.id}`" data-testid="sr-answer">{{ t('inventory.sr.answer') }}</RouterLink></Button>
          </div>
        </section>
      </template>
    </section>

    <aside v-if="sr && (sr.status !== 'DRAFT' && sr.status !== 'CANCELLED' || sr.steps.length > 0)" class="flex min-w-0 flex-col gap-4" data-testid="chain-aside">
      <ApprovalChain :request="sr" />
      <section v-if="decision && sr.status === 'SUBMITTED'" class="rounded-xl border border-border bg-card p-4" data-testid="decision-box">
        <h3 class="m-0 text-sm font-semibold">{{ t('inventory.request.yourDecision', { level: decision.level_no }) }}</h3>
        <template v-if="canDecide">
          <p class="muted mt-1 text-sm">{{ t('inventory.sr.decisionHint') }}</p>
          <label v-if="activeStep?.edit_allowed" class="mt-2 flex items-center gap-2 text-sm"><input v-model="edit.on" type="checkbox" name="edit_lines" data-testid="decision-edit" /> {{ t('inventory.sr.editLines') }}</label>
          <FormField class="mt-2" :label="t('inventory.request.decisionNote')" :hint="t('inventory.request.decisionNoteHint')" :error="formError?.fieldMessage('note')">
            <template #default="{ id }"><textarea :id="id" v-model="edit.note" name="note" rows="3" maxlength="500" class="w-full rounded-md border border-border bg-card px-3 py-2 text-sm" data-testid="decision-note" /></template>
          </FormField>
          <div class="mt-3 flex justify-end gap-2">
            <Button type="button" variant="outline" :disabled="busy || !edit.note.trim()" data-testid="decision-return" @click="decide(false)">{{ t('inventory.request.returnIt') }}</Button>
            <Button type="button" :disabled="busy" data-testid="decision-approve" @click="decide(true)">{{ t('inventory.request.approve') }}</Button>
          </div>
        </template>
        <p v-else class="muted mt-1 text-sm" data-testid="decision-refused">{{ t(`inventory.request.cannotDecide.${decision.reason ?? 'NOT_ELIGIBLE'}`) }}</p>
      </section>
    </aside>
  </div>

  <Dialog :open="rejectOpen" @update:open="(v: boolean) => (rejectOpen = v)">
    <DialogContent class="max-w-md p-5" data-testid="reject-dialog">
      <DialogTitle>{{ t('inventory.sr.rejectTitle', { number: sr?.sr_number ?? '' }) }}</DialogTitle>
      <DialogDescription class="mt-2">{{ t('inventory.sr.rejectHint') }}</DialogDescription>
      <ErrorNotice v-if="formError" :error="formError" inline class="mt-2" />
      <FormField class="mt-3" :label="t('inventory.sr.rejectReason')" required><template #default="{ id }"><Input :id="id" v-model="rejectReason" name="reason" maxlength="300" data-testid="reject-reason" /></template></FormField>
      <div class="mt-5 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="rejectOpen = false">{{ t('common.cancel') }}</Button>
        <Button type="button" variant="destructive" :disabled="busy || !rejectReason.trim()" data-testid="reject-confirm" @click="reject">{{ t('inventory.sr.reject') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
