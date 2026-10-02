<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { ChargeCode, ChargeType, PriceMode, ServiceCharge, Tax } from '@/api/types'
import ChargeCalculator from '@/components/ChargeCalculator.vue'
import { useAccountNames } from './accountNames'
import GlAccountInput from './GlAccountInput.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const { label: accountLabel } = useAccountNames()
const property = usePropertyStore()

const codes = ref<ChargeCode[]>([])
const taxes = ref<Tax[]>([])
const services = ref<ServiceCharge[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<ChargeCode | 'new' | null>(null)
const typeFilter = ref('')

const canManage = computed(() => auth.can('billing_config.manage', property.currentId))
const types: ChargeType[] = ['ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER']
const columns = computed<Column<ChargeCode>[]>(() => [
  { key: 'code', label: t('chargeCodes.code'), sortable: true, filter: 'text' as const },
  { key: 'name', label: t('chargeCodes.name'), sortable: true, filter: 'text' as const },
  { key: 'charge_type', label: t('chargeCodes.type'), filter: 'select' as const, filterValue: (r: ChargeCode) => t(`chargeCodes.type_${r.charge_type}` as 'chargeCodes.type_ROOM') },
  { key: 'price_mode', label: t('chargeCodes.colPrices'), filter: 'select' as const, filterValue: (r: ChargeCode) => (r.price_mode === 'INCLUSIVE' ? t('chargeCodes.inclusive') : t('chargeCodes.exclusive')) },
  { key: 'summary', label: t('chargeCodes.colRules') },
  { key: 'account', label: t('chargeCodes.colAccount') },
  { key: 'status', label: t('setup.status'), filter: 'select' as const, filterValue: (r: ChargeCode) => (r.is_active ? t('setup.active') : t('setup.inactive')) },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])
const visible = computed(() => codes.value.filter((c) => !typeFilter.value || c.charge_type === typeFilter.value))

const blank = () => ({
  code: '', name: '', charge_type: 'OTHER' as ChargeType, price_mode: 'EXCLUSIVE' as PriceMode, default_unit_price: '', gl_account_code: '', is_active: true,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The ordered rule editor works on copies: nothing changes until "Save rules".
interface RuleRow {
  id: number
  label: string
}
const taxRows = ref<RuleRow[]>([])
const serviceRows = ref<RuleRow[]>([])
const addTax = ref(0)
const addService = ref(0)
const rulesSaved = ref(false)
const rulesVersion = ref(0) // bumped when rules are saved, so the calculator recalculates

const freeTaxes = computed(() => taxes.value.filter((x) => x.is_active && !taxRows.value.some((r) => r.id === x.id)))
const freeServices = computed(() => services.value.filter((s) => s.is_active && !serviceRows.value.some((r) => r.id === s.id)))

function summary(c: ChargeCode): string {
  const parts = [...c.service_charges.map((s) => `${s.code} ${Number(s.rate)}%`), ...c.taxes.map((x) => `${x.code} ${Number(x.rate)}%`)]
  return parts.length ? parts.join(' → ') : t('chargeCodes.summaryNone')
}

async function load(): Promise<void> {
  const propertyId = property.currentId
  codes.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [c, tx, s] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/taxes', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/service-charges', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    codes.value = c
    taxes.value = tx
    services.value = s
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function loadRules(c: ChargeCode | null): void {
  taxRows.value = (c?.taxes ?? []).map((x) => ({ id: x.tax_id, label: `${x.code} · ${Number(x.rate)}%${x.tax_on_service ? ` ${t('chargeCodes.alsoOnService')}` : ''}` }))
  serviceRows.value = (c?.service_charges ?? []).map((s) => ({ id: s.service_charge_id, label: `${s.code} · ${Number(s.rate)}%` }))
  addTax.value = 0
  addService.value = 0
  rulesSaved.value = false
}

function startNew(): void {
  Object.assign(form, blank())
  loadRules(null)
  error.value = null
  editing.value = 'new'
}

function startEdit(c: ChargeCode): void {
  Object.assign(form, blank(), {
    code: c.code, name: c.name, charge_type: c.charge_type, price_mode: c.price_mode, default_unit_price: c.default_unit_price ?? '', gl_account_code: c.gl_account_code ?? '', is_active: c.is_active,
  })
  loadRules(c)
  error.value = null
  editing.value = c
}

function move(rows: RuleRow[], i: number, by: -1 | 1): void {
  const j = i + by
  const a = rows[i]
  const b = rows[j]
  if (!a || !b) return
  rows[i] = b
  rows[j] = a
}

function addRule(kind: 'tax' | 'service'): void {
  if (kind === 'tax') {
    const tx = taxes.value.find((x) => x.id === Number(addTax.value))
    if (tx) taxRows.value.push({ id: tx.id, label: `${tx.code} · ${Number(tx.rate)}%${tx.tax_on_service ? ` ${t('chargeCodes.alsoOnService')}` : ''}` })
    addTax.value = 0
  } else {
    const s = services.value.find((x) => x.id === Number(addService.value))
    if (s) serviceRows.value.push({ id: s.id, label: `${s.code} · ${Number(s.rate)}%` })
    addService.value = 0
  }
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/charge-codes', {
        params: { path: { propertyId } },
        body: {
          code: form.code, name: form.name, charge_type: form.charge_type, price_mode: form.price_mode,
          default_unit_price: form.default_unit_price || undefined, gl_account_code: form.gl_account_code || undefined, is_active: form.is_active,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/charge-codes/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          name: form.name, charge_type: form.charge_type, price_mode: form.price_mode,
          default_unit_price: form.default_unit_price, gl_account_code: form.gl_account_code, is_active: form.is_active,
        },
      })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function saveRules(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null || editing.value === 'new') return
  saving.value = true
  error.value = null
  rulesSaved.value = false
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/charge-codes/{id}/rules', {
      params: { path: { propertyId, id: editing.value.id } },
      body: {
        taxes: taxRows.value.map((r, i) => ({ tax_id: r.id, sequence: i + 1 })),
        service_charges: serviceRows.value.map((r, i) => ({ service_charge_id: r.id, sequence: i + 1 })),
      },
    })
    if (data) {
      editing.value = data
      loadRules(data)
      rulesSaved.value = true
      rulesVersion.value++
    }
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('chargeCodes.title')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" data-testid="new-code" @click="startNew">{{ t('chargeCodes.new') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else>
    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="code-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ editing === 'new' ? t('chargeCodes.new') : t('chargeCodes.edit', { code: form.code }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField :label="t('chargeCodes.code')" :error="fieldError('code')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('chargeCodes.name')" :error="fieldError('name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('chargeCodes.type')" :hint="editing !== 'new' && editing.is_system ? t('chargeCodes.typeFixed') : undefined" :error="fieldError('charge_type')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model="form.charge_type" name="charge_type" :disabled="editing !== 'new' && editing.is_system" :aria-invalid="invalid">
                  <option v-for="ty in types" :key="ty" :value="ty">{{ t(`chargeCodes.type_${ty}`) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('chargeCodes.pricesAre')" :hint="t('chargeCodes.priceModeHint')" :error="fieldError('price_mode')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model="form.price_mode" name="price_mode" :aria-invalid="invalid">
                  <option value="EXCLUSIVE">{{ t('chargeCodes.exclusiveOption') }}</option>
                  <option value="INCLUSIVE">{{ t('chargeCodes.inclusiveOption') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('chargeCodes.defaultPrice')" :error="fieldError('default_unit_price')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.default_unit_price" name="default_unit_price" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('chargeCodes.revenueAccount')" :hint="t('chargeCodes.accountHint')" :error="fieldError('gl_account_code')">
              <template #default="{ id, invalid }"><GlAccountInput :id="id" v-model="form.gl_account_code" kind="CHARGE_CODE" :invalid="invalid" /></template>
            </FormField>
            <label class="flex items-center gap-2 self-end pb-2 text-sm">
              <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
              <span>{{ t('chargeCodes.active') }}</span>
            </label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.close') }}</Button>
            <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
          </div>

          <template v-if="editing !== 'new'">
            <h2 class="mb-1 mt-6 text-base font-semibold">{{ t('chargeCodes.rules') }}</h2>
            <p class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('chargeCodes.rulesHint') }}</p>
            <div class="grid gap-x-6 gap-y-4 md:grid-cols-2" data-testid="rules">
              <div>
                <h3 class="mb-1.5 mt-0 text-sm font-semibold">{{ t('chargeCodes.serviceCharges') }}</h3>
                <p v-if="!serviceRows.length" class="m-0 mb-2 text-sm text-muted-foreground">{{ t('chargeCodes.none') }}</p>
                <ol class="m-0 mb-2 list-decimal pl-5">
                  <li v-for="(r, i) in serviceRows" :key="r.id" class="py-1" :data-testid="`service-row-${i}`">
                    <div class="flex items-center justify-between gap-2">
                      <span>{{ r.label }}</span>
                      <span class="flex gap-1">
                        <Button type="button" variant="outline" size="sm" :disabled="i === 0" :aria-label="t('chargeCodes.moveUp', { label: r.label })" @click="move(serviceRows, i, -1)">↑</Button>
                        <Button type="button" variant="outline" size="sm" :disabled="i === serviceRows.length - 1" :aria-label="t('chargeCodes.moveDown', { label: r.label })" @click="move(serviceRows, i, 1)">↓</Button>
                        <Button type="button" variant="outline" size="sm" :aria-label="t('chargeCodes.removeLabel', { label: r.label })" @click="serviceRows.splice(i, 1)">{{ t('chargeCodes.remove') }}</Button>
                      </span>
                    </div>
                  </li>
                </ol>
                <div v-if="freeServices.length" class="flex gap-2">
                  <NativeSelect v-model="addService" name="add_service" :aria-label="t('chargeCodes.addServiceAria')">
                    <option :value="0">{{ t('chargeCodes.addServiceOption') }}</option>
                    <option v-for="s in freeServices" :key="s.id" :value="s.id">{{ s.code }} · {{ Number(s.rate) }}%</option>
                  </NativeSelect>
                  <Button type="button" variant="outline" :disabled="!addService" data-testid="add-service" @click="addRule('service')">{{ t('chargeCodes.add') }}</Button>
                </div>
              </div>
              <div>
                <h3 class="mb-1.5 mt-0 text-sm font-semibold">{{ t('chargeCodes.taxes') }}</h3>
                <p v-if="!taxRows.length" class="m-0 mb-2 text-sm text-muted-foreground">{{ t('chargeCodes.none') }}</p>
                <ol class="m-0 mb-2 list-decimal pl-5">
                  <li v-for="(r, i) in taxRows" :key="r.id" class="py-1" :data-testid="`tax-row-${i}`">
                    <div class="flex items-center justify-between gap-2">
                      <span>{{ r.label }}</span>
                      <span class="flex gap-1">
                        <Button type="button" variant="outline" size="sm" :disabled="i === 0" :aria-label="t('chargeCodes.moveUp', { label: r.label })" @click="move(taxRows, i, -1)">↑</Button>
                        <Button type="button" variant="outline" size="sm" :disabled="i === taxRows.length - 1" :aria-label="t('chargeCodes.moveDown', { label: r.label })" @click="move(taxRows, i, 1)">↓</Button>
                        <Button type="button" variant="outline" size="sm" :aria-label="t('chargeCodes.removeLabel', { label: r.label })" @click="taxRows.splice(i, 1)">{{ t('chargeCodes.remove') }}</Button>
                      </span>
                    </div>
                  </li>
                </ol>
                <div v-if="freeTaxes.length" class="flex gap-2">
                  <NativeSelect v-model="addTax" name="add_tax" :aria-label="t('chargeCodes.addTaxAria')">
                    <option :value="0">{{ t('chargeCodes.addTaxOption') }}</option>
                    <option v-for="tx in freeTaxes" :key="tx.id" :value="tx.id">{{ tx.code }} · {{ Number(tx.rate) }}%</option>
                  </NativeSelect>
                  <Button type="button" variant="outline" :disabled="!addTax" data-testid="add-tax" @click="addRule('tax')">{{ t('chargeCodes.add') }}</Button>
                </div>
              </div>
            </div>
            <div class="mt-4 flex items-center justify-end gap-3">
              <span v-if="rulesSaved" class="text-sm text-muted-foreground" role="status" data-testid="rules-saved">{{ t('chargeCodes.rulesSaved') }}</span>
              <Button type="button" :disabled="saving || !canManage" data-testid="save-rules" @click="saveRules">{{ t('chargeCodes.saveRules') }}</Button>
            </div>
            <ChargeCalculator
              :key="editing.id"
              :charge-code-id="editing.id"
              :price-mode="editing.price_mode"
              :default-unit-price="editing.default_unit_price"
              :version="rulesVersion"
            />
          </template>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <FormField v-if="codes.length" class="mb-3 max-w-56" :label="t('chargeCodes.type')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="typeFilter" name="type_filter">
              <option value="">{{ t('chargeCodes.allTypes') }}</option>
              <option v-for="ty in types" :key="ty" :value="ty">{{ t(`chargeCodes.type_${ty}`) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <EmptyState v-if="loaded && !codes.length" :title="t('chargeCodes.empty')" data-testid="empty" />
        <DataTable v-else-if="codes.length" :columns="columns" :rows="visible" row-key="id" :row-test-id="(c) => `code-${c.code}`" :caption="t('chargeCodes.title')">
          <template #cell-code="{ row }"><b>{{ row.code }}</b><small v-if="row.is_system" class="text-muted-foreground"> {{ t('chargeCodes.system') }}</small></template>
          <template #cell-charge_type="{ row }">{{ t(`chargeCodes.type_${row.charge_type}`) }}</template>
          <template #cell-price_mode="{ row }">{{ row.price_mode === 'INCLUSIVE' ? t('chargeCodes.inclusive') : t('chargeCodes.exclusive') }}</template>
          <template #cell-summary="{ row }"><span data-testid="summary">{{ summary(row) }}</span></template>
          <template #cell-account="{ row }"><span data-testid="account">{{ accountLabel(row.gl_account_code) }}</span></template>
          <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
          <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
