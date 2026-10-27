<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetLocation, AssetWaitingLine } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'
import { monthlyStraightLine, splitCost } from './split'

/** Make one or more assets of a journal line (docs/architecture/37-fixed-assets.md 4.2.2 to 4.2.4, mockup 54): no journal is posted, the line already debits the cost account. */
const props = defineProps<{ line: AssetWaitingLine | null; categories: AssetCategory[]; locations: AssetLocation[]; threshold: string; propertyId: number }>()
const emit = defineEmits<{ close: []; done: [] }>()
const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)

const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = ref({ name: '', units: 1, category_id: 0, location_id: 0, department_id: null as number | null, in_service_date: '', start_rule: 'IN_SERVICE_MONTH', unit_count: '', serial_no: '', notes: '' })
let key = crypto.randomUUID()

watch(() => props.line, (l) => {
  if (!l) return
  const c = props.categories.find((x) => x.id === l.category_id)
  form.value = { name: l.description, units: 1, category_id: l.category_id, location_id: 0, department_id: l.department_id ?? c?.department_id ?? null, in_service_date: l.journal_date, start_rule: 'IN_SERVICE_MONTH', unit_count: '', serial_no: '', notes: '' }
  error.value = null
  key = crypto.randomUUID()
}, { immediate: true })
// a change of the input is a new request
watch(form, () => { key = crypto.randomUUID() }, { deep: true })

const category = computed(() => props.categories.find((c) => c.id === form.value.category_id))
const isBatch = computed(() => category.value?.kind === 'OE')
const units = computed(() => Math.max(1, Math.min(200, Math.floor(Number(form.value.units) || 1))))
const costs = computed(() => (props.line ? splitCost(props.line.cost, units.value, decimals.value) : []))
const perMonth = (cost: string): string => {
  const c = category.value
  if (!c || !c.is_depreciable || c.method !== 'SL' || !c.life_months) return '–'
  return formatMoney(monthlyStraightLine(cost, c.residual_pct, c.life_months, decimals.value))
}
const nextMonth = computed(() => {
  const m = /^(\d{4})-(\d{2})/.exec(form.value.in_service_date)
  if (!m) return ''
  const idx = Number(m[1]) * 12 + Number(m[2]) // the month after, 1-based month in 0-based arithmetic
  return `${Math.floor(idx / 12)}-${String((idx % 12) + 1).padStart(2, '0')}`
})
const hasThreshold = computed(() => Number(props.threshold) > 0)
const belowThreshold = computed(() => hasThreshold.value && costs.value.some((c) => Number(c) < Number(props.threshold)))
const depreciable = computed(() => !!category.value?.is_depreciable)
const valid = computed(() => form.value.name.trim() !== '' && form.value.in_service_date !== '' && form.value.category_id > 0)

