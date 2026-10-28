<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetDetail } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'

/**
 * Change the life, the residual value or the method of an asset, or move it to another category (docs/architecture/37-fixed-assets.md 4.5): prospective, from the next run, nothing posted is rewritten.
 * A construction in progress that is finished takes the same route: it moves to the category of the finished asset, with the date it was put in service.
 */
const props = defineProps<{ asset: AssetDetail | null; categories: AssetCategory[]; propertyId: number; initialKind: 'TERMS' | 'CATEGORY' }>()
const emit = defineEmits<{ close: []; done: [asset: AssetDetail] }>()

type Kind = 'LIFE' | 'RESIDUAL' | 'METHOD' | 'CATEGORY'
const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = ref({ kind: 'LIFE' as Kind, reason: '', life_months: '', residual_value: '', method: 'SL', db_factor: '2', category_id: 0, in_service_date: '', start_rule: 'IN_SERVICE_MONTH' })
let key = crypto.randomUUID()

const isCip = computed(() => props.asset?.category_kind === 'CIP')
const kinds = computed<Kind[]>(() => (isCip.value ? ['CATEGORY'] : ['LIFE', 'RESIDUAL', 'METHOD', 'CATEGORY']))

watch(() => props.asset, (a) => {
  if (!a) return
  const cip = a.category_kind === 'CIP'
  form.value = {
    kind: cip || props.initialKind === 'CATEGORY' ? 'CATEGORY' : 'LIFE', reason: '', life_months: a.life_months ? String(a.life_months) : '', residual_value: a.residual_value, method: a.method ?? 'SL', db_factor: a.db_factor ?? '2',
    category_id: 0, in_service_date: a.in_service_date, start_rule: 'IN_SERVICE_MONTH',
  }
  error.value = null
  key = crypto.randomUUID()
}, { immediate: true })
watch(() => props.initialKind, (k) => { if (props.asset && !isCip.value) form.value.kind = k === 'CATEGORY' ? 'CATEGORY' : 'LIFE' })
watch(form, () => { key = crypto.randomUUID() }, { deep: true })

const targets = computed(() => props.categories.filter((c) => c.is_active && c.id !== props.asset?.category_id))
const target = computed(() => props.categories.find((c) => c.id === form.value.category_id))
const valid = computed(() => {
  const f = form.value
  if (f.reason.trim() === '') return false
  if (f.kind === 'LIFE') return Number(f.life_months) > 0
  if (f.kind === 'RESIDUAL') return f.residual_value.trim() !== ''
  if (f.kind === 'METHOD') return f.method === 'SL' || Number(f.db_factor) > 0
  return f.category_id > 0 && (!isCip.value || f.in_service_date !== '')
})

async function submit(): Promise<void> {
  const a = props.asset
  if (!a) return
  busy.value = true
  error.value = null
  const f = form.value
  const body: Record<string, unknown> = { kind: f.kind, reason: f.reason.trim() }
  if (f.kind === 'LIFE') body.life_months = Number(f.life_months)
  if (f.kind === 'RESIDUAL') body.residual_value = f.residual_value.trim()
  if (f.kind === 'METHOD') {
    body.method = f.method
    if (f.method === 'DB') body.db_factor = f.db_factor.trim()
  }
  if (f.kind === 'CATEGORY') {
    body.category_id = f.category_id
    if (isCip.value) {
      body.in_service_date = f.in_service_date
      body.start_rule = f.start_rule
    }
  }
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/revisions', {
      params: { path: { propertyId: props.propertyId, id: a.id }, header: { 'Idempotency-Key': key } },
      body: body as never,
    })
    key = crypto.randomUUID()
    toast.success(t(isCip.value ? 'assets.revise.doneCip' : 'assets.revise.done'))
    if (data) emit('done', data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="!!asset" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent v-if="asset" class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="revise-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ isCip ? t('assets.revise.titleCip') : t('assets.revise.title') }}</DialogTitle>
        <DialogDescription>{{ isCip ? t('assets.revise.hintCip') : t('assets.revise.hint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField v-if="!isCip" :label="t('assets.revise.what')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.kind" name="kind" data-testid="revise-kind">
              <option v-for="k in kinds" :key="k" :value="k">{{ t(`assets.revise.kind.${k}`) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <template v-if="form.kind === 'LIFE'">
          <FormField :label="t('assets.revise.life', { now: asset.life_months ?? 0, done: asset.dep_months_done })" required :error="error?.fieldMessage('life_months')"><template #default="{ id }"><Input :id="id" v-model="form.life_months" name="life_months" inputmode="numeric" data-testid="revise-life" /></template></FormField>
        </template>
        <template v-if="form.kind === 'RESIDUAL'">
          <FormField :label="t('assets.revise.residual', { now: formatMoney(asset.residual_value) })" required :error="error?.fieldMessage('residual_value')"><template #default="{ id }"><Input :id="id" v-model="form.residual_value" name="residual_value" inputmode="decimal" data-testid="revise-residual" /></template></FormField>
        </template>
        <template v-if="form.kind === 'METHOD'">
          <div class="grid gap-3 sm:grid-cols-2">
            <FormField :label="t('assets.revise.method')">
              <template #default="{ id }"><NativeSelect :id="id" v-model="form.method" name="method" data-testid="revise-method"><option v-for="m in ['SL', 'DB']" :key="m" :value="m">{{ t(`assets.method.${m}`) }}</option></NativeSelect></template>
            </FormField>
            <FormField v-if="form.method === 'DB'" :label="t('assets.revise.factor')" :error="error?.fieldMessage('db_factor')"><template #default="{ id }"><Input :id="id" v-model="form.db_factor" name="db_factor" inputmode="decimal" /></template></FormField>
          </div>
        </template>
        <template v-if="form.kind === 'CATEGORY'">
          <FormField :label="isCip ? t('assets.revise.finishedCategory') : t('assets.revise.newCategory', { now: asset.category_name })" required :error="error?.fieldMessage('category_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.category_id" name="category_id" data-testid="revise-category">
                <option :value="0">–</option>
                <option v-for="c in targets" :key="c.id" :value="c.id">{{ c.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <div v-if="isCip" class="grid gap-3 sm:grid-cols-2" data-testid="revise-cip">
            <FormField :label="t('assets.make.inServiceDate')" required :error="error?.fieldMessage('in_service_date')"><template #default="{ id }"><Input :id="id" v-model="form.in_service_date" name="in_service_date" type="date" /></template></FormField>
            <FormField :label="t('assets.make.startRule')" :error="error?.fieldMessage('start_rule')">
              <template #default="{ id }"><NativeSelect :id="id" v-model="form.start_rule" name="start_rule"><option v-for="r in ['IN_SERVICE_MONTH', 'NEXT_MONTH']" :key="r" :value="r">{{ t(`assets.startRule.${r}`) }}</option></NativeSelect></template>
            </FormField>
          </div>
          <p v-if="target" class="m-0 text-xs text-muted-foreground" data-testid="revise-journal-note">{{ t('assets.revise.journalNote', { from: asset.category_name, to: target.name }) }}</p>
        </template>
        <FormField :label="t('assets.asset.reason')" required :error="error?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="form.reason" name="reason" maxlength="300" /></template></FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ t('assets.revise.prospective') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !valid" data-testid="revise-confirm">{{ t('assets.revise.confirm') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
