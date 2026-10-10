<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { ChargeCode, MealPlan, OccupancyKind, RatePlan } from '@/api/types'
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
const roomCodes = ref<ChargeCode[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<RatePlan | 'new' | null>(null)

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const mealKeys: MealPlan[] = ['RO', 'BB', 'HB', 'FB', 'AI']
const kindKeys: OccupancyKind[] = ['PAID', 'COMPLIMENTARY', 'HOUSE_USE']
const columns = computed<Column<RatePlan>[]>(() => [
  { key: 'code', label: t('ratePlans.code') },
  { key: 'name', label: t('ratePlans.name') },
  { key: 'meal_plan', label: t('ratePlans.mealPlan') },
  { key: 'room_charge_code', label: t('ratePlans.roomChargeCode') },
  { key: 'price_mode', label: t('ratePlans.prices') },
  { key: 'occupancy_kind', label: t('occupancy.kind') },
  { key: 'is_refundable', label: t('ratePlans.refundable') },
  { key: 'status', label: t('setup.status') },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])
const blank = () => ({
  code: '', name: '', description: '', meal_plan: 'RO' as MealPlan, cancellation_policy: '', is_refundable: true, room_charge_code_id: 0, occupancy_kind: 'PAID' as OccupancyKind, is_reference: false, is_active: true,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The codes a plan can sell through: active ROOM codes, plus the plan's current one if it has since become unusable.
const selectable = computed(() =>
  roomCodes.value.filter((c) => (c.is_active && c.charge_type === 'ROOM') || (editing.value !== 'new' && editing.value?.room_charge_code_id === c.id)),
)
const chosenMode = computed(() => roomCodes.value.find((c) => c.id === Number(form.room_charge_code_id))?.price_mode)

async function load(): Promise<void> {
  const propertyId = property.currentId
  plans.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, c] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { ...path, query: { limit: 200, cursor, charge_type: 'ROOM' } } })),
    ])
    plans.value = p
    roomCodes.value = c
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank(), { room_charge_code_id: roomCodes.value.find((c) => c.is_active)?.id ?? 0 })
  error.value = null
  editing.value = 'new'
}

function startEdit(p: RatePlan): void {
  Object.assign(form, blank(), {
    code: p.code, name: p.name, description: p.description ?? '', meal_plan: p.meal_plan, cancellation_policy: p.cancellation_policy ?? '',
    is_refundable: p.is_refundable, room_charge_code_id: p.room_charge_code_id, occupancy_kind: p.occupancy_kind, is_reference: p.is_reference, is_active: p.is_active,
  })
  error.value = null
  editing.value = p
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const common = {
    name: form.name, description: form.description, meal_plan: form.meal_plan, cancellation_policy: form.cancellation_policy,
    is_refundable: form.is_refundable, room_charge_code_id: Number(form.room_charge_code_id), is_active: form.is_active,
  }
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/rate-plans', { params: { path: { propertyId } }, body: { code: form.code, occupancy_kind: form.occupancy_kind, ...common } })
    } else {
      const body = form.occupancy_kind === 'PAID' ? { ...common, is_reference: form.is_reference } : common
      await api.PATCH('/api/v1/properties/{propertyId}/rate-plans/{id}', { params: { path: { propertyId, id: editing.value.id } }, body })
    }
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
  <PageHeader :title="t('ratePlans.title')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" :disabled="!roomCodes.length" data-testid="new-plan" @click="startNew">{{ t('ratePlans.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<span v-if="error.code === 'RATE_PLAN_PRICE_MODE_MISMATCH'"> {{ t('ratePlans.mismatch') }}</span>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('ratePlans.readOnly', { permission: 'rate.manage' }) }}</p>
  <p v-else-if="loaded && !roomCodes.length" class="muted">{{ t('ratePlans.needCode') }}</p>

  <Card v-if="editing" class="mb-4">
    <form v-autofocus="editing" novalidate data-testid="plan-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('ratePlans.new') : t('ratePlans.edit', { code: form.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('ratePlans.code')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('ratePlans.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('ratePlans.mealPlan')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.meal_plan" name="meal_plan">
                <option v-for="k in mealKeys" :key="k" :value="k">{{ k }} · {{ t(`ratePlans.meal_${k}`) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('occupancy.kind')" :error="fieldError('occupancy_kind')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.occupancy_kind" name="occupancy_kind" :disabled="editing !== 'new'">
                <option v-for="k in kindKeys" :key="k" :value="k">{{ t(`occupancy.kind_${k}`) }}</option>
              </NativeSelect>
              <small v-if="form.occupancy_kind !== 'PAID'" class="text-xs text-muted-foreground" data-testid="kind-hint">{{ t('occupancy.kindHint') }}</small>
            </template>
          </FormField>
          <FormField :label="t('ratePlans.roomChargeCode')" :error="fieldError('room_charge_code_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model="form.room_charge_code_id" name="room_charge_code_id" :aria-invalid="invalid">
                <option v-for="c in selectable" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
              </NativeSelect>
              <small v-if="chosenMode" class="text-xs text-muted-foreground" data-testid="mode-hint">
                {{ chosenMode === 'INCLUSIVE' ? t('ratePlans.modeInclusive') : t('ratePlans.modeExclusive') }}
              </small>
            </template>
          </FormField>
          <FormField :label="t('ratePlans.description')">
            <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" /></template>
          </FormField>
          <FormField :label="t('ratePlans.cancellationPolicy')">
            <template #default="{ id }"><Input :id="id" v-model="form.cancellation_policy" name="cancellation_policy" /></template>
          </FormField>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.is_refundable" name="is_refundable" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('ratePlans.refundable') }}</span>
          </label>
          <label v-if="editing !== 'new' && form.occupancy_kind === 'PAID'" class="flex items-center gap-2 text-sm" :title="t('ratePlans.refPlanHint')">
            <input v-model="form.is_reference" name="is_reference" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('ratePlans.refPlan') }}</span>
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('ratePlans.activeCheck') }}</span>
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card>
    <EmptyState v-if="loaded && !plans.length" :description="t('emptyState.ratePlans')" :action-label="canManage && !editing && roomCodes.length ? t('ratePlans.new') : ''" @action="startNew" :title="t('ratePlans.empty')" data-testid="empty" />
    <DataTable v-else-if="plans.length" :columns="columns" :rows="plans" row-key="id" :row-test-id="(p) => `plan-${p.code}`" :caption="t('ratePlans.title')">
      <template #cell-code="{ row }"><b>{{ row.code }}</b> <Badge v-if="row.is_reference" variant="outline" :data-testid="`ref-${row.code}`">{{ t('ratePlans.refBadge') }}</Badge></template>
      <template #cell-price_mode="{ row }">{{ row.price_mode === 'INCLUSIVE' ? t('ratePlans.inclusive') : t('ratePlans.exclusive') }}</template>
      <template #cell-occupancy_kind="{ row }"><Badge v-if="row.occupancy_kind !== 'PAID'" variant="warning" :data-testid="`kind-${row.code}`">{{ t(`occupancy.kind_${row.occupancy_kind}`) }}</Badge><template v-else>{{ t('occupancy.kind_PAID') }}</template></template>
      <template #cell-is_refundable="{ row }">{{ row.is_refundable ? t('common.yes') : t('common.no') }}</template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
      <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
    </DataTable>
  </Card>
</template>