async function submit(): Promise<void> {
  const l = props.line
  if (!l) return
  busy.value = true
  error.value = null
  const f = form.value
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/from-line', {
      params: { path: { propertyId: props.propertyId }, header: { 'Idempotency-Key': key } },
      body: {
        journal_line_id: l.line_id, units: units.value, name: f.name.trim(), category_id: f.category_id || null, location_id: f.location_id || null, department_id: f.department_id,
        in_service_date: f.in_service_date || null, start_rule: f.start_rule, unit_count: isBatch.value && f.unit_count.trim() ? Number(f.unit_count) : null,
        serial_no: f.serial_no.trim() || null, notes: f.notes.trim() || null,
      },
    })
    key = crypto.randomUUID()
    toast.success(t('assets.make.done', { n: data?.assets.length ?? units.value }))
    emit('done')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="!!line" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent v-if="line" class="max-h-[90dvh] max-w-2xl overflow-y-auto p-5" data-testid="make-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ isBatch ? t('assets.make.titleBatch') : t('assets.make.title') }}</DialogTitle>
        <DialogDescription data-testid="make-source">
          {{ t(`assets.waiting.source.${line.source}`) }} {{ line.bill_number ?? line.document_number }}<template v-if="line.supplier_name"> · {{ line.supplier_name }}</template> · {{ formatDate(line.journal_date) }} · {{ t('assets.make.account', { account: line.account_code }) }} · {{ t('assets.make.cost', { cost: formatMoney(line.cost) }) }}
        </DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <div class="grid gap-3 sm:grid-cols-[1fr_8rem]">
          <FormField :label="t('assets.asset.name')" required :error="error?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="150" /></template></FormField>
          <FormField :label="t('assets.make.units')" :error="error?.fieldMessage('units')"><template #default="{ id }"><Input :id="id" v-model.number="form.units" name="units" type="number" min="1" max="200" inputmode="numeric" /></template></FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('assets.col.category')" required>
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.category_id" name="category_id">
                <option v-for="c in categories.filter((x) => x.is_active)" :key="c.id" :value="c.id">{{ c.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('assets.col.place')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.location_id" name="location_id">
                <option :value="0">–</option>
                <option v-for="l in locations.filter((x) => x.is_active)" :key="l.id" :value="l.id">{{ l.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('assets.col.department')" :error="error?.fieldMessage('department_id')"><template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" /></template></FormField>
          <FormField :label="t('assets.make.inServiceDate')" required :error="error?.fieldMessage('in_service_date')"><template #default="{ id }"><Input :id="id" v-model="form.in_service_date" name="in_service_date" type="date" /></template></FormField>
        </div>
        <fieldset v-if="depreciable" class="m-0 flex flex-col gap-1 border-0 p-0" :aria-label="t('assets.make.startRule')">
          <legend class="mb-1 p-0 text-sm font-medium">{{ t('assets.make.startRule') }}</legend>
          <label class="flex cursor-pointer items-center gap-2 text-sm"><input v-model="form.start_rule" type="radio" name="start_rule" value="IN_SERVICE_MONTH" class="accent-primary" /> {{ t('assets.make.startInService', { month: formatMonth(form.in_service_date.slice(0, 7)) }) }}</label>
          <label class="flex cursor-pointer items-center gap-2 text-sm"><input v-model="form.start_rule" type="radio" name="start_rule" value="NEXT_MONTH" class="accent-primary" /> {{ t('assets.make.startNext', { month: formatMonth(nextMonth) }) }}</label>
          <p class="m-0 text-xs text-muted-foreground">{{ t('assets.make.startHint') }}</p>
        </fieldset>
        <div v-if="isBatch" class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('assets.opening.unitCount')" :hint="t('assets.make.batchHint')" :error="error?.fieldMessage('unit_count')"><template #default="{ id }"><Input :id="id" v-model="form.unit_count" name="unit_count" inputmode="numeric" /></template></FormField>
        </div>
        <div v-if="units > 1" class="overflow-x-auto" data-testid="make-units">
          <table class="w-full border-collapse text-sm">
            <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-1">{{ t('assets.make.unit') }}</th><th class="p-1 text-right">{{ t('assets.col.cost') }}</th><th class="p-1 text-right">{{ t('assets.make.perMonth') }}</th></tr></thead>
            <tbody>
              <tr v-for="(c, i) in costs" :key="i" class="border-t border-border" :data-testid="`make-unit-${i + 1}`"><td class="p-1">{{ i + 1 }}</td><td class="p-1 text-right tabular-nums">{{ formatMoney(c) }}</td><td class="p-1 text-right tabular-nums">{{ perMonth(c) }}</td></tr>
            </tbody>
          </table>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('assets.make.splitHint') }}</p>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('assets.asset.serial')"><template #default="{ id }"><Input :id="id" v-model="form.serial_no" name="serial_no" maxlength="100" /></template></FormField>
          <FormField :label="t('assets.asset.notes')"><template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template></FormField>
        </div>
        <p v-if="belowThreshold" class="alert warning m-0" role="status" data-testid="make-threshold">{{ t('assets.make.belowThreshold', { threshold: formatMoney(threshold) }) }}</p>
        <p v-else class="m-0 text-xs text-muted-foreground">{{ t('assets.make.noJournal', { account: line.account_code }) }} <Badge v-if="hasThreshold" variant="outline" data-testid="make-above">{{ t('assets.make.aboveThreshold', { threshold: formatMoney(threshold) }) }}</Badge></p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !valid" data-testid="make-confirm">{{ units > 1 ? t('assets.make.createN', { n: units }) : t('assets.make.create') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
