<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Asset, AssetPage, AssetWaitingLine } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'

/** Add the value of a journal line to an asset that exists (docs/architecture/37-fixed-assets.md 4.5, "Tambah nilai", example 10.8): the line already debits the cost account, so no journal is posted; the cost grows and the depreciation goes on from the next run. */
const props = defineProps<{ line: AssetWaitingLine | null; propertyId: number; presetAssetId?: number | null }>()
const emit = defineEmits<{ close: []; done: [] }>()

const busy = ref(false)
const error = ref<ApiError | null>(null)
const candidates = ref<Asset[]>([])
const form = ref({ asset_id: 0, reason: '' })
let key = crypto.randomUUID()

watch(() => props.line, async (l) => {
  if (!l) return
  form.value = { asset_id: props.presetAssetId ?? 0, reason: '' }
  error.value = null
  key = crypto.randomUUID()
  try {
    const res = await api.GET('/api/v1/properties/{propertyId}/assets', { params: { path: { propertyId: props.propertyId }, query: { category_id: l.category_id, status: 'ACTIVE', limit: 200 } as never } })
    candidates.value = (res.data as unknown as AssetPage | undefined)?.data ?? []
  } catch (e) {
    candidates.value = []
    error.value = e instanceof ApiError ? e : null
  }
}, { immediate: true })
watch(form, () => { key = crypto.randomUUID() }, { deep: true })

const target = computed(() => candidates.value.find((a) => a.id === form.value.asset_id) ?? null)
const valid = computed(() => form.value.asset_id > 0 && form.value.reason.trim() !== '')

async function submit(): Promise<void> {
  const l = props.line
  if (!l || !target.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/{id}/revisions', {
      params: { path: { propertyId: props.propertyId, id: target.value.id }, header: { 'Idempotency-Key': key } },
      body: { kind: 'CAPITALIZE', reason: form.value.reason.trim(), journal_line_id: l.line_id },
    })
    key = crypto.randomUUID()
    toast.success(t('assets.addValue.done', { asset: target.value.number }))
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
    <DialogContent v-if="line" class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="addvalue-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ t('assets.addValue.title') }}</DialogTitle>
        <DialogDescription data-testid="addvalue-source">{{ t(`assets.waiting.source.${line.source}`) }} {{ line.bill_number ?? line.document_number }} · {{ formatDate(line.journal_date) }} · {{ t('assets.make.account', { account: line.account_code }) }} · {{ t('assets.make.cost', { cost: formatMoney(line.cost) }) }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField :label="t('assets.addValue.asset')" required :error="error?.fieldMessage('asset_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.asset_id" name="asset_id" data-testid="addvalue-asset">
              <option :value="0">–</option>
              <option v-for="a in candidates" :key="a.id" :value="a.id">{{ a.number }} · {{ a.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <p v-if="!candidates.length" class="m-0 text-xs text-muted-foreground" data-testid="addvalue-none">{{ t('assets.addValue.none') }}</p>
        <dl v-if="target" class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm tabular-nums" data-testid="addvalue-effect">
          <dt class="text-muted-foreground">{{ t('assets.col.cost') }}</dt><dd class="m-0">{{ formatMoney(target.cost) }} → <b>{{ formatMoney((Number(target.cost) + Number(line.cost)).toString()) }}</b></dd>
          <dt class="text-muted-foreground">{{ t('assets.col.carrying') }}</dt><dd class="m-0">{{ formatMoney(target.carrying_value) }} → <b>{{ formatMoney((Number(target.carrying_value) + Number(line.cost)).toString()) }}</b></dd>
        </dl>
        <FormField :label="t('assets.asset.reason')" required :error="error?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="form.reason" name="reason" maxlength="300" /></template></FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ t('assets.addValue.note') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !valid" data-testid="addvalue-confirm">{{ t('assets.addValue.confirm') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
