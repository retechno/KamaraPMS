<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useAccountNames } from './accountNames'
import GlAccountInput from './GlAccountInput.vue'

/** Taxes and service charges differ only in `tax_on_service`; one section edits either list. */
const props = defineProps<{ kind: 'tax' | 'service' }>()

interface Item {
  id: number
  code: string
  name: string
  rate: string
  is_active: boolean
  tax_on_service?: boolean
  tax_kind?: 'VAT' | 'LOCAL' | 'OTHER'
  gl_account_code: string | null
}

const auth = useAuthStore()
const { label: accountLabel } = useAccountNames()
const property = usePropertyStore()

const items = ref<Item[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)
const editing = ref<Item | 'new' | null>(null)

const isTax = computed(() => props.kind === 'tax')
const title = computed(() => (isTax.value ? t('rateItems.taxes') : t('rateItems.services')))
const canManage = computed(() => auth.can('billing_config.manage', property.currentId))
const columns = computed<Column<Item>[]>(() => [
  { key: 'code', label: t('rateItems.code') },
  { key: 'name', label: t('rateItems.name') },
  { key: 'rate', label: t('rateItems.colRate') },
  { key: 'account', label: t('rateItems.colAccount') },
  ...(isTax.value ? [{ key: 'tax_kind', label: t('rateItems.colKind') }, { key: 'tax_on_service', label: t('rateItems.colOnService') }] : []),
  { key: 'status', label: t('setup.status') },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])
const blank = () => ({ code: '', name: '', rate: '', tax_on_service: false, tax_kind: 'LOCAL', gl_account_code: '', is_active: true })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = property.currentId
  items.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    items.value = await (isTax.value
      ? fetchAll<Item>((cursor) => api.GET('/api/v1/properties/{propertyId}/taxes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
      : fetchAll<Item>((cursor) => api.GET('/api/v1/properties/{propertyId}/service-charges', { params: { path: { propertyId }, query: { limit: 200, cursor } } })))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  notice.value = ''
  editing.value = 'new'
}

function startEdit(i: Item): void {
  Object.assign(form, blank(), { code: i.code, name: i.name, rate: i.rate, tax_on_service: i.tax_on_service ?? false, tax_kind: i.tax_kind ?? 'LOCAL', gl_account_code: i.gl_account_code ?? '', is_active: i.is_active })
  error.value = null
  notice.value = ''
  editing.value = i
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    let saved: Item | undefined
    if (editing.value === 'new') {
      saved = isTax.value
        ? (await api.POST('/api/v1/properties/{propertyId}/taxes', {
            params: { path: { propertyId } },
            body: { code: form.code, name: form.name, rate: form.rate, tax_on_service: form.tax_on_service, tax_kind: form.tax_kind as 'LOCAL', gl_account_code: form.gl_account_code || undefined, is_active: form.is_active },
          })).data
        : (await api.POST('/api/v1/properties/{propertyId}/service-charges', {
            params: { path: { propertyId } },
            body: { code: form.code, name: form.name, rate: form.rate, gl_account_code: form.gl_account_code || undefined, is_active: form.is_active },
          })).data
    } else {
      const id = editing.value.id
      saved = isTax.value
        ? (await api.PATCH('/api/v1/properties/{propertyId}/taxes/{id}', {
            params: { path: { propertyId, id } },
            body: { name: form.name, rate: form.rate, tax_on_service: form.tax_on_service, tax_kind: form.tax_kind as 'LOCAL', gl_account_code: form.gl_account_code, is_active: form.is_active },
          })).data
        : (await api.PATCH('/api/v1/properties/{propertyId}/service-charges/{id}', {
            params: { path: { propertyId, id } },
            body: { name: form.name, rate: form.rate, gl_account_code: form.gl_account_code, is_active: form.is_active },
          })).data
    }
    const affected = (saved as { affected_open_stays?: number } | undefined)?.affected_open_stays
    notice.value = affected ? t('rateItems.notice', { n: affected }) : ''
    editing.value = null
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
  <Card class="mb-4" :data-testid="`section-${kind}`">
    <CardHeader class="flex-row items-center justify-between">
      <CardTitle>{{ title }}</CardTitle>
      <Button v-if="canManage && !editing" type="button" variant="outline" size="sm" data-testid="new-item" @click="startNew">{{ isTax ? t('rateItems.newTax') : t('rateItems.newService') }}</Button>
    </CardHeader>
    <CardContent>
      <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
      <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>

      <form v-if="editing" novalidate class="mb-4" @submit.prevent="save">
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('rateItems.code')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('rateItems.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('rateItems.rate')" :hint="t('rateItems.rateHint')" :error="fieldError('rate')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.rate" name="rate" inputmode="decimal" placeholder="11" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="isTax ? t('rateItems.taxAccount') : t('rateItems.serviceAccount')" :hint="t('rateItems.accountHint')" :error="fieldError('gl_account_code')">
            <template #default="{ id, invalid }"><GlAccountInput :id="id" v-model="form.gl_account_code" :kind="isTax ? 'TAX' : 'SERVICE_CHARGE'" :invalid="invalid" /></template>
          </FormField>
          <FormField v-if="isTax" :label="t('rateItems.kind')" :hint="t('rateItems.kindHint')" :error="fieldError('tax_kind')">
            <template #default="{ id, invalid }">
              <Combobox :id="id" v-model="form.tax_kind" name="tax_kind" :aria-invalid="invalid" :options="[{ value: 'LOCAL', label: t('rateItems.kind_LOCAL') }, { value: 'VAT', label: t('rateItems.kind_VAT') }, { value: 'OTHER', label: t('rateItems.kind_OTHER') }]" />
            </template>
          </FormField>
          <label v-if="isTax" class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.tax_on_service" name="tax_on_service" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('rateItems.taxOnService') }}</span>
          </label>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('rateItems.active') }}</span>
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </form>

      <EmptyState v-if="loaded && !items.length" :title="t('rateItems.none')" data-testid="empty" />
      <DataTable v-else-if="items.length" :columns="columns" :rows="items" row-key="id" :row-test-id="(i) => `${kind}-${i.code}`" :caption="title">
        <template #cell-code="{ row }"><b>{{ row.code }}</b></template>
        <template #cell-rate="{ row }">{{ Number(row.rate) }}%</template>
        <template #cell-account="{ row }"><span data-testid="account">{{ accountLabel(row.gl_account_code) }}</span></template>
        <template #cell-tax_kind="{ row }">{{ t(`rateItems.kind_${row.tax_kind ?? 'LOCAL'}`) }}</template>
        <template #cell-tax_on_service="{ row }">{{ row.tax_on_service ? t('common.yes') : t('common.no') }}</template>
        <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
        <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
      </DataTable>
    </CardContent>
  </Card>
</template>
