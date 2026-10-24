<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BanquetItem, BanquetPackage } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/**
 * The items an event can buy and the packages that bundle them (docs/architecture/36-banquet.md 3.2, 6.9). An item has a charge code, a price list and a basis: per guest, per session, per hour or per
 * unit. A package is a price per guest and the items it contains with a quantity for each guest. Nothing is sold from here yet: the quotation comes with the bill.
 */
const KINDS = ['MENU', 'DRINK_PACKAGE', 'ROOM_RENTAL', 'EQUIPMENT', 'DECOR', 'OTHER'] as const
const BASES = ['PER_PAX', 'PER_SESSION', 'PER_HOUR', 'PER_UNIT'] as const
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canSetup = computed(() => auth.can('banquet.setup', pid.value))
const items = ref<BanquetItem[]>([])
const packages = ref<BanquetPackage[]>([])
const codes = ref<{ id: number; code: string; name: string }[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const tab = ref('items')

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const path = { params: { path: { propertyId } } }
    const [i, p] = await Promise.all([api.GET('/api/v1/properties/{propertyId}/banquet/items', path), api.GET('/api/v1/properties/{propertyId}/banquet/packages', path)])
    items.value = i.data?.data ?? []
    packages.value = p.data?.data ?? []
    if (canSetup.value && !codes.value.length) {
      codes.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

// ---- items
const iDialog = ref(false)
const iError = ref<ApiError | null>(null)
const busy = ref(false)
const iForm = reactive({ id: null as number | null, code: '', name: '', kind: 'MENU' as (typeof KINDS)[number], basis: 'PER_PAX' as (typeof BASES)[number], charge_code_id: 0, price: '0', fb: '' as '' | 'FOOD' | 'BEVERAGE' | 'OTHER', description: '', active: true })
function editItem(x?: BanquetItem): void {
  iError.value = null
  Object.assign(iForm, x
    ? { id: x.id, code: x.code, name: x.name, kind: x.kind, basis: x.unit_basis, charge_code_id: x.charge_code_id, price: x.list_price, fb: x.fb_class ?? '', description: x.description ?? '', active: x.is_active }
    : { id: null, code: '', name: '', kind: 'MENU', basis: 'PER_PAX', charge_code_id: 0, price: '0', fb: '', description: '', active: true })
  iDialog.value = true
}
async function saveItem(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  iError.value = null
  const body = { code: iForm.code.trim().toUpperCase(), name: iForm.name.trim(), kind: iForm.kind, unit_basis: iForm.basis, charge_code_id: iForm.charge_code_id, list_price: iForm.price.trim(), fb_class: iForm.fb || null, description: iForm.description.trim() || null, is_active: iForm.active }
  try {
    if (iForm.id === null) await api.POST('/api/v1/properties/{propertyId}/banquet/items', { params: { path: { propertyId } }, body })
    else await api.PATCH('/api/v1/properties/{propertyId}/banquet/items/{id}', { params: { path: { propertyId, id: iForm.id } }, body })
    iDialog.value = false
    toast.success(t('banquet.catalog.saved'))
    await load()
  } catch (e) {
    iError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- packages
const pDialog = ref(false)
const pError = ref<ApiError | null>(null)
const pForm = reactive({ id: null as number | null, code: '', name: '', price: '0', description: '', active: true, components: [] as { item_id: number; qty: string }[] })
function editPackage(x?: BanquetPackage): void {
  pError.value = null
  Object.assign(pForm, x
    ? { id: x.id, code: x.code, name: x.name, price: x.price_per_pax, description: x.description ?? '', active: x.is_active, components: x.components.map((c) => ({ item_id: c.item_id, qty: c.qty_per_pax })) }
    : { id: null, code: '', name: '', price: '0', description: '', active: true, components: [] })
  pDialog.value = true
}
async function savePackage(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  pError.value = null
  const body = {
    code: pForm.code.trim().toUpperCase(), name: pForm.name.trim(), price_per_pax: pForm.price.trim(), description: pForm.description.trim() || null, is_active: pForm.active,
    components: pForm.components.filter((c) => c.item_id).map((c) => ({ item_id: c.item_id, qty_per_pax: c.qty.trim() || '1' })),
  }
  try {
    if (pForm.id === null) await api.POST('/api/v1/properties/{propertyId}/banquet/packages', { params: { path: { propertyId } }, body })
    else await api.PATCH('/api/v1/properties/{propertyId}/banquet/packages/{id}', { params: { path: { propertyId, id: pForm.id } }, body })
    pDialog.value = false
    toast.success(t('banquet.catalog.saved'))
    await load()
  } catch (e) {
    pError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="t('banquet.catalog.title')" :description="t('banquet.catalog.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <Tabs v-else :model-value="tab" @update:model-value="tab = String($event)">
    <TabsList data-testid="catalog-tabs">
      <TabsTrigger value="items" data-testid="tab-items">{{ t('banquet.catalog.items') }}</TabsTrigger>
      <TabsTrigger value="packages" data-testid="tab-packages">{{ t('banquet.catalog.packages') }}</TabsTrigger>
    </TabsList>
    <TabsContent value="items">
      <div class="mb-3 flex justify-end"><Button v-if="canSetup" type="button" data-testid="item-new" @click="editItem()">{{ t('banquet.catalog.newItem') }}</Button></div>
      <EmptyState v-if="loaded && !items.length" :title="t('banquet.catalog.noItems')" :description="t('emptyState.banquetItems')" />
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0" data-testid="item-list">
        <li v-for="x in items" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" :data-testid="`item-${x.code}`">
          <div class="min-w-0 flex-1 basis-56">
            <p class="m-0 font-medium">{{ x.name }} <span class="text-muted-foreground">{{ x.code }}</span></p>
            <p class="m-0 text-sm text-muted-foreground">{{ t(`banquet.kind.${x.kind}`) }} · {{ t(`banquet.basis.${x.unit_basis}`) }} · {{ x.charge_code }}</p>
          </div>
          <span class="tabular-nums">{{ formatMoney(x.list_price) }}</span>
          <Badge :variant="x.is_active ? 'success' : 'outline'">{{ x.is_active ? t('common.active') : t('common.inactive') }}</Badge>
          <Button v-if="canSetup" type="button" variant="outline" size="sm" :data-testid="`item-edit-${x.code}`" @click="editItem(x)">{{ t('common.edit') }}</Button>
        </li>
      </ul>
    </TabsContent>
    <TabsContent value="packages">
      <div class="mb-3 flex justify-end"><Button v-if="canSetup" type="button" data-testid="package-new" @click="editPackage()">{{ t('banquet.catalog.newPackage') }}</Button></div>
      <EmptyState v-if="loaded && !packages.length" :title="t('banquet.catalog.noPackages')" :description="t('emptyState.banquetPackages')" />
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0" data-testid="package-list">
        <li v-for="x in packages" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" :data-testid="`package-${x.code}`">
          <div class="min-w-0 flex-1 basis-56">
            <p class="m-0 font-medium">{{ x.name }} <span class="text-muted-foreground">{{ x.code }}</span></p>
            <p class="m-0 text-sm text-muted-foreground">{{ x.components.map((c) => c.item_name).join(', ') }}</p>
          </div>
          <span class="tabular-nums">{{ t('banquet.catalog.perPax', { price: formatMoney(x.price_per_pax) }) }}</span>
          <Badge :variant="x.is_active ? 'success' : 'outline'">{{ x.is_active ? t('common.active') : t('common.inactive') }}</Badge>
          <Button v-if="canSetup" type="button" variant="outline" size="sm" :data-testid="`package-edit-${x.code}`" @click="editPackage(x)">{{ t('common.edit') }}</Button>
        </li>
      </ul>
    </TabsContent>
  </Tabs>

  <Dialog :open="iDialog" @update:open="(v) => (iDialog = v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="item-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveItem">
        <DialogTitle>{{ iForm.id === null ? t('banquet.catalog.newItem') : t('banquet.catalog.editItem') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.catalog.itemHint') }}</DialogDescription>
        <ErrorNotice v-if="iError" :error="iError" inline />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.rooms.code')" required :error="iError?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="iForm.code" name="code" maxlength="20" :disabled="iForm.id !== null" class="uppercase" /></template></FormField>
          <FormField :label="t('banquet.rooms.name')" required :error="iError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="iForm.name" name="name" maxlength="100" /></template></FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.catalog.kind')"><template #default="{ id }"><NativeSelect :id="id" v-model="iForm.kind" name="kind"><option v-for="k in KINDS" :key="k" :value="k">{{ t(`banquet.kind.${k}`) }}</option></NativeSelect></template></FormField>
          <FormField :label="t('banquet.catalog.basis')"><template #default="{ id }"><NativeSelect :id="id" v-model="iForm.basis" name="unit_basis"><option v-for="b in BASES" :key="b" :value="b">{{ t(`banquet.basis.${b}`) }}</option></NativeSelect></template></FormField>
        </div>
        <FormField :label="t('banquet.catalog.chargeCode')" required :error="iError?.fieldMessage('charge_code_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="iForm.charge_code_id" name="charge_code_id">
              <option :value="0">{{ t('banquet.event.choose') }}</option>
              <option v-for="c in codes" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.catalog.price')" required :error="iError?.fieldMessage('list_price')"><template #default="{ id }"><Input :id="id" v-model="iForm.price" name="list_price" inputmode="decimal" /></template></FormField>
          <FormField :label="t('banquet.catalog.fbClass')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="iForm.fb" name="fb_class"><option value="">{{ t('banquet.catalog.noClass') }}</option><option v-for="c in ['FOOD', 'BEVERAGE', 'OTHER']" :key="c" :value="c">{{ t(`banquet.fb.${c}`) }}</option></NativeSelect>
            </template>
          </FormField>
        </div>
        <FormField :label="t('banquet.event.notes')"><template #default="{ id }"><Input :id="id" v-model="iForm.description" name="description" maxlength="500" /></template></FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="iForm.active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="iDialog = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy" data-testid="item-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="pDialog" @update:open="(v) => (pDialog = v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="package-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="savePackage">
        <DialogTitle>{{ pForm.id === null ? t('banquet.catalog.newPackage') : t('banquet.catalog.editPackage') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.catalog.packageHint') }}</DialogDescription>
        <ErrorNotice v-if="pError" :error="pError" inline />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.rooms.code')" required :error="pError?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="pForm.code" name="code" maxlength="20" :disabled="pForm.id !== null" class="uppercase" /></template></FormField>
          <FormField :label="t('banquet.rooms.name')" required :error="pError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="pForm.name" name="name" maxlength="100" /></template></FormField>
        </div>
        <FormField :label="t('banquet.catalog.pricePerPax')" required :error="pError?.fieldMessage('price_per_pax')"><template #default="{ id }"><Input :id="id" v-model="pForm.price" name="price_per_pax" inputmode="decimal" /></template></FormField>
        <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
          <legend class="mb-1 text-sm font-medium">{{ t('banquet.catalog.components') }}</legend>
          <div v-for="(c, n) in pForm.components" :key="n" class="flex items-center gap-2" :data-testid="`component-${n}`">
            <NativeSelect v-model.number="c.item_id" :name="`component_item_${n}`" :aria-label="t('banquet.catalog.items')"><option :value="0">{{ t('banquet.event.choose') }}</option><option v-for="x in items.filter((y) => y.is_active || y.id === c.item_id)" :key="x.id" :value="x.id">{{ x.name }}</option></NativeSelect>
            <Input v-model="c.qty" :name="`component_qty_${n}`" class="w-24" inputmode="decimal" :aria-label="t('banquet.catalog.qtyPerPax')" />
            <Button type="button" variant="outline" size="sm" @click="pForm.components.splice(n, 1)">{{ t('common.remove') }}</Button>
          </div>
          <Button type="button" variant="outline" size="sm" class="self-start" data-testid="component-add" @click="pForm.components.push({ item_id: 0, qty: '1' })">{{ t('banquet.catalog.addComponent') }}</Button>
          <p v-if="pError?.fieldMessage('components')" class="m-0 text-sm text-destructive">{{ pError.fieldMessage('components') }}</p>
        </fieldset>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="pForm.active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="pDialog = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy" data-testid="package-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
