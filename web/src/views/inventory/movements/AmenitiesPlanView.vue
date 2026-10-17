<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Department, InventoryAmenityPlan, InventoryAmenityPreview, InventoryAmenityRunBrief, InventoryItem, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'

/**
 * The plan of the automatic amenities (docs/architecture/35-inventory.md 5.9, I16, mockup 32): on or off, the warehouse they come from, the department and the expense account, the base package of every room
 * type (per room or per guest) and the extras of a room type, with what a room costs. Saved as a new version; the next close that runs uses it. Below it, tonight's preview and the documents of the last closes.
 */
interface PlanLine {
  room_type_id: number // 0: every type
  item_id: number
  unit_id: number
  qty: string
  basis: 'PER_ROOM' | 'PER_GUEST'
}

const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const canManage = computed(() => can('inventory.manage'))

const plan = ref<InventoryAmenityPlan | null>(null)
const preview = ref<InventoryAmenityPreview | null>(null)
const runs = ref<InventoryAmenityRunBrief[]>([])
const warehouses = ref<InventoryWarehouse[]>([])
const departments = ref<Department[]>([])
const items = ref<InventoryItem[]>([])
const roomTypes = ref<{ id: number; code: string; name: string }[]>([])
const accounts = ref<{ id: number; code: string; name: string }[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive({ is_enabled: false, warehouse_id: 0, department_id: 0, expense_account_id: 0 })
const lines = ref<PlanLine[]>([])
const typeTab = ref(0) // 0: the base package, else the id of the room type of the extras

const itemOf = (id: number): InventoryItem | undefined => items.value.find((i) => i.id === id)
const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [p, v, r, w, i] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/amenities/plan', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/amenities/preview', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/amenities/runs', { params: { path: { propertyId }, query: { limit: 10 } } }),
      api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
    ])
    plan.value = p.data ?? null
    preview.value = v.data ?? null
    runs.value = r.data?.data ?? []
    warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
    items.value = i.data?.items ?? []
    departments.value = (await loadDepartments(propertyId)).filter((d) => d.is_active)
    roomTypes.value = (await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))).filter((x) => x.is_active).map((x) => ({ id: x.id, code: x.code, name: x.name }))
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query: { account_type: 'EXPENSE', postable: true, active: true } } })
      accounts.value = (((data as unknown as { data?: { id: number; code: string; name: string }[] } | undefined)?.data) ?? []).map((a) => ({ id: a.id, code: a.code, name: a.name }))
    } catch {
      accounts.value = []
    }
    fill()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

function fill(): void {
  const p = plan.value
  form.is_enabled = p?.is_enabled ?? false
  form.warehouse_id = p?.warehouse_id ?? warehouses.value.find((w) => w.kind === 'DEPARTMENT')?.id ?? 0
  form.department_id = p?.department_id ?? 0
  form.expense_account_id = p?.expense_account_id ?? accounts.value.find((a) => a.code === '5150')?.id ?? 0
  lines.value = (p?.lines ?? []).map((l) => ({ room_type_id: l.room_type_id ?? 0, item_id: l.item_id, unit_id: l.unit_id, qty: trimZeros(l.qty), basis: l.basis }))
}

