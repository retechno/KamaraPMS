<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetDetail, AssetLocation } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'

/** One asset (docs/architecture/37-fixed-assets.md 9, mockup 52, the part of PR 1): its data, its values, its fiscal group and the history of the register. The schedule comes with the depreciation. */
const route = useRoute()
const { pid, canRead, canManage } = useAssetAccess()
const assetId = computed(() => Number(route.params.id))
const asset = ref<AssetDetail | null>(null)
const locations = ref<AssetLocation[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/{id}', { params: { path: { propertyId, id: assetId.value } } })
    asset.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, assetId], load, { immediate: true })

const noDepreciation = computed(() => asset.value?.category_kind === 'LAND' || asset.value?.category_kind === 'CIP')
const statusVariant = computed(() => (asset.value?.status === 'ACTIVE' ? 'success' : 'outline'))
const methodText = computed(() => {
  const a = asset.value
  if (!a || noDepreciation.value || !a.method) return t('assets.life.notDepreciated')
  return `${t(`assets.method.${a.method}`)} · ${t('assets.life.months', { n: a.life_months ?? 0 })}`
})
const kindText = (kind: string): string => (te(`assets.ledger.kind.${kind}`) ? t(`assets.ledger.kind.${kind}`) : kind)

// the details
const editing = ref(false)
const busy = ref(false)
const formError = ref<ApiError | null>(null)
const form = ref({ name: '', location_id: 0, department_id: null as number | null, serial_no: '', warranty_until: '', notes: '' })
async function openEdit(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  form.value = { name: a.name, location_id: a.location_id ?? 0, department_id: a.department_id ?? null, serial_no: a.serial_no ?? '', warranty_until: a.warranty_until ?? '', notes: a.notes ?? '' }
  formError.value = null
  editing.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } })
    locations.value = (data?.data ?? []).filter((l) => l.is_active || l.id === a.location_id)
  } catch {
    locations.value = []
  }
}
async function save(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const f = form.value
    const { data } = await api.PATCH('/api/v1/properties/{propertyId}/assets/{id}', {
      params: { path: { propertyId, id: a.id } },
      body: { name: f.name.trim(), location_id: f.location_id || null, department_id: f.department_id, serial_no: f.serial_no.trim() || null, warranty_until: f.warranty_until || null, notes: f.notes.trim() || null, version: a.version },
    })
    if (data) asset.value = data
    editing.value = false
    toast.success(t('assets.asset.saved'))
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// cancel
const cancelling = ref(false)
const reason = ref('')
async function cancelAsset(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/cancel', { params: { path: { propertyId, id: a.id } }, body: { reason: reason.value.trim() } })
    if (data) asset.value = data
    cancelling.value = false
    toast.success(t('assets.asset.cancelled'))
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else-if="asset">
    <RouterLink to="/assets" class="text-sm text-primary hover:underline" data-testid="back">← {{ t('assets.register.title') }}</RouterLink>
    <PageHeader :title="asset.name" :description="`${asset.number} · ${asset.category_name}`">
      <template #actions>
        <Badge :variant="statusVariant" data-testid="asset-status">{{ t(`assets.status.${asset.status}`) }}</Badge>
        <Button v-if="canManage && asset.status === 'ACTIVE'" variant="outline" data-testid="asset-edit" @click="openEdit">{{ t('assets.asset.edit') }}</Button>
        <Button v-if="canManage && asset.status === 'ACTIVE'" variant="outline" data-testid="asset-cancel" @click="cancelling = true; formError = null; reason = ''">{{ t('assets.asset.cancel') }}</Button>
        <Button as-child variant="outline"><RouterLink :to="{ path: '/assets/labels', query: { ids: String(asset.id) } }" data-testid="asset-label">{{ t('assets.asset.label') }}</RouterLink></Button>
      </template>
    </PageHeader>
    <p class="text-sm text-muted-foreground" data-testid="asset-source">
      <template v-if="asset.source === 'OPENING'">{{ t('assets.asset.fromOpening', { number: asset.opening_import_number ?? '' }) }}</template>
      <template v-else>{{ t('assets.asset.fromJournal') }}</template>
    </p>
    <div class="grid gap-4 lg:grid-cols-2">
      <Card class="flex flex-col gap-1 p-4" data-testid="asset-data">
        <h2 class="m-0 mb-1 text-base font-semibold">{{ t('assets.asset.data') }}</h2>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm">
          <dt class="text-muted-foreground">{{ t('assets.col.place') }}</dt><dd class="m-0">{{ asset.location_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.col.department') }}</dt><dd class="m-0">{{ asset.department_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.supplier') }}</dt><dd class="m-0">{{ asset.supplier_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.serial') }}</dt><dd class="m-0">{{ asset.serial_no ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.acquired') }}</dt><dd class="m-0">{{ formatDate(asset.acquisition_date) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.col.inService') }}</dt><dd class="m-0">{{ formatDate(asset.in_service_date) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.depStart') }}</dt><dd class="m-0">{{ noDepreciation ? '–' : asset.first_dep_month ? formatMonth(asset.first_dep_month) : '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.warranty') }}</dt><dd class="m-0">{{ asset.warranty_until ? formatDate(asset.warranty_until) : '–' }}</dd>
          <template v-if="asset.notes"><dt class="text-muted-foreground">{{ t('assets.asset.notes') }}</dt><dd class="m-0">{{ asset.notes }}</dd></template>
        </dl>
      </Card>
      <Card class="flex flex-col gap-1 p-4" data-testid="asset-values">
        <h2 class="m-0 mb-1 text-base font-semibold">{{ t('assets.asset.values') }}</h2>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm tabular-nums">
          <dt class="text-muted-foreground">{{ t('assets.col.cost') }}</dt><dd class="m-0 font-semibold" data-testid="value-cost">{{ formatMoney(asset.cost) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.residual') }}</dt><dd class="m-0">{{ formatMoney(asset.residual_value) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.methodLife') }}</dt><dd class="m-0">{{ methodText }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.accumMonths', { n: asset.dep_months_done }) }}</dt><dd class="m-0" data-testid="value-accum">{{ formatMoney(asset.accum_depr) }}</dd>
          <dt class="font-semibold">{{ t('assets.col.carrying') }}</dt><dd class="m-0 font-semibold" data-testid="value-carrying">{{ formatMoney(asset.carrying_value) }}</dd>
          <template v-if="asset.fully_depreciated"><dt /><dd class="m-0"><Badge variant="outline" data-testid="fully-depreciated">{{ t('assets.life.fullyDepreciated') }}</Badge></dd></template>
          <template v-if="asset.unit_count > 1"><dt class="text-muted-foreground">{{ t('assets.asset.unitCount') }}</dt><dd class="m-0">{{ asset.unit_count }}</dd></template>
        </dl>
        <h3 class="m-0 mt-3 text-sm font-semibold">{{ t('assets.asset.fiscal') }}</h3>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm tabular-nums" data-testid="asset-fiscal">
          <dt class="text-muted-foreground">{{ t('assets.asset.fiscalGroup') }}</dt><dd class="m-0">{{ asset.fiscal_group === 'NONE' ? '–' : `${t(`assets.fiscalGroup.${asset.fiscal_group}`)}${asset.fiscal_method ? ` · ${t(`assets.method.${asset.fiscal_method}`)}` : ''}` }}</dd>
          <template v-if="asset.fiscal_accum_opening !== null && asset.fiscal_accum_opening !== undefined">
            <dt class="text-muted-foreground">{{ t('assets.asset.fiscalOpening', { date: formatDate(asset.fiscal_opening_as_of) }) }}</dt>
            <dd class="m-0">{{ formatMoney(asset.fiscal_accum_opening) }}<Badge v-if="asset.fiscal_opening_estimated" variant="warning" class="ml-2" data-testid="fiscal-estimated">{{ t('assets.asset.fiscalEstimated') }}</Badge></dd>
          </template>
        </dl>
      </Card>
    </div>
    <Card class="flex flex-col gap-2 p-4" data-testid="asset-history">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.asset.history') }}</h2>
      <p v-if="!asset.ledger.length" class="muted m-0">{{ t('assets.asset.noHistory') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
        <li v-for="e in asset.ledger" :key="e.id" class="flex flex-wrap items-baseline gap-x-3 border-b border-border py-1" :data-testid="`ledger-${e.id}`">
          <b class="w-28 font-medium">{{ formatDate(e.event_date) }}</b>
          <span class="flex-1">{{ kindText(e.kind) }}</span>
          <span class="tabular-nums">{{ t('assets.asset.costDelta') }} {{ formatMoney(e.cost_delta) }}</span>
          <span class="tabular-nums">{{ t('assets.asset.accumDelta') }} {{ formatMoney(e.accum_delta) }}</span>
        </li>
      </ul>
    </Card>
  </template>
  <p v-else-if="loaded && !error" class="muted">{{ t('assets.asset.notFound') }}</p>

  <Dialog :open="editing" @update:open="(v) => (editing = v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="edit-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ t('assets.asset.edit') }}</DialogTitle>
        <DialogDescription>{{ t('assets.asset.editHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.name')" required :error="formError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="150" /></template></FormField>
        <FormField :label="t('assets.col.place')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.location_id" name="location_id">
              <option :value="0">–</option>
              <option v-for="l in locations" :key="l.id" :value="l.id">{{ l.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('assets.col.department')"><template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" /></template></FormField>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('assets.asset.serial')"><template #default="{ id }"><Input :id="id" v-model="form.serial_no" name="serial_no" maxlength="100" /></template></FormField>
          <FormField :label="t('assets.asset.warranty')" :error="formError?.fieldMessage('warranty_until')"><template #default="{ id }"><Input :id="id" v-model="form.warranty_until" name="warranty_until" type="date" /></template></FormField>
        </div>
        <FormField :label="t('assets.asset.notes')"><template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="editing = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy || !form.name.trim()" data-testid="edit-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="cancelling" @update:open="(v) => (cancelling = v)">
    <DialogContent class="max-w-md p-5" data-testid="cancel-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="cancelAsset">
        <DialogTitle>{{ t('assets.asset.cancelTitle') }}</DialogTitle>
        <DialogDescription>{{ t('assets.asset.cancelHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.reason')" required :error="formError?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="300" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="cancelling = false">{{ t('common.cancel') }}</Button><Button type="submit" variant="destructive" :disabled="busy || !reason.trim()" data-testid="cancel-confirm">{{ t('assets.asset.cancel') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
