<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCostTarget, InventoryItem, InventoryRecipe, InventoryRecipeLine } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'

/**
 * The recipe of a POS product (docs/architecture/35-inventory.md 3.1, 5.6, D15, mockup 27): the ingredients of one portion, in a unit of the item; a variant replaces the line of the same item, a modifier
 * adds. The cost is the server's (the average of the warehouse of the outlet, else the reference cost of the item) for the lines that are saved; a line that was just added or changed shows its cost once the recipe is
 * saved. The recipe is saved whole. Without the stock module in the plan, or without the right to manage the POS, it is shown and not edited.
 */
const props = defineProps<{ productId: number }>()

const property = usePropertyStore()
const pid = computed(() => property.currentId)

interface Row {
  key: number
  option_id: number | null
  item_id: number
  unit_id: number
  quantity: string
  saved: InventoryRecipeLine | null
}
let seq = 0
const recipe = ref<InventoryRecipe | null>(null)
const items = ref<InventoryItem[]>([])
const itemsLoaded = ref(false)
const rows = ref<Row[]>([])
const outletId = ref(0)
const loaded = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const original = ref('')
const targets = ref<InventoryCostTarget[]>([])

const fromLine = (l: InventoryRecipeLine, optionId: number | null): Row => ({ key: ++seq, option_id: optionId, item_id: l.item_id, unit_id: l.unit_id, quantity: trimZeros(l.quantity), saved: l })
const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)
const signature = (): string => JSON.stringify(rows.value.map((r) => [r.option_id, r.item_id, r.unit_id, r.quantity]))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  notice.value = ''
  formError.value = null
  if (propertyId === null) return
  try {
    const query = outletId.value ? { outlet_id: outletId.value } : {}
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/products/{id}/recipe', { params: { path: { propertyId, id: props.productId }, query } })
    recipe.value = data && Array.isArray(data.base) ? data : null
    if (!recipe.value) return
    const data2 = recipe.value
    if (data2.outlet_id !== null) outletId.value = data2.outlet_id
    rows.value = [...data2.base.map((l) => fromLine(l, null)), ...data2.options.flatMap((o) => o.lines.map((l) => fromLine(l, o.option_id)))]
    original.value = signature()
    await loadTargets()
    if (!itemsLoaded.value && data2.editable) {
      try {
        const r = await api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } })
        items.value = r.data?.items ?? []
        itemsLoaded.value = true
      } catch {
        itemsLoaded.value = false // a person who may not read the stock cannot choose items: the recipe is shown and not edited
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
/** The target cost % of the outlet and the class of the sale (docs/architecture/35-inventory.md 12.2): the outlet's own, else the property's. A person who may not read the inventory sees no target. */
async function loadTargets(): Promise<void> {
  const propertyId = pid.value
  targets.value = []
  if (propertyId === null) return
  try {
    targets.value = (await api.GET('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId } } })).data?.data ?? []
  } catch {
    targets.value = []
  }
}
const targetPct = computed<string | null>(() => {
  const c = recipe.value?.sale_class
  if (c !== 'FOOD' && c !== 'BEVERAGE') return null
  const own = targets.value.find((x) => x.outlet_id === recipe.value?.outlet_id && x.class === c)
  return (own ?? targets.value.find((x) => x.outlet_id === null && x.class === c))?.target_pct ?? null
})
const overTarget = computed(() => !dirty.value && targetPct.value !== null && recipe.value?.food_cost_pct != null && Number(recipe.value.food_cost_pct) > Number(targetPct.value))
watch([pid, () => props.productId], () => { outletId.value = 0; itemsLoaded.value = false; void load() }, { immediate: true })

const editable = computed(() => !!recipe.value?.editable && itemsLoaded.value)
const dirty = computed(() => signature() !== original.value)
const itemOf = (id: number): InventoryItem | undefined => items.value.find((i) => i.id === id)
const unitsOf = (itemId: number) => {
  const it = itemOf(itemId)
  return (it?.units ?? []).filter((u) => u.is_recipe || u.unit_id === it?.stock_unit_id)
}
const rowsOf = (optionId: number | null): Row[] => rows.value.filter((r) => r.option_id === optionId)

function addRow(optionId: number | null): void {
  const used = new Set(rowsOf(optionId).map((r) => r.item_id))
  const it = items.value.find((i) => !used.has(i.id))
  if (!it) return
  const u = it.units.find((x) => x.is_default_recipe) ?? it.units[0]
  rows.value.push({ key: ++seq, option_id: optionId, item_id: it.id, unit_id: u?.unit_id ?? it.stock_unit_id, quantity: '', saved: null })
}
function removeRow(key: number): void {
  rows.value = rows.value.filter((r) => r.key !== key)
}
function changeItem(r: Row): void {
  const it = itemOf(r.item_id)
  r.unit_id = (it?.units.find((x) => x.is_default_recipe) ?? it?.units[0])?.unit_id ?? it?.stock_unit_id ?? 0
  r.saved = null
}
const costOf = (r: Row): string | null => (r.saved && !changed(r) ? r.saved.cost : null)
function changed(r: Row): boolean {
  return !r.saved || r.saved.item_id !== r.item_id || r.saved.unit_id !== r.unit_id || trimZeros(r.saved.quantity) !== r.quantity
}
const lineOf = (r: Row): InventoryRecipeLine | null => r.saved
const valid = computed(() => rows.value.every((r) => r.item_id > 0 && r.unit_id > 0 && isDecimal(r.quantity, 4) && Number(r.quantity) > 0))

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !valid.value) return
  saving.value = true
  formError.value = null
  notice.value = ''
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/pos/products/{id}/recipe', {
      params: { path: { propertyId, id: props.productId } },
      body: { lines: rows.value.map((r) => ({ option_id: r.option_id, item_id: r.item_id, unit_id: r.unit_id, quantity: r.quantity })) },
    })
    if (data) {
      await load()
      notice.value = t('pos.recipe.saved')
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
const fieldIssue = (i: number): string | undefined => formError.value?.fieldMessage(`lines[${i}].item_id`) ?? formError.value?.fieldMessage(`lines[${i}].unit_id`) ?? formError.value?.fieldMessage(`lines[${i}].quantity`)
const indexOf = (r: Row): number => rows.value.indexOf(r)
const money = (v: string | null | undefined): string => (v === null || v === undefined ? '—' : formatMoney(v))
const basisKey = (b: string): string => `pos.recipe.basis.${b}`
const className = (c: string): string => t(`pos.recipe.saleClass.${c}`)
const plus = (v: string): string => (Number(v) > 0 ? '+' : '') + formatMoney(v)
</script>

<template>
  <section class="rounded-xl border border-border bg-card p-4" data-testid="product-recipe" @keydown.enter.prevent>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="m-0 text-base font-semibold">{{ t('pos.recipe.title') }}</h3>
      <label v-if="recipe && recipe.outlets.length > 1" class="flex items-center gap-2 text-sm">
        <span class="text-muted-foreground">{{ t('pos.recipe.costAt') }}</span>
        <NativeSelect v-model.number="outletId" name="recipe_outlet" class="w-56" :aria-label="t('pos.recipe.costAt')" data-testid="recipe-outlet" @update:model-value="load">
          <option v-for="o in recipe.outlets" :key="o.id" :value="o.id">{{ o.name }}{{ o.warehouse_name ? ` · ${o.warehouse_name}` : ` · ${t('pos.recipe.noWarehouse')}` }}</option>
        </NativeSelect>
      </label>
      <span v-else-if="recipe?.warehouse_name" class="text-sm text-muted-foreground" data-testid="recipe-warehouse">{{ t('pos.recipe.costAtWarehouse', { warehouse: recipe.warehouse_name, outlet: recipe.outlet_name ?? '' }) }}</span>
    </div>
    <ErrorNotice v-if="error" :error="error" inline />
    <p v-if="notice" class="notice mt-2" role="status" data-testid="recipe-notice">{{ notice }}</p>
    <p v-if="loaded && recipe && !recipe.inventory_active" class="alert warning mt-2 text-sm" data-testid="recipe-readonly">{{ t('pos.recipe.moduleOff') }}</p>
    <p v-else-if="loaded && recipe && recipe.warehouse_id === null" class="alert warning mt-2 text-sm" data-testid="recipe-no-warehouse">{{ t('pos.recipe.outletWithoutWarehouse') }}</p>
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="recipe-error" />

    <template v-if="loaded && recipe">
      <!-- the base recipe -->
      <h4 class="m-0 mt-4 text-sm font-semibold">{{ t('pos.recipe.base') }}</h4>
      <div class="overflow-x-auto">
        <table class="mt-1 w-full min-w-[40rem] text-sm" data-testid="recipe-base">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('pos.recipe.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('pos.recipe.quantity') }}</th><th class="py-1 pr-2 font-medium">{{ t('pos.recipe.unit') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('pos.recipe.unitCost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('pos.recipe.cost') }}</th><th class="w-16" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rowsOf(null)" :key="r.key" class="border-t border-border align-top" :data-testid="`recipe-line-${itemOf(r.item_id)?.code ?? lineOf(r)?.item_code}`">
              <td class="py-2 pr-2">
                <NativeSelect v-if="editable" v-model.number="r.item_id" :name="`item_${indexOf(r)}`" class="max-w-56" :aria-label="t('pos.recipe.item')" @update:model-value="changeItem(r)">
                  <option v-for="i in items" :key="i.id" :value="i.id">{{ i.code }} · {{ i.name }}</option>
                </NativeSelect>
                <template v-else>{{ lineOf(r)?.item_name }} <span class="text-xs text-muted-foreground">{{ lineOf(r)?.item_code }}</span></template>
                <p v-if="fieldIssue(indexOf(r))" class="m-0 mt-1 text-xs text-destructive">{{ fieldIssue(indexOf(r)) }}</p>
              </td>
              <td class="py-2 pr-2 text-right">
                <QuantityInput v-if="editable" v-model="r.quantity" :name="`quantity_${indexOf(r)}`" :decimals="4" class="w-24" :aria-label="t('pos.recipe.quantity')" />
                <template v-else>{{ formatQty(r.quantity) }}</template>
              </td>
              <td class="py-2 pr-2">
                <NativeSelect v-if="editable" v-model.number="r.unit_id" :name="`unit_${indexOf(r)}`" class="w-24" :aria-label="t('pos.recipe.unit')" @update:model-value="r.saved = null">
                  <option v-for="u in unitsOf(r.item_id)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option>
                </NativeSelect>
                <template v-else>{{ lineOf(r)?.unit_code }}</template>
              </td>
              <td class="py-2 pr-2 text-right text-xs text-muted-foreground">
                <template v-if="lineOf(r) && !changed(r)">{{ money(lineOf(r)!.unit_cost) }} / {{ lineOf(r)!.stock_unit_code }} <Badge v-if="lineOf(r)!.cost_basis === 'REFERENCE'" variant="outline">{{ t(basisKey('REFERENCE')) }}</Badge></template>
              </td>
              <td class="py-2 pr-2 text-right" :data-testid="`recipe-cost-${indexOf(r)}`">{{ money(costOf(r)) }}</td>
              <td class="py-2 text-right"><Button v-if="editable" type="button" variant="ghost" size="sm" @click="removeRow(r.key)">{{ t('common.remove') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="rowsOf(null).length === 0" class="m-0 mt-1 text-sm text-muted-foreground">{{ t('pos.recipe.empty') }}</p>
      <Button v-if="editable" type="button" variant="outline" size="sm" class="mt-2" data-testid="recipe-add-base" @click="addRow(null)">{{ t('pos.recipe.addItem') }}</Button>

      <!-- the variants and the modifiers -->
      <template v-if="recipe.options.length">
        <h4 class="m-0 mt-5 text-sm font-semibold">{{ t('pos.recipe.options') }}</h4>
        <p class="m-0 text-xs text-muted-foreground">{{ t('pos.recipe.optionsNote') }}</p>
        <div v-for="o in recipe.options" :key="o.option_id" class="mt-3 rounded-lg border border-border p-3" :data-testid="`recipe-option-${o.option_id}`">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div><Badge variant="outline">{{ t(`pos.recipe.kind.${o.kind}`) }}</Badge> <strong class="ml-1 text-sm">{{ o.name }}</strong> <span class="text-xs text-muted-foreground">{{ o.group_name }}</span></div>
            <div class="text-xs text-muted-foreground">
              <span v-if="o.lines.length" :data-testid="`recipe-extra-${o.option_id}`">{{ t('pos.recipe.extraCost') }} {{ plus(o.extra_cost) }}</span>
              <span v-if="o.price_delta !== null && Number(o.price_delta) !== 0"> · {{ t('pos.recipe.extraPrice') }} {{ plus(o.price_delta) }}</span>
            </div>
          </div>
          <p v-if="rowsOf(o.option_id).length === 0" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('pos.recipe.noStockItems') }}</p>
          <table v-else class="mt-1 w-full min-w-[36rem] text-sm">
            <tbody>
              <tr v-for="r in rowsOf(o.option_id)" :key="r.key" class="border-t border-border align-top">
                <td class="py-2 pr-2">
                  <NativeSelect v-if="editable" v-model.number="r.item_id" :name="`item_${indexOf(r)}`" class="max-w-56" :aria-label="t('pos.recipe.item')" @update:model-value="changeItem(r)">
                    <option v-for="i in items" :key="i.id" :value="i.id">{{ i.code }} · {{ i.name }}</option>
                  </NativeSelect>
                  <template v-else>{{ lineOf(r)?.item_name }}</template>
                  <p v-if="lineOf(r)?.replaces_label && !changed(r)" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('pos.recipe.replaces', { item: lineOf(r)!.replaces_label!, qty: formatQty(lineOf(r)!.replaced_qty), unit: lineOf(r)!.unit_code }) }}</p>
                  <p v-if="fieldIssue(indexOf(r))" class="m-0 mt-1 text-xs text-destructive">{{ fieldIssue(indexOf(r)) }}</p>
                </td>
                <td class="py-2 pr-2 text-right">
                  <QuantityInput v-if="editable" v-model="r.quantity" :name="`quantity_${indexOf(r)}`" :decimals="4" class="w-24" :aria-label="t('pos.recipe.quantity')" />
                  <template v-else>{{ formatQty(r.quantity) }}</template>
                </td>
                <td class="py-2 pr-2">
                  <NativeSelect v-if="editable" v-model.number="r.unit_id" :name="`unit_${indexOf(r)}`" class="w-24" :aria-label="t('pos.recipe.unit')" @update:model-value="r.saved = null">
                    <option v-for="u in unitsOf(r.item_id)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option>
                  </NativeSelect>
                  <template v-else>{{ lineOf(r)?.unit_code }}</template>
                </td>
                <td class="py-2 pr-2 text-right">{{ money(costOf(r)) }}</td>
                <td class="py-2 text-right"><Button v-if="editable" type="button" variant="ghost" size="sm" @click="removeRow(r.key)">{{ t('common.remove') }}</Button></td>
              </tr>
            </tbody>
          </table>
          <Button v-if="editable" type="button" variant="outline" size="sm" class="mt-2" :data-testid="`recipe-add-option-${o.option_id}`" @click="addRow(o.option_id)">{{ t('pos.recipe.addItem') }}</Button>
        </div>
      </template>

      <!-- cost, price, margin -->
      <dl class="m-0 mt-5 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 border-t border-border pt-3 text-sm sm:max-w-md" data-testid="recipe-summary">
        <dt class="text-muted-foreground">{{ t('pos.recipe.portionCost') }}</dt><dd class="m-0 text-right font-semibold" data-testid="recipe-cost">{{ dirty ? '—' : money(recipe.base_cost) }}</dd>
        <dt class="text-muted-foreground">{{ t('pos.recipe.netPrice') }}</dt><dd class="m-0 text-right" data-testid="recipe-price">{{ money(recipe.net_price) }}</dd>
        <dt class="text-muted-foreground">{{ t('pos.recipe.margin') }}</dt><dd class="m-0 text-right" data-testid="recipe-margin">{{ dirty ? '—' : money(recipe.margin) }}</dd>
        <dt class="text-muted-foreground">{{ t('pos.recipe.foodCost') }}</dt><dd class="m-0 text-right font-semibold" data-testid="recipe-foodcost">{{ dirty || recipe.food_cost_pct === null ? '—' : `${formatQty(recipe.food_cost_pct)}%` }}</dd>
        <template v-if="targetPct !== null">
          <dt class="text-muted-foreground">{{ t('inventory.targets.recipe') }}</dt>
          <dd class="m-0 text-right" :class="overTarget ? 'font-semibold text-destructive' : ''" data-testid="recipe-target"><span v-if="overTarget" :title="t('inventory.targets.above')">▲ </span>{{ formatQty(targetPct) }}%</dd>
        </template>
      </dl>
      <ul class="m-0 mt-3 list-none p-0 text-xs text-muted-foreground">
        <li v-if="recipe.warehouse_name">{{ t('pos.recipe.noteDeduct', { warehouse: recipe.warehouse_name }) }}</li>
        <li v-if="recipe.sale_class">{{ t('pos.recipe.noteClass', { class: className(recipe.sale_class) }) }}</li>
        <li>{{ t('pos.recipe.noteCost') }}</li>
        <li>{{ t('pos.recipe.noteAfter') }}</li>
      </ul>
      <div v-if="editable" class="mt-4 flex flex-wrap justify-end gap-2">
        <Button type="button" variant="outline" :disabled="saving || !dirty" data-testid="recipe-cancel" @click="load">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="saving || !dirty || !valid" data-testid="recipe-save" @click="save">{{ t('pos.recipe.save') }}</Button>
      </div>
    </template>
  </section>
</template>