const shown = computed(() => lines.value.map((l, i) => ({ l, i })).filter((x) => x.l.room_type_id === typeTab.value))
function addLine(): void {
  const used = new Set(shown.value.map((x) => x.l.item_id))
  const it = items.value.find((i) => !used.has(i.id)) ?? items.value[0]
  if (!it) return
  lines.value.push({ room_type_id: typeTab.value, item_id: it.id, unit_id: it.stock_unit_id, qty: '1', basis: 'PER_ROOM' })
}
function itemChanged(l: PlanLine): void {
  const it = itemOf(l.item_id)
  if (it) l.unit_id = it.stock_unit_id
}
const unitsOf = (l: PlanLine) => itemOf(l.item_id)?.units ?? []
const complete = computed(() => lines.value.every((l) => l.item_id > 0 && l.unit_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0) && (!form.is_enabled || (lines.value.length > 0 && form.warehouse_id > 0 && form.expense_account_id > 0)))

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/inventory/amenities/plan', {
      params: { path: { propertyId } },
      body: {
        is_enabled: form.is_enabled,
        warehouse_id: form.warehouse_id || null,
        department_id: form.department_id || null,
        expense_account_id: form.expense_account_id || null,
        lines: lines.value.map((l) => ({ room_type_id: l.room_type_id || null, item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim(), basis: l.basis })),
      },
    })
    if (data) {
      plan.value = data
      fill()
      notice.value = t('inventory.amenities.saved', { version: data.version_no })
      const v = await api.GET('/api/v1/properties/{propertyId}/inventory/amenities/preview', { params: { path: { propertyId } } })
      preview.value = v.data ?? null
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

/** What one room of this type costs with two guests: the base package and the extras, at the cost of the warehouse now (the server works it out). */
const costOf = (id: number): string | null => plan.value?.cost_per_room.find((c) => c.room_type_id === id)?.cost ?? null
const base = computed(() => costOf(roomTypes.value[0]?.id ?? 0))
</script>

<template>
  <PageHeader :title="t('inventory.amenities.title')" :description="t('inventory.amenities.intro')">
    <template v-if="plan && plan.version_no > 0" #marks><Badge variant="outline" data-testid="plan-version">{{ t('inventory.amenities.version', { n: plan.version_no }) }}</Badge></template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
    <form novalidate class="flex min-w-0 flex-col gap-4" data-testid="plan-form" @submit.prevent="save">
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="plan-error" />
      <label class="flex items-center gap-2 text-sm font-medium"><input v-model="form.is_enabled" type="checkbox" name="is_enabled" :disabled="!canManage" data-testid="plan-enabled" /> {{ t('inventory.amenities.enabled') }}</label>
      <p class="muted m-0 text-sm">{{ t('inventory.amenities.rule') }}</p>
      <div class="grid gap-3 sm:grid-cols-3">
        <FormField :label="t('inventory.amenities.warehouse')" :error="fieldError('warehouse_id')" required>
          <template #default="{ id, invalid }"><NativeSelect :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :disabled="!canManage" :aria-invalid="invalid"><option :value="0">—</option><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect></template>
        </FormField>
        <FormField :label="t('inventory.issue.department')" :error="fieldError('department_id')">
          <template #default="{ id, invalid }"><NativeSelect :id="id" v-model.number="form.department_id" name="department_id" :disabled="!canManage" :aria-invalid="invalid"><option :value="0">—</option><option v-for="d in departments" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option></NativeSelect></template>
        </FormField>
        <FormField :label="t('inventory.amenities.account')" :error="fieldError('expense_account_id')" required>
          <template #default="{ id, invalid }"><NativeSelect :id="id" v-model.number="form.expense_account_id" name="expense_account_id" :disabled="!canManage" :aria-invalid="invalid"><option :value="0">—</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option></NativeSelect></template>
        </FormField>
      </div>

      <div class="flex flex-wrap gap-2" role="tablist" data-testid="type-tabs">
        <Button type="button" size="sm" role="tab" :aria-selected="typeTab === 0" :variant="typeTab === 0 ? 'default' : 'outline'" data-testid="tab-base" @click="typeTab = 0">{{ t('inventory.amenities.base') }}</Button>
        <Button v-for="rt in roomTypes" :key="rt.id" type="button" size="sm" role="tab" :aria-selected="typeTab === rt.id" :variant="typeTab === rt.id ? 'default' : 'outline'" :data-testid="`tab-type-${rt.code}`" @click="typeTab = rt.id">
          {{ rt.name }}<template v-if="lines.some((l) => l.room_type_id === rt.id)"> · {{ lines.filter((l) => l.room_type_id === rt.id).length }}</template>
        </Button>
      </div>
      <p class="muted m-0 text-sm">{{ typeTab === 0 ? t('inventory.amenities.baseHint') : t('inventory.amenities.extraHint') }}</p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm" data-testid="plan-lines">
          <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.amenities.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.unit') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.amenities.per') }}</th><th /></tr></thead>
          <tbody>
            <tr v-for="x in shown" :key="x.i" class="border-t border-border align-top" :data-testid="`plan-line-${x.i}`">
              <td class="py-2 pr-2">
                <NativeSelect v-model.number="x.l.item_id" :name="`item_${x.i}`" class="max-w-56" :disabled="!canManage" :aria-label="t('inventory.request.item')" @change="itemChanged(x.l)"><option v-for="it in items" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option></NativeSelect>
                <p v-if="lineError(x.i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(x.i, 'item_id') }}</p>
              </td>
              <td class="py-2 pr-2"><QuantityInput v-model="x.l.qty" :name="`qty_${x.i}`" class="w-24" :disabled="!canManage" :aria-label="t('inventory.amenities.qty')" /><p v-if="lineError(x.i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(x.i, 'qty') }}</p></td>
              <td class="py-2 pr-2"><NativeSelect v-model.number="x.l.unit_id" :name="`unit_${x.i}`" class="w-28" :disabled="!canManage" :aria-label="t('inventory.request.unit')"><option v-for="u in unitsOf(x.l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option></NativeSelect></td>
              <td class="py-2 pr-2"><NativeSelect v-model="x.l.basis" :name="`basis_${x.i}`" class="w-32" :disabled="!canManage" :aria-label="t('inventory.amenities.per')"><option value="PER_ROOM">{{ t('inventory.amenities.perRoom') }}</option><option value="PER_GUEST">{{ t('inventory.amenities.perGuest') }}</option></NativeSelect></td>
              <td class="py-2 text-right"><Button v-if="canManage" type="button" variant="ghost" size="sm" :data-testid="`plan-remove-${x.i}`" @click="lines.splice(x.i, 1)">{{ t('common.remove') }}</Button></td>
            </tr>
            <tr v-if="shown.length === 0"><td colspan="5" class="py-3 text-muted-foreground">{{ t('inventory.amenities.noLines') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <Button v-if="canManage" type="button" variant="outline" size="sm" data-testid="plan-add" :disabled="items.length === 0" @click="addLine">{{ t('inventory.request.addLine') }}</Button><span v-else />
        <div class="flex flex-wrap items-center gap-3"><span class="muted text-xs">{{ t('inventory.amenities.fromNextClose') }}</span><Button v-if="canManage" type="submit" :disabled="busy || !complete" data-testid="plan-save">{{ t('inventory.amenities.save') }}</Button></div>
      </div>
      <p v-if="plan?.cost_per_room.length" class="m-0 text-sm" data-testid="plan-cost">
        {{ t('inventory.amenities.costPerRoom') }}:
        <template v-for="(c, n) in plan.cost_per_room" :key="c.room_type_id"><template v-if="n > 0"> · </template>{{ c.room_type_name }} <strong>{{ formatMoney(c.cost) }}</strong></template>
        <span class="text-xs text-muted-foreground"> ({{ t('inventory.amenities.twoGuests') }})</span>
      </p>
    </form>

    <aside class="flex min-w-0 flex-col gap-4">
      <section class="rounded-xl border border-border bg-card p-4" data-testid="preview-box">
        <h2 class="m-0 text-sm font-semibold">{{ t('inventory.amenities.tonight') }}</h2>
        <template v-if="preview && preview.enabled">
          <p class="muted mt-1 text-sm">{{ t('inventory.amenities.tonightText', { rooms: preview.rooms, guests: preview.guests, value: formatMoney(preview.value) }) }}</p>
          <p v-if="preview.invalid.length" class="alert warning mt-2 text-sm" data-testid="preview-invalid">{{ preview.invalid[0] }}</p>
          <p v-if="preview.held_by_count" class="alert warning mt-2 text-sm" data-testid="preview-held">{{ t('inventory.amenities.heldByCount', { number: preview.held_by_count }) }}</p>
          <ul v-if="preview.short.length" class="m-0 mt-2 list-none p-0 text-sm" data-testid="preview-short">
            <li v-for="s in preview.short" :key="s.item_id" class="text-muted-foreground">{{ t('inventory.amenities.shortLine', { item: s.item_name, need: formatQty(s.need), have: formatQty(s.on_hand), unit: s.stock_unit_code }) }}</li>
          </ul>
        </template>
        <p v-else class="muted mt-1 text-sm">{{ t('inventory.amenities.off') }}</p>
      </section>
      <section class="rounded-xl border border-border bg-card p-4" data-testid="runs-box">
        <h2 class="m-0 text-sm font-semibold">{{ t('inventory.amenities.lastRuns') }}</h2>
        <ul class="m-0 mt-2 list-none p-0 text-sm">
          <li v-for="r in runs" :key="r.business_date" class="flex flex-wrap items-center justify-between gap-2 border-t border-border py-1.5 first:border-0">
            <RouterLink :to="`/inventory/amenities/runs/${r.business_date}`" class="text-primary hover:underline" :data-testid="`run-${r.iss_number}`">{{ r.iss_number }}</RouterLink>
            <span class="text-muted-foreground">{{ formatDate(r.business_date) }}</span><span>{{ r.held_lines > 0 ? t('inventory.issues.heldShort', { n: r.held_lines }) : formatMoney(r.total_value) }}</span>
            <Badge v-if="r.status === 'REVERSED'" variant="outline">{{ t('inventory.postedStatus.REVERSED') }}</Badge>
          </li>
          <li v-if="runs.length === 0" class="text-muted-foreground">{{ t('inventory.amenities.noRuns') }}</li>
        </ul>
        <p v-if="plan && plan.versions.length" class="muted mt-3 text-xs">{{ t('inventory.amenities.versions', { n: plan.versions.length }) }}</p>
      </section>
      <p v-if="base" class="sr-only">{{ base }}</p>
    </aside>
  </div>
</template>
