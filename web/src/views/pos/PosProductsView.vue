<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosCategory, PosImportResult, PosOutlet, PosProduct, PosRuleProfile, PosStation } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import ProductRecipe from '@/components/pos/ProductRecipe.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/**
 * The products of the outlets (docs/architecture/34-pos.md 3.2, mockup 15): the charge code decides the revenue account and the department, the price is per outlet, variants are "pick one"
 * with a price difference and add-ons are priced or not with a limit; the tax and service follow the outlet unless the product names a profile. The form is saved whole.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const decimals = computed(() => property.current?.currency_decimals ?? 0)

const products = ref<PosProduct[]>([])
const categories = ref<PosCategory[]>([])
const outlets = ref<PosOutlet[]>([])
const stations = ref<PosStation[]>([])
const profiles = ref<PosRuleProfile[]>([])
const chargeCodes = ref<{ id: number; code: string; name: string; charge_type: string; is_active: boolean }[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const query = ref('')
const listOutlet = ref(0)

const selectedId = ref<number | null>(null) // null with `creating`: a new product
const creating = ref(false)

/** `id` is the id of a group or a choice that exists already: it travels with the form so that the choice stays the same choice (and keeps its recipe) whatever its name or price becomes. */
interface OptionRow { id?: number; name: string; price_delta: string; is_default: boolean; max_qty: number }
interface GroupRow { id?: number; name: string; kind: 'VARIANT' | 'MODIFIER'; required: boolean; max_select: number; options: OptionRow[] }
interface PriceRow { outletId: number; listId: number; sold: boolean; price: string; shared: boolean }
const form = reactive({
  version: 0, code: '', name: '', short_name: '', category_id: 0, charge_code_id: 0, station_id: 0, rule_profile_id: 0, own_profile: false, is_active: true,
  groups: [] as GroupRow[], prices: [] as PriceRow[],
})
const saving = ref(false)
const formError = ref<ApiError | null>(null)
const fieldError = (f: string) => formError.value?.fieldMessage(f)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [c, o, s, pr, cc] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/categories', path),
      api.GET('/api/v1/properties/{propertyId}/pos/outlets', path),
      api.GET('/api/v1/properties/{propertyId}/pos/stations', path),
      api.GET('/api/v1/properties/{propertyId}/pos/rule-profiles', path),
      api.GET('/api/v1/properties/{propertyId}/charge-codes', path),
    ])
    categories.value = c.data?.data ?? []
    outlets.value = o.data?.data ?? []
    if (!outlets.value.some((x) => x.id === listOutlet.value && x.is_active)) listOutlet.value = outlets.value.find((x) => x.is_active)?.id ?? 0
    await loadProducts()
    stations.value = s.data?.data ?? []
    profiles.value = pr.data?.data ?? []
    chargeCodes.value = ((cc.data?.data ?? []) as typeof chargeCodes.value).filter((x) => x.is_active && x.charge_type !== 'ROOM' && x.charge_type !== 'TRANSFER')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

/** The products, with the price they have at the outlet the list is read for. */
async function loadProducts(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const query = listOutlet.value ? { outlet_id: listOutlet.value } : {}
  const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/products', { params: { path: { propertyId }, query } })
  products.value = data?.data ?? []
}

const activeOutlets = computed(() => outlets.value.filter((o) => o.is_active))
const grouped = computed(() => {
  const q = query.value.trim().toLowerCase()
  const hit = (p: PosProduct) => !q || p.name.toLowerCase().includes(q) || p.code.toLowerCase().includes(q)
  return categories.value
    .map((c) => ({ category: c, items: products.value.filter((p) => p.category_id === c.id && hit(p)) }))
    .filter((g) => g.items.length > 0)
})

function blankPrices(prices: { price_list_id: number; price: string; is_sold: boolean }[] = []): PriceRow[] {
  const seen = new Set<number>()
  return activeOutlets.value.map((o) => {
    const it = prices.find((p) => p.price_list_id === o.price_list_id)
    const shared = seen.has(o.price_list_id)
    seen.add(o.price_list_id)
    return { outletId: o.id, listId: o.price_list_id, sold: it?.is_sold ?? false, price: it ? stripZeros(it.price) : '', shared }
  })
}
const stripZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

