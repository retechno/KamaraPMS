<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosCategory, PosPriceList, PosRuleProfile } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * Categories (what the cashier screen groups products by), price lists (which an outlet uses) and the tax and service profiles (docs/architecture/34-pos.md 4.3): a profile is the list of taxes and
 * service charges an outlet applies, and a product may name another one. The rules are those of Setup > Taxes and service charges; a profile only picks and orders them.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const categories = ref<PosCategory[]>([])
const lists = ref<PosPriceList[]>([])
const profiles = ref<PosRuleProfile[]>([])
const taxes = ref<{ id: number; code: string; name: string; rate: string; is_active: boolean }[]>([])
const services = ref<{ id: number; code: string; name: string; rate: string; is_active: boolean }[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [c, l, p, tx, sv] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/categories', path),
      api.GET('/api/v1/properties/{propertyId}/pos/price-lists', path),
      api.GET('/api/v1/properties/{propertyId}/pos/rule-profiles', path),
      api.GET('/api/v1/properties/{propertyId}/taxes', path),
      api.GET('/api/v1/properties/{propertyId}/service-charges', path),
    ])
    categories.value = c.data?.data ?? []
    lists.value = l.data?.data ?? []
    profiles.value = p.data?.data ?? []
    taxes.value = (tx.data?.data ?? []) as typeof taxes.value
    services.value = (sv.data?.data ?? []) as typeof services.value
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

