<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCategory, InventoryItem, InventoryUnit } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * An item (docs/architecture/35-inventory.md 3.2): its code, name and category, the unit its quantities are kept in, and the other units it is bought and used in, each with its
 * conversion to the stock unit (1 karton = 12 botol; 1 ml = 1/650 botol). The page fills the screen; the list it comes from has no column beside it.
 */
interface UnitRow {
  unit_id: number
  num: number
  den: number
  purchase: boolean
  recipe: boolean
  defaultPurchase: boolean
  defaultRecipe: boolean
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const itemId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => itemId.value === null)

const categories = ref<InventoryCategory[]>([])
const units = ref<InventoryUnit[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const saving = ref(false)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const hasStock = ref(false)

const form = reactive({ code: '', name: '', category_id: 0, stock_unit_id: 0, reference_cost: '', barcode: '', note: '', is_active: true, version: 0 })
const stockRow = reactive<UnitRow>({ unit_id: 0, num: 1, den: 1, purchase: true, recipe: true, defaultPurchase: true, defaultRecipe: true })
const rows = ref<UnitRow[]>([])

const unitCode = (id: number): string => units.value.find((u) => u.id === id)?.code ?? ''
const freeUnits = (own: number): InventoryUnit[] =>
  units.value.filter((u) => u.is_active && u.id !== form.stock_unit_id && (u.id === own || !rows.value.some((r) => r.unit_id === u.id)))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [c, u] = await Promise.all([api.GET('/api/v1/properties/{propertyId}/inventory/categories', path), api.GET('/api/v1/properties/{propertyId}/inventory/units', path)])
    categories.value = (c.data?.data ?? []).filter((x) => x.is_active)
    units.value = u.data?.data ?? []
    if (isNew.value) {
      Object.assign(form, { code: '', name: '', category_id: categories.value[0]?.id ?? 0, stock_unit_id: units.value.find((x) => x.is_active)?.id ?? 0, reference_cost: '', barcode: '', note: '', is_active: true, version: 0 })
      Object.assign(stockRow, { unit_id: form.stock_unit_id, num: 1, den: 1, purchase: true, recipe: true, defaultPurchase: true, defaultRecipe: true })
      rows.value = []
      hasStock.value = false
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id: itemId.value! } } })
      if (data) apply(data)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function apply(it: InventoryItem): void {
  Object.assign(form, { code: it.code, name: it.name, category_id: it.category_id, stock_unit_id: it.stock_unit_id, reference_cost: it.reference_cost ?? '', barcode: it.barcode ?? '', note: it.note ?? '', is_active: it.is_active, version: it.version })
  hasStock.value = it.has_stock
  rows.value = []
  for (const u of it.units) {
    const r: UnitRow = { unit_id: u.unit_id, num: u.stock_num, den: u.stock_den, purchase: u.is_purchase, recipe: u.is_recipe, defaultPurchase: u.is_default_purchase, defaultRecipe: u.is_default_recipe }
    if (u.unit_id === it.stock_unit_id) Object.assign(stockRow, r)
    else rows.value.push(r)
  }
}

// a new stock unit is the unit 1/1 of the item, and cannot also be one of the others
watch(() => form.stock_unit_id, (id) => {
  stockRow.unit_id = id
  rows.value = rows.value.filter((r) => r.unit_id !== id)
})

function addUnit(): void {
  const free = freeUnits(0)[0]
  if (free) rows.value.push({ unit_id: free.id, num: 1, den: 1, purchase: true, recipe: false, defaultPurchase: false, defaultRecipe: false })
}
// a default is one per kind: choosing one takes the mark from the others
function setDefault(kind: 'defaultPurchase' | 'defaultRecipe', row: UnitRow | typeof stockRow): void {
  const on = !row[kind]
  for (const r of [stockRow, ...rows.value]) r[kind] = false
  row[kind] = on
}