async function select(p: PosProduct): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  formError.value = null
  notice.value = ''
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/products/{id}', { params: { path: { propertyId, id: p.id } } })
    if (!data) return
    selectedId.value = data.id
    creating.value = false
    Object.assign(form, {
      version: data.version, code: data.code, name: data.name, short_name: data.short_name ?? '', category_id: data.category_id, charge_code_id: data.charge_code_id,
      station_id: data.station_id ?? 0, rule_profile_id: data.rule_profile_id ?? 0, own_profile: data.rule_profile_id !== null && data.rule_profile_id !== undefined, is_active: data.is_active,
      groups: (data.option_groups ?? []).map((g) => ({
        id: g.id, name: g.name, kind: g.kind, required: g.min_select >= 1, max_select: g.max_select,
        options: g.options.map((o) => ({ id: o.id, name: o.name, price_delta: stripZeros(o.price_delta), is_default: o.is_default, max_qty: o.max_qty })),
      })),
      prices: blankPrices(data.prices ?? []),
    })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startNew(): void {
  selectedId.value = null
  creating.value = true
  formError.value = null
  notice.value = ''
  Object.assign(form, {
    version: 0, code: '', name: '', short_name: '', category_id: categories.value.find((c) => c.is_active)?.id ?? 0, charge_code_id: 0, station_id: 0, rule_profile_id: 0,
    own_profile: false, is_active: true, groups: [], prices: blankPrices(),
  })
}

const editing = computed(() => creating.value || selectedId.value !== null)

function addGroup(kind: 'VARIANT' | 'MODIFIER'): void {
  form.groups.push({ name: '', kind, required: kind === 'VARIANT', max_select: kind === 'VARIANT' ? 1 : 3, options: [{ name: '', price_delta: '0', is_default: false, max_qty: 1 }] })
}
const addOption = (g: GroupRow): void => { g.options.push({ name: '', price_delta: '0', is_default: false, max_qty: 1 }) }

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  formError.value = null
  const lists = new Map<number, PriceRow>()
  for (const r of form.prices) if (!r.shared && r.price.trim() !== '') lists.set(r.listId, r)
  const body = {
    code: form.code.trim(), name: form.name.trim(), short_name: form.short_name.trim() || null, category_id: form.category_id, charge_code_id: form.charge_code_id,
    station_id: form.station_id || null, rule_profile_id: form.own_profile && form.rule_profile_id ? form.rule_profile_id : null, is_active: form.is_active,
    version: creating.value ? undefined : form.version,
    option_groups: form.groups.map((g, gi) => ({
      id: g.id, name: g.name.trim(), kind: g.kind, min_select: g.kind === 'VARIANT' ? (g.required ? 1 : 0) : g.required ? 1 : 0, max_select: g.kind === 'VARIANT' ? 1 : g.max_select, sort_order: gi,
      options: g.options.map((o, oi) => ({ id: o.id, name: o.name.trim(), price_delta: o.price_delta.trim() || '0', is_default: o.is_default, max_qty: o.max_qty, sort_order: oi, is_active: true })),
    })),
    prices: [...lists.values()].map((r) => ({ price_list_id: r.listId, price: r.price.trim(), is_sold: r.sold })),
  }
  try {
    if (creating.value) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/products', { params: { path: { propertyId } }, body })
      await load()
      if (data) await select(data as PosProduct)
    } else if (selectedId.value !== null) {
      await api.PUT('/api/v1/properties/{propertyId}/pos/products/{id}', { params: { path: { propertyId, id: selectedId.value } }, body })
      const keep = selectedId.value
      await load()
      const again = products.value.find((p) => p.id === keep)
      if (again) await select(again)
    }
    notice.value = t('pos.products.saved')
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function deactivate(): Promise<void> {
  form.is_active = !form.is_active
  await save()
}