// ---- categories
const cForm = reactive({ id: null as number | null, code: '', name: '', parent_id: 0, sort_order: 0, is_active: true })
const cDialog = ref(false)
const cError = ref<ApiError | null>(null)
function editCategory(c?: PosCategory): void {
  Object.assign(cForm, c ? { id: c.id, code: c.code, name: c.name, parent_id: c.parent_id ?? 0, sort_order: c.sort_order, is_active: c.is_active } : { id: null, code: '', name: '', parent_id: 0, sort_order: categories.value.length + 1, is_active: true })
  cError.value = null
  cDialog.value = true
}
async function saveCategory(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  cError.value = null
  const body = { code: cForm.code.trim().toUpperCase(), name: cForm.name.trim(), parent_id: cForm.parent_id || null, sort_order: cForm.sort_order, is_active: cForm.is_active }
  try {
    if (cForm.id === null) await api.POST('/api/v1/properties/{propertyId}/pos/categories', { params: { path: { propertyId } }, body })
    else await api.PUT('/api/v1/properties/{propertyId}/pos/categories/{id}', { params: { path: { propertyId, id: cForm.id } }, body })
    cDialog.value = false
    notice.value = t('pos.menu.saved')
    await load()
  } catch (e) {
    cError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- price lists
const lForm = reactive({ id: null as number | null, code: '', name: '', is_active: true })
const lDialog = ref(false)
const lError = ref<ApiError | null>(null)
function editList(l?: PosPriceList): void {
  Object.assign(lForm, l ? { id: l.id, code: l.code, name: l.name, is_active: l.is_active } : { id: null, code: '', name: '', is_active: true })
  lError.value = null
  lDialog.value = true
}
async function saveList(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  lError.value = null
  const body = { code: lForm.code.trim().toUpperCase(), name: lForm.name.trim(), is_active: lForm.is_active }
  try {
    if (lForm.id === null) await api.POST('/api/v1/properties/{propertyId}/pos/price-lists', { params: { path: { propertyId } }, body })
    else await api.PUT('/api/v1/properties/{propertyId}/pos/price-lists/{id}', { params: { path: { propertyId, id: lForm.id } }, body })
    lDialog.value = false
    notice.value = t('pos.menu.saved')
    await load()
  } catch (e) {
    lError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- profiles
interface RuleRow { key: string; tax_id: number | null; service_charge_id: number | null }
const pForm = reactive({ id: null as number | null, code: '', name: '', price_mode: 'EXCLUSIVE', is_active: true, rules: [] as RuleRow[] })
const pDialog = ref(false)
const pError = ref<ApiError | null>(null)
const addRule = ref('')
function editProfile(p?: PosRuleProfile): void {
  Object.assign(pForm, p
    ? {
        id: p.id, code: p.code, name: p.name, price_mode: p.price_mode, is_active: p.is_active,
        rules: p.rules.map((r) => ({ key: r.rule_type === 'TAX' ? `T${r.tax_id}` : `S${r.service_charge_id}`, tax_id: r.tax_id ?? null, service_charge_id: r.service_charge_id ?? null })),
      }
    : { id: null, code: '', name: '', price_mode: 'EXCLUSIVE', is_active: true, rules: [] })
  pError.value = null
  addRule.value = ''
  pDialog.value = true
}
const ruleName = (r: RuleRow): string => {
  const tax = r.tax_id !== null ? taxes.value.find((x) => x.id === r.tax_id) : undefined
  const svc = r.service_charge_id !== null ? services.value.find((x) => x.id === r.service_charge_id) : undefined
  const x = tax ?? svc
  return x ? `${x.name} ${x.rate}%` : r.key
}
const availableRules = computed(() => [
  ...services.value.filter((s) => s.is_active && !pForm.rules.some((r) => r.service_charge_id === s.id)).map((s) => ({ key: `S${s.id}`, label: `${t('pos.profiles.service')}: ${s.name} ${s.rate}%` })),
  ...taxes.value.filter((x) => x.is_active && !pForm.rules.some((r) => r.tax_id === x.id)).map((x) => ({ key: `T${x.id}`, label: `${t('pos.profiles.tax')}: ${x.name} ${x.rate}%` })),
])
function pushRule(): void {
  const k = addRule.value
  if (!k) return
  pForm.rules.push(k.startsWith('T') ? { key: k, tax_id: Number(k.slice(1)), service_charge_id: null } : { key: k, tax_id: null, service_charge_id: Number(k.slice(1)) })
  addRule.value = ''
}
function moveRule(i: number, by: number): void {
  const j = i + by
  if (j < 0 || j >= pForm.rules.length) return
  const [x] = pForm.rules.splice(i, 1)
  pForm.rules.splice(j, 0, x!)
}
async function saveProfile(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  pError.value = null
  const body = {
    code: pForm.code.trim().toUpperCase(), name: pForm.name.trim(), price_mode: pForm.price_mode as 'EXCLUSIVE' | 'INCLUSIVE', is_active: pForm.is_active,
    rules: pForm.rules.map((r) => ({ tax_id: r.tax_id, service_charge_id: r.service_charge_id })),
  }
  try {
    if (pForm.id === null) await api.POST('/api/v1/properties/{propertyId}/pos/rule-profiles', { params: { path: { propertyId } }, body })
    else await api.PUT('/api/v1/properties/{propertyId}/pos/rule-profiles/{id}', { params: { path: { propertyId, id: pForm.id } }, body })
    pDialog.value = false
    notice.value = t('pos.menu.saved')
    await load()
  } catch (e) {
    pError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const topLevel = computed(() => categories.value.filter((c) => c.parent_id === null))
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('pos.menu.title')" />
  <p class="muted">{{ t('pos.menu.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="grid gap-4 xl:grid-cols-3">
    <section class="rounded-xl border border-border bg-card p-4" data-testid="categories">
      <header class="mb-3 flex items-center justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('pos.menu.categories') }}</h2>
        <Button size="sm" data-testid="category-new" @click="editCategory()">{{ t('pos.menu.newCategory') }}</Button>
      </header>
      <p v-if="categories.length === 0" class="muted">{{ t('pos.menu.noCategories') }}</p>
      <ul class="m-0 list-none divide-y divide-border p-0">
        <li v-for="c in categories" :key="c.id" class="flex min-h-11 items-center gap-2 py-2" :data-testid="`category-${c.code}`">
          <div class="min-w-0 flex-1">
            <strong class="block truncate text-sm">{{ c.parent_id ? '– ' : '' }}{{ c.name }}</strong>
            <span class="text-xs text-muted-foreground">{{ c.code }} · {{ t('pos.menu.products', { n: c.product_count }) }}</span>
          </div>
          <Badge v-if="!c.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
          <Button variant="outline" size="sm" @click="editCategory(c)">{{ t('common.edit') }}</Button>
        </li>
      </ul>
    </section>

    <section class="rounded-xl border border-border bg-card p-4" data-testid="price-lists">
      <header class="mb-3 flex items-center justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('pos.menu.priceLists') }}</h2>
        <Button size="sm" data-testid="list-new" @click="editList()">{{ t('pos.menu.newList') }}</Button>
      </header>
      <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('pos.menu.listHint') }}</p>
      <ul class="m-0 list-none divide-y divide-border p-0">
        <li v-for="l in lists" :key="l.id" class="flex min-h-11 items-center gap-2 py-2" :data-testid="`list-${l.code}`">
          <div class="min-w-0 flex-1">
            <strong class="block truncate text-sm">{{ l.name }}</strong>
            <span class="text-xs text-muted-foreground">{{ l.code }} · {{ t('pos.menu.prices', { n: l.item_count }) }}</span>
          </div>
          <Badge v-if="!l.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
          <Button variant="outline" size="sm" @click="editList(l)">{{ t('common.edit') }}</Button>
        </li>
      </ul>
    </section>

    <section class="rounded-xl border border-border bg-card p-4" data-testid="profiles">
      <header class="mb-3 flex items-center justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('pos.profiles.title') }}</h2>
        <Button size="sm" data-testid="profile-new" @click="editProfile()">{{ t('pos.profiles.new') }}</Button>
      </header>
      <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('pos.profiles.hint') }}</p>
      <ul class="m-0 list-none divide-y divide-border p-0">
        <li v-for="p in profiles" :key="p.id" class="flex min-h-11 items-center gap-2 py-2" :data-testid="`profile-${p.code}`">
          <div class="min-w-0 flex-1">
            <strong class="block truncate text-sm">{{ p.name }}</strong>
            <span class="text-xs text-muted-foreground">
              {{ p.rules.length ? p.rules.map((r) => `${r.name} ${r.rate}%`).join(' · ') : t('pos.profiles.noRules') }} · {{ t(`pos.profiles.mode_${p.price_mode}`) }}
            </span>
          </div>
          <Badge v-if="!p.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
          <Button variant="outline" size="sm" @click="editProfile(p)">{{ t('common.edit') }}</Button>
        </li>
      </ul>
    </section>
  </div>

  <Dialog :open="cDialog" @update:open="(v) => (cDialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="category-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveCategory">
        <DialogTitle>{{ cForm.id === null ? t('pos.menu.newCategory') : t('pos.menu.editCategory') }}</DialogTitle>
        <ErrorNotice v-if="cError" :error="cError" inline />
        <FormField :label="t('pos.menu.code')" :error="cError?.fieldMessage('code')">
          <template #default="{ id }"><Input :id="id" v-model="cForm.code" name="code" maxlength="20" :disabled="cForm.id !== null" class="uppercase" /></template>
        </FormField>
        <FormField :label="t('pos.menu.name')" :error="cError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="cForm.name" name="name" maxlength="100" /></template>
        </FormField>
        <FormField v-if="cForm.id === null" :label="t('pos.menu.parent')" :error="cError?.fieldMessage('parent_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="cForm.parent_id" name="parent_id">
              <option :value="0">{{ t('pos.menu.noParent') }}</option>
              <option v-for="c in topLevel" :key="c.id" :value="c.id">{{ c.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="cForm.is_active" type="checkbox" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="cDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !cForm.name.trim() || !cForm.code.trim()" data-testid="category-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="lDialog" @update:open="(v) => (lDialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="list-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveList">
        <DialogTitle>{{ lForm.id === null ? t('pos.menu.newList') : t('pos.menu.editList') }}</DialogTitle>
        <ErrorNotice v-if="lError" :error="lError" inline />
        <FormField :label="t('pos.menu.code')" :error="lError?.fieldMessage('code')">
          <template #default="{ id }"><Input :id="id" v-model="lForm.code" name="code" maxlength="20" :disabled="lForm.id !== null" class="uppercase" /></template>
        </FormField>
        <FormField :label="t('pos.menu.name')" :error="lError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="lForm.name" name="name" maxlength="100" /></template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="lForm.is_active" type="checkbox" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="lDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !lForm.name.trim() || !lForm.code.trim()" data-testid="list-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="pDialog" @update:open="(v) => (pDialog = v)">
    <DialogContent class="max-h-[90vh] max-w-lg overflow-y-auto p-5" data-testid="profile-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveProfile">
        <DialogTitle>{{ pForm.id === null ? t('pos.profiles.new') : t('pos.profiles.edit') }}</DialogTitle>
        <DialogDescription>{{ t('pos.profiles.dialogHint') }}</DialogDescription>
        <ErrorNotice v-if="pError" :error="pError" inline />
        <FormField :label="t('pos.menu.code')" :error="pError?.fieldMessage('code')">
          <template #default="{ id }"><Input :id="id" v-model="pForm.code" name="code" maxlength="20" :disabled="pForm.id !== null" class="uppercase" /></template>
        </FormField>
        <FormField :label="t('pos.menu.name')" :error="pError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="pForm.name" name="name" maxlength="100" /></template>
        </FormField>
        <FormField :label="t('pos.profiles.mode')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="pForm.price_mode" name="price_mode">
              <option value="EXCLUSIVE">{{ t('pos.profiles.mode_EXCLUSIVE') }}</option>
              <option value="INCLUSIVE">{{ t('pos.profiles.mode_INCLUSIVE') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <div>
          <span class="mb-1 block text-sm font-medium">{{ t('pos.profiles.rules') }}</span>
          <ol class="m-0 list-none space-y-1 p-0" data-testid="profile-rules">
            <li v-for="(r, i) in pForm.rules" :key="r.key" class="flex min-h-11 items-center gap-2 rounded-lg border border-border px-3 text-sm">
              <span class="min-w-0 flex-1 truncate">{{ i + 1 }}. {{ ruleName(r) }}</span>
              <Button type="button" variant="ghost" size="sm" :disabled="i === 0" :aria-label="t('pos.profiles.up')" @click="moveRule(i, -1)">↑</Button>
              <Button type="button" variant="ghost" size="sm" :disabled="i === pForm.rules.length - 1" :aria-label="t('pos.profiles.down')" @click="moveRule(i, 1)">↓</Button>
              <Button type="button" variant="ghost" size="sm" :aria-label="t('common.remove')" @click="pForm.rules.splice(i, 1)">✕</Button>
            </li>
          </ol>
          <div class="mt-2 flex gap-2">
            <NativeSelect v-model="addRule" name="add_rule" class="flex-1" :aria-label="t('pos.profiles.addRule')" data-testid="profile-add-rule">
              <option value="">{{ t('pos.profiles.addRule') }}</option>
              <option v-for="a in availableRules" :key="a.key" :value="a.key">{{ a.label }}</option>
            </NativeSelect>
            <Button type="button" variant="outline" :disabled="!addRule" data-testid="profile-add" @click="pushRule">{{ t('common.add') }}</Button>
          </div>
        </div>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="pForm.is_active" type="checkbox" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="pDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !pForm.name.trim() || !pForm.code.trim()" data-testid="profile-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