const unitPayload = () =>
  [stockRow, ...rows.value].map((r) => ({
    unit_id: r.unit_id, stock_num: r === stockRow ? 1 : r.num, stock_den: r === stockRow ? 1 : r.den, is_purchase: r.purchase, is_recipe: r.recipe,
    is_default_purchase: r.purchase && r.defaultPurchase, is_default_recipe: r.recipe && r.defaultRecipe,
  }))

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  formError.value = null
  const body = {
    code: form.code.trim().toUpperCase(), name: form.name.trim(), category_id: form.category_id, stock_unit_id: form.stock_unit_id,
    reference_cost: form.reference_cost.trim() || null, barcode: form.barcode.trim() || null, note: form.note.trim() || null, is_active: form.is_active, version: isNew.value ? undefined : form.version,
  }
  try {
    if (isNew.value) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId } }, body: { ...body, units: unitPayload() } })
      notice.value = t('inventory.itemForm.saved')
      if (data) await router.replace(`/inventory/items/${data.id}`)
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id: itemId.value! } }, body })
      const { data } = await api.PUT('/api/v1/properties/{propertyId}/inventory/items/{id}/units', { params: { path: { propertyId, id: itemId.value! } }, body: { units: unitPayload() } })
      if (data) apply(data)
      notice.value = t('inventory.itemForm.saved')
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch([pid, itemId], load, { immediate: true })
const ready = computed(() => form.name.trim() !== '' && form.code.trim() !== '' && form.category_id > 0 && form.stock_unit_id > 0)
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.itemForm.newTitle') : t('inventory.itemForm.editTitle', { name: form.name })" :description="t('inventory.itemForm.intro')">
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/items" data-testid="back">{{ t('inventory.itemForm.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="loaded && (categories.length === 0 || units.length === 0) && isNew" class="muted" data-testid="needs-setup">{{ t('inventory.itemForm.needCategories') }}</p>

  <form v-else-if="loaded" novalidate class="flex flex-col gap-5" data-testid="item-form" @submit.prevent="save">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="item-error" />
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <FormField :label="t('inventory.itemForm.code')" :hint="t('inventory.itemForm.codeHint')" :error="fieldError('code')" required>
        <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="30" :disabled="!isNew" :aria-invalid="invalid" class="uppercase" /></template>
      </FormField>
      <FormField class="xl:col-span-2" :label="t('inventory.itemForm.name')" :error="fieldError('name')" required>
        <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="150" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField :label="t('inventory.itemForm.category')" :hint="hasStock ? t('inventory.itemForm.stockUnitFixed') : t('inventory.itemForm.categoryHint')" :error="fieldError('category_id')" required>
        <template #default="{ id, invalid }">
          <NativeSelect :id="id" v-model.number="form.category_id" name="category_id" :disabled="hasStock" :aria-invalid="invalid">
            <option :value="0" disabled>{{ t('common.choose') }}</option>
            <option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }} ({{ c.inventory_account_code }})</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.itemForm.stockUnit')" :error="fieldError('stock_unit_id')" required>
        <template #default="{ id, invalid }">
          <NativeSelect :id="id" v-model.number="form.stock_unit_id" name="stock_unit_id" :disabled="hasStock" :aria-invalid="invalid">
            <option :value="0" disabled>{{ t('common.choose') }}</option>
            <option v-for="u in units.filter((x) => x.is_active || x.id === form.stock_unit_id)" :key="u.id" :value="u.id">{{ u.code }} · {{ u.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.itemForm.referenceCost')" :hint="t('inventory.itemForm.referenceCostHint')" :error="fieldError('reference_cost')">
        <template #default="{ id, invalid }"><MoneyInput :id="id" v-model="form.reference_cost" name="reference_cost" :decimals="6" :aria-invalid="invalid" class="max-w-40" /></template>
      </FormField>
      <FormField :label="t('inventory.itemForm.barcode')" :error="fieldError('barcode')">
        <template #default="{ id, invalid }"><Input :id="id" v-model="form.barcode" name="barcode" maxlength="40" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField class="sm:col-span-2" :label="t('inventory.itemForm.note')" :error="fieldError('note')">
        <template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template>
      </FormField>
      <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm">
        <input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('inventory.itemForm.active') }}
      </label>
    </div>

    <fieldset class="m-0 rounded-xl border border-border p-4" data-testid="units">
      <legend class="px-1 text-sm font-semibold">{{ t('inventory.itemForm.unitsTitle') }}</legend>
      <p class="m-0 mb-3 text-xs text-muted-foreground">{{ t('inventory.itemForm.unitsHint') }}</p>
      <p v-if="fieldError('units')" class="m-0 mb-2 text-sm text-destructive">{{ fieldError('units') }}</p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.itemForm.unit') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.itemForm.ratio') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.itemForm.purchase') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.itemForm.recipe') }}</th><th />
            </tr>
          </thead>
          <tbody>
            <tr class="border-t border-border" data-testid="unit-stock">
              <td class="py-2 pr-2"><strong>{{ unitCode(stockRow.unit_id) }}</strong> <span class="text-xs text-muted-foreground">({{ t('inventory.itemForm.stockUnitRow') }})</span></td>
              <td class="py-2 pr-2">1 / 1</td>
              <td class="py-2 pr-2"><input v-model="stockRow.purchase" type="checkbox" class="size-4 accent-primary" :aria-label="t('inventory.itemForm.purchase')" /> <input type="radio" :checked="stockRow.purchase && stockRow.defaultPurchase" class="ml-2 accent-primary" :disabled="!stockRow.purchase" :aria-label="t('inventory.itemForm.defaultPurchase')" @click.prevent="setDefault('defaultPurchase', stockRow)" /></td>
              <td class="py-2 pr-2"><input v-model="stockRow.recipe" type="checkbox" class="size-4 accent-primary" :aria-label="t('inventory.itemForm.recipe')" /> <input type="radio" :checked="stockRow.recipe && stockRow.defaultRecipe" class="ml-2 accent-primary" :disabled="!stockRow.recipe" :aria-label="t('inventory.itemForm.defaultRecipe')" @click.prevent="setDefault('defaultRecipe', stockRow)" /></td>
              <td />
            </tr>
            <tr v-for="(r, i) in rows" :key="i" class="border-t border-border" :data-testid="`unit-row-${i}`">
              <td class="py-2 pr-2">
                <NativeSelect v-model.number="r.unit_id" :name="`unit_${i}`" class="max-w-44" :aria-label="t('inventory.itemForm.unit')">
                  <option v-for="u in freeUnits(r.unit_id)" :key="u.id" :value="u.id">{{ u.code }} · {{ u.name }}</option>
                </NativeSelect>
              </td>
              <td class="py-2 pr-2">
                <span class="inline-flex items-center gap-1">
                  <Input v-model.number="r.num" type="number" min="1" :name="`num_${i}`" class="w-20" :aria-label="t('inventory.itemForm.ratio')" />
                  <span>/</span>
                  <Input v-model.number="r.den" type="number" min="1" :name="`den_${i}`" class="w-20" :aria-label="t('inventory.itemForm.ratioOf')" />
                  <span class="text-xs text-muted-foreground">{{ unitCode(form.stock_unit_id) }}</span>
                </span>
              </td>
              <td class="py-2 pr-2"><input v-model="r.purchase" type="checkbox" class="size-4 accent-primary" :aria-label="t('inventory.itemForm.purchase')" /> <input type="radio" :checked="r.purchase && r.defaultPurchase" class="ml-2 accent-primary" :disabled="!r.purchase" :aria-label="t('inventory.itemForm.defaultPurchase')" @click.prevent="setDefault('defaultPurchase', r)" /></td>
              <td class="py-2 pr-2"><input v-model="r.recipe" type="checkbox" class="size-4 accent-primary" :aria-label="t('inventory.itemForm.recipe')" /> <input type="radio" :checked="r.recipe && r.defaultRecipe" class="ml-2 accent-primary" :disabled="!r.recipe" :aria-label="t('inventory.itemForm.defaultRecipe')" @click.prevent="setDefault('defaultRecipe', r)" /></td>
              <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`unit-remove-${i}`" @click="rows.splice(i, 1)">{{ t('common.remove') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <Button type="button" variant="outline" size="sm" class="mt-3" data-testid="unit-add" :disabled="freeUnits(0).length === 0" @click="addUnit">{{ t('inventory.itemForm.addUnit') }}</Button>
    </fieldset>

    <div class="flex justify-end gap-2">
      <Button as-child variant="outline"><RouterLink to="/inventory/items">{{ t('common.cancel') }}</RouterLink></Button>
      <Button type="submit" :disabled="saving || !ready" data-testid="item-save">{{ t('common.save') }}</Button>
    </div>
  </form>
</template>
