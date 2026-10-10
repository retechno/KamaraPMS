<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BedAdjustment as Adjustment, BedType, RatePlan, RoomType } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const beds = ref<BedType[]>([])
const rows = ref<Adjustment[]>([])
const planId = ref(0)
const loaded = ref(false)
const adding = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)

const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('rate.manage', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)
const blank = () => ({ room_type_id: 0, bed_type_id: 0, adjust_kind: 'AMOUNT' as 'AMOUNT' | 'PERCENT', amount: '', effective_from: property.clock?.business_date ?? '' })
const form = reactive(blank())

const columns = computed<Column<Adjustment>[]>(() => [
  { key: 'room_type_code', label: t('bedSupplements.roomType') },
  { key: 'bed', label: t('bedSupplements.bed') },
  { key: 'supplement', label: t('bedSupplements.supplement'), align: 'right', class: 'tabular-nums' },
  { key: 'effective_from', label: t('bedSupplements.from') },
  { key: 'status', label: '' },
])

const supplement = (r: Adjustment): string => {
  const v = Number(r.amount)
  return `${v > 0 ? '+' : ''}${r.amount}${r.adjust_kind === 'PERCENT' ? '%' : ''}`
}

async function loadRows(): Promise<void> {
  const propertyId = pid.value
  rows.value = []
  if (propertyId === null || !planId.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rate-plans/{id}/bed-adjustments', { params: { path: { propertyId, id: planId.value } } })
    rows.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, rt, b] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
      api.GET('/api/v1/properties/{propertyId}/bed-types', { params: path }),
    ])
    plans.value = p.filter((x) => x.occupancy_kind === 'PAID')
    types.value = rt
    beds.value = b.data?.data ?? []
    if (!planId.value && plans.value.length) planId.value = plans.value[0]!.id
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
  await loadRows()
}

function startAdd(): void {
  Object.assign(form, blank(), { room_type_id: types.value[0]?.id ?? 0, bed_type_id: beds.value.find((x) => x.is_active)?.id ?? 0 })
  error.value = null
  adding.value = true
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !planId.value) return
  saving.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/rate-plans/{id}/bed-adjustments', {
      params: { path: { propertyId, id: planId.value } },
      body: { room_type_id: Number(form.room_type_id), bed_type_id: Number(form.bed_type_id), adjust_kind: form.adjust_kind, amount: form.amount.trim(), effective_from: form.effective_from },
    })
    adding.value = false
    await loadRows()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(pid, () => {
  planId.value = 0
  adding.value = false
  void load()
}, { immediate: true })
watch(planId, () => void loadRows())
</script>

<template>
  <PageHeader :title="t('bedSupplements.title')" :description="t('bedSupplements.intro')">
    <template #actions>
      <Button v-if="canManage && !adding && planId" type="button" data-testid="new-supplement" @click="startAdd">{{ t('bedSupplements.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('bedSupplements.readOnly', { permission: 'rate.manage' }) }}</p>

  <template v-if="pid !== null">
    <FormField class="mb-4 max-w-xs" :label="t('bedSupplements.ratePlan')">
      <template #default="{ id }">
        <NativeSelect :id="id" v-model.number="planId" name="rate_plan_id">
          <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </NativeSelect>
      </template>
    </FormField>

    <Card v-if="adding" class="mb-4">
      <form v-autofocus novalidate data-testid="supplement-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ t('bedSupplements.new') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
            <FormField :label="t('bedSupplements.roomType')" :error="fieldError('room_type_id')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="form.room_type_id" name="room_type_id">
                  <option v-for="x in types" :key="x.id" :value="x.id">{{ x.code }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('bedSupplements.bed')" :error="fieldError('bed_type_id')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="form.bed_type_id" name="bed_type_id">
                  <option v-for="x in beds" :key="x.id" :value="x.id">{{ x.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('bedSupplements.kind')" :error="fieldError('adjust_kind')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.adjust_kind" name="adjust_kind">
                  <option value="AMOUNT">{{ t('bedSupplements.kindAmount') }}</option>
                  <option value="PERCENT">{{ t('bedSupplements.kindPercent') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('bedSupplements.value')" :hint="t('bedSupplements.valueHint')" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bedSupplements.from')" :error="fieldError('effective_from')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.effective_from" name="effective_from" type="date" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex gap-2">
            <Button type="submit" :disabled="saving" data-testid="save-supplement">{{ t('common.save') }}</Button>
            <Button type="button" variant="ghost" @click="adding = false">{{ t('common.cancel') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <EmptyState v-if="loaded && !rows.length" :description="t('emptyState.bedSupplements')" :action-label="canManage && !adding && planId ? t('bedSupplements.new') : ''" @action="startAdd" :title="t('bedSupplements.empty')" data-testid="empty" />
    <DataTable v-else :columns="columns" :rows="rows" row-key="id" :caption="t('bedSupplements.title')" :row-test-id="(r: Adjustment) => `supplement-${r.id}`">
      <template #cell-bed="{ row }">{{ row.bed_type_name || row.bed_type_code }}</template>
      <template #cell-supplement="{ row }">{{ supplement(row) }}</template>
      <template #cell-status="{ row }"><Badge v-if="row.in_force" variant="success">{{ t('bedSupplements.inForce') }}</Badge></template>
    </DataTable>
    <p class="muted mt-3 text-sm">{{ t('bedSupplements.note') }}</p>
  </template>
</template>