// ---- CSV
const csvOpen = ref(false)
const csvText = ref('')
const csvResult = ref<PosImportResult | null>(null)
const csvError = ref<ApiError | null>(null)
const csvBusy = ref(false)
async function csvFile(e: Event): Promise<void> {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (f) csvText.value = await f.text()
}
async function runCsv(dry: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  csvBusy.value = true
  csvError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/products/import', { params: { path: { propertyId }, query: { dry_run: dry } }, body: { csv: csvText.value } })
    csvResult.value = data ?? null
    if (!dry && data && data.errors.length === 0) {
      notice.value = t('pos.products.imported', { n: data.rows })
      csvOpen.value = false
      await load()
    }
  } catch (e) {
    csvError.value = e instanceof ApiError ? e : null
  } finally {
    csvBusy.value = false
  }
}
const shownPrice = (v: string): string => formatMoney(v.includes('.') ? v.replace(/\.?0+$/, '') : v)
const outletName = (id: number) => outlets.value.find((o) => o.id === id)?.name ?? ''
const profileOf = (o: PosOutlet) => profiles.value.find((p) => p.id === o.rule_profile_id)
const followText = computed(() =>
  activeOutlets.value.map((o) => {
    const p = profileOf(o)
    return `${o.name}: ${p ? p.rules.map((r) => `${r.name} ${r.rate}%`).join(', ') || t('pos.profiles.noRules') : ''}`
  }).join(' · '),
)
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('pos.products.title')">
    <template #actions>
      <Button variant="outline" data-testid="csv-open" @click="csvOpen = true; csvResult = null">{{ t('pos.products.importCsv') }}</Button>
      <Button data-testid="product-new" @click="startNew">{{ t('pos.products.new') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="grid items-start gap-4 lg:grid-cols-[minmax(0,320px)_minmax(0,1fr)]">
    <!-- the list -->
    <section class="rounded-xl border border-border bg-card" data-testid="product-list">
      <div class="flex flex-col gap-2 border-b border-border p-3">
        <Input v-model="query" name="search" type="search" :placeholder="t('pos.products.search')" class="h-11" data-testid="product-search" />
        <NativeSelect v-if="activeOutlets.length > 1" v-model.number="listOutlet" name="list_outlet" :aria-label="t('pos.products.pricesOf')" data-testid="list-outlet" @update:model-value="loadProducts">
          <option v-for="o in activeOutlets" :key="o.id" :value="o.id">{{ t('pos.products.pricesAt', { outlet: o.name }) }}</option>
        </NativeSelect>
      </div>
      <p v-if="products.length === 0" class="m-0 p-4 text-sm text-muted-foreground" data-testid="products-empty">{{ t('pos.products.empty') }}</p>
      <div v-for="g in grouped" :key="g.category.id">
        <h3 class="m-0 bg-muted/40 px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ g.category.name }} · {{ g.items.length }}</h3>
        <ul class="m-0 list-none p-0">
          <li v-for="p in g.items" :key="p.id">
            <button
              type="button" class="flex min-h-11 w-full items-center justify-between gap-2 border-0 border-t border-solid border-border bg-transparent px-4 py-2 text-left text-sm hover:bg-accent"
              :class="selectedId === p.id && 'bg-accent font-semibold'" :data-testid="`product-${p.code}`" @click="select(p)"
            >
              <span class="min-w-0 truncate">{{ p.name }}<Badge v-if="!p.is_active" variant="outline" class="ml-2">{{ t('common.inactive') }}</Badge></span>
              <span v-if="p.price" class="shrink-0 tabular-nums">{{ shownPrice(p.price) }}</span>
              <span v-else-if="p.is_sold === false" class="shrink-0 text-xs text-muted-foreground">{{ t('pos.products.notSold') }}</span>
            </button>
          </li>
        </ul>
      </div>
    </section>

    <!-- the form -->
    <form v-if="editing" class="flex min-w-0 flex-col gap-4" novalidate data-testid="product-form" @submit.prevent="save">
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="product-error" />
      <div class="grid items-start gap-4 xl:grid-cols-2">
        <section class="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
          <h2 class="m-0 text-base font-semibold">{{ creating ? t('pos.products.new') : form.name }}</h2>
          <div class="grid gap-3 sm:grid-cols-2">
            <FormField :label="t('pos.products.name')" :error="fieldError('name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="100" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('pos.products.code')" :hint="creating ? undefined : t('pos.products.codeFixed')" :error="fieldError('code')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="30" :disabled="!creating" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('pos.products.shortName')" :hint="t('pos.products.shortNameHint')" :error="fieldError('short_name')">
              <template #default="{ id }"><Input :id="id" v-model="form.short_name" name="short_name" maxlength="24" /></template>
            </FormField>
            <FormField :label="t('pos.products.category')" :error="fieldError('category_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="form.category_id" name="category_id" :aria-invalid="invalid">
                  <option :value="0" disabled>{{ t('common.choose') }}</option>
                  <option v-for="c in categories.filter((x) => x.is_active)" :key="c.id" :value="c.id">{{ c.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('pos.products.chargeCode')" :hint="t('pos.products.chargeCodeHint')" :error="fieldError('charge_code_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="form.charge_code_id" name="charge_code_id" :aria-invalid="invalid">
                  <option :value="0" disabled>{{ t('common.choose') }}</option>
                  <option v-for="c in chargeCodes" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('pos.products.station')" :error="fieldError('station_id')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="form.station_id" name="station_id">
                  <option :value="0">{{ t('pos.products.noStation') }}</option>
                  <option v-for="s in stations.filter((x) => x.is_active)" :key="s.id" :value="s.id">{{ s.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
          </div>
        </section>

        <section class="rounded-xl border border-border bg-card p-4" data-testid="product-prices">
          <h3 class="m-0 text-base font-semibold">{{ t('pos.products.pricePerOutlet') }}</h3>
          <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('pos.products.pricePerOutletHint') }}</p>
          <p v-if="form.prices.length === 0" class="muted">{{ t('pos.products.noOutlets') }}</p>
          <table v-else class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs uppercase text-muted-foreground"><th class="py-1 pr-2">{{ t('pos.products.outlet') }}</th><th class="py-1 pr-2">{{ t('pos.products.sold') }}</th><th class="py-1">{{ t('pos.products.price') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in form.prices" :key="r.outletId" class="border-t border-border" :data-testid="`price-row-${r.outletId}`">
                <td class="py-1.5 pr-2">{{ outletName(r.outletId) }}<span v-if="r.shared" class="block text-xs text-muted-foreground">{{ t('pos.products.sharedList') }}</span></td>
                <td class="py-1.5 pr-2"><input v-model="r.sold" type="checkbox" class="size-4 accent-primary" :disabled="r.shared" :aria-label="t('pos.products.sold')" :name="`sold_${r.outletId}`" /></td>
                <td class="py-1.5"><MoneyInput v-model="r.price" :decimals="decimals" :name="`price_${r.outletId}`" class="h-10 max-w-40" :disabled="r.shared || !r.sold" :aria-label="t('pos.products.price')" /></td>
              </tr>
            </tbody>
          </table>
        </section>

        <section v-for="kind in (['VARIANT', 'MODIFIER'] as const)" :key="kind" class="rounded-xl border border-border bg-card p-4" :data-testid="kind === 'VARIANT' ? 'product-variants' : 'product-modifiers'">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h3 class="m-0 text-base font-semibold">{{ kind === 'VARIANT' ? t('pos.products.variants') : t('pos.products.modifiers') }}</h3>
            <Button type="button" variant="outline" size="sm" :data-testid="kind === 'VARIANT' ? 'add-variant' : 'add-modifier'" @click="addGroup(kind)">
              {{ kind === 'VARIANT' ? t('pos.products.addVariant') : t('pos.products.addModifier') }}
            </Button>
          </div>
          <p v-if="!form.groups.some((g) => g.kind === kind)" class="muted">{{ kind === 'VARIANT' ? t('pos.products.noVariants') : t('pos.products.noModifiers') }}</p>
          <template v-for="(g, gi) in form.groups" :key="gi">
            <div v-if="g.kind === kind" class="mt-3 rounded-lg border border-border p-3" :data-testid="`group-${gi}`">
              <div class="flex flex-wrap items-center gap-2">
                <Input v-model="g.name" :name="`group_name_${gi}`" maxlength="100" class="h-10 min-w-0 flex-1 basis-32" :placeholder="t('pos.products.groupName')" :aria-label="t('pos.products.groupName')" />
                <Badge variant="outline">{{ g.kind === 'VARIANT' ? t('pos.products.variantKind') : t('pos.products.modifierKind') }}</Badge>
                <label class="inline-flex min-h-10 items-center gap-1.5 text-sm"><input v-model="g.required" type="checkbox" class="size-4 accent-primary" />{{ t('pos.products.required') }}</label>
                <label v-if="g.kind === 'MODIFIER'" class="inline-flex min-h-10 items-center gap-1.5 text-sm">
                  {{ t('pos.products.maxSelect') }}
                  <Input v-model.number="g.max_select" type="number" min="1" max="20" class="h-10 w-16" :name="`group_max_${gi}`" />
                </label>
                <Button type="button" variant="ghost" size="sm" :aria-label="t('common.remove')" @click="form.groups.splice(gi, 1)">✕</Button>
              </div>
              <p v-if="fieldError(`option_groups[${gi}].max_select`) || fieldError(`option_groups[${gi}].options`)" class="m-0 mt-1 text-xs text-destructive" role="alert">
                {{ fieldError(`option_groups[${gi}].max_select`) || fieldError(`option_groups[${gi}].options`) }}
              </p>
              <ul class="m-0 mt-2 list-none space-y-1.5 p-0">
                <li v-for="(o, oi) in g.options" :key="oi" class="flex flex-wrap items-center gap-2">
                  <Input v-model="o.name" :name="`option_name_${gi}_${oi}`" maxlength="100" class="h-10 min-w-0 flex-1 basis-28" :placeholder="t('pos.products.optionName')" :aria-label="t('pos.products.optionName')" />
                  <MoneyInput v-model="o.price_delta" :decimals="decimals" :name="`option_price_${gi}_${oi}`" class="h-10 w-28" :aria-label="t('pos.products.priceDelta')" />
                  <label v-if="g.kind === 'VARIANT'" class="inline-flex items-center gap-1 text-xs"><input v-model="o.is_default" type="checkbox" class="accent-primary" />{{ t('pos.products.default') }}</label>
                  <Button type="button" variant="ghost" size="sm" :aria-label="t('common.remove')" @click="g.options.splice(oi, 1)">✕</Button>
                  <span v-if="fieldError(`option_groups[${gi}].options[${oi}].price_delta`) || fieldError(`option_groups[${gi}].options[${oi}].name`)" class="w-full text-xs text-destructive">
                    {{ fieldError(`option_groups[${gi}].options[${oi}].price_delta`) || fieldError(`option_groups[${gi}].options[${oi}].name`) }}
                  </span>
                </li>
              </ul>
              <Button type="button" variant="outline" size="sm" class="mt-2" @click="addOption(g)">{{ t('pos.products.addOption') }}</Button>
            </div>
          </template>
        </section>

        <section class="rounded-xl border border-border bg-card p-4" data-testid="product-tax">
          <h3 class="m-0 text-base font-semibold">{{ t('pos.products.taxService') }}</h3>
          <label class="mt-2 flex min-h-11 cursor-pointer items-start gap-2 text-sm">
            <input v-model="form.own_profile" type="radio" :value="false" name="own_profile" class="mt-1 accent-primary" data-testid="tax-follow" />
            <span><strong class="block">{{ t('pos.products.followOutlet') }}</strong><span class="text-xs text-muted-foreground">{{ followText }}</span></span>
          </label>
          <label class="flex min-h-11 cursor-pointer items-start gap-2 text-sm">
            <input v-model="form.own_profile" type="radio" :value="true" name="own_profile" class="mt-1 accent-primary" data-testid="tax-own" />
            <span><strong class="block">{{ t('pos.products.ownProfile') }}</strong><span class="text-xs text-muted-foreground">{{ t('pos.products.ownProfileHint') }}</span></span>
          </label>
          <NativeSelect v-if="form.own_profile" v-model.number="form.rule_profile_id" name="rule_profile_id" class="mt-2 max-w-sm" :aria-label="t('pos.products.profile')" data-testid="tax-profile">
            <option :value="0" disabled>{{ t('common.choose') }}</option>
            <option v-for="p in profiles.filter((x) => x.is_active)" :key="p.id" :value="p.id">{{ p.name }}</option>
          </NativeSelect>
        </section>

        <ProductRecipe v-if="!creating && selectedId" :key="selectedId" class="xl:col-span-2" :product-id="selectedId" />
        <section v-else class="rounded-xl border border-dashed border-border bg-card p-4" data-testid="product-recipe-later">
          <h3 class="m-0 text-base font-semibold">{{ t('pos.recipe.title') }}</h3>
          <p class="m-0 mt-1 text-sm text-muted-foreground">{{ t('pos.recipe.saveFirst') }}</p>
        </section>
      </div>

      <div class="sticky bottom-0 -mx-1 flex flex-wrap items-center justify-between gap-2 border-t border-border bg-background/95 px-1 py-3">
        <Button v-if="!creating" type="button" variant="outline" :disabled="saving" data-testid="product-toggle" @click="deactivate">{{ form.is_active ? t('pos.products.deactivate') : t('pos.products.activate') }}</Button>
        <span v-else />
        <Button type="submit" :disabled="saving || !form.name.trim() || !form.code.trim() || !form.category_id || !form.charge_code_id" data-testid="product-save">{{ t('common.save') }}</Button>
      </div>
    </form>
    <p v-else class="m-0 rounded-xl border border-dashed border-border p-6 text-sm text-muted-foreground" data-testid="product-hint">{{ t('pos.products.chooseOne') }}</p>
  </div>

  <Dialog :open="csvOpen" @update:open="(v) => (csvOpen = v)">
    <DialogContent class="max-h-[90vh] max-w-2xl overflow-y-auto p-5" data-testid="csv-dialog" @interact-outside.prevent>
      <DialogTitle>{{ t('pos.products.importCsv') }}</DialogTitle>
      <DialogDescription>{{ t('pos.products.csvHint') }}</DialogDescription>
      <ErrorNotice v-if="csvError" :error="csvError" inline data-testid="csv-error" />
      <input type="file" accept=".csv,text/csv" class="mt-2 text-sm" data-testid="csv-file" @change="csvFile" />
      <textarea v-model="csvText" name="csv" rows="8" class="mt-2 w-full rounded-lg border border-border bg-background p-2 font-mono text-xs" :placeholder="'code,name,category,charge_code,station,price_list,price'" data-testid="csv-text" />
      <div v-if="csvResult" class="mt-2 text-sm" data-testid="csv-result">
        <p class="m-0 font-semibold">{{ csvResult.errors.length ? t('pos.products.csvErrors', { n: csvResult.errors.length }) : t('pos.products.csvOk', { created: csvResult.created, updated: csvResult.updated }) }}</p>
        <ul v-if="csvResult.errors.length" class="m-0 mt-1 list-none space-y-0.5 p-0 text-destructive">
          <li v-for="(e, i) in csvResult.errors" :key="i">{{ t('pos.products.csvRow', { row: e.row }) }}: {{ e.field }} · {{ e.code }}<span v-if="e.detail"> — {{ e.detail }}</span></li>
        </ul>
      </div>
      <div class="mt-3 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="csvOpen = false">{{ t('common.cancel') }}</Button>
        <Button type="button" variant="outline" :disabled="csvBusy || !csvText.trim()" data-testid="csv-check" @click="runCsv(true)">{{ t('pos.products.csvCheck') }}</Button>
        <Button type="button" :disabled="csvBusy || !csvText.trim()" data-testid="csv-run" @click="runCsv(false)">{{ t('pos.products.csvRun') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
