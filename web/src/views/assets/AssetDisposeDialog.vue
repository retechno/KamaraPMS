<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetAccountOption, AssetDetail, AssetDisposalPreview, AssetDisposeRequest } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useApprovalRulesStore } from '@/stores/approvalRules'
import { usePropertyStore } from '@/stores/property'
import { toMinor } from '@/utils/checkin'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * Sell, write off or lose an asset, or some pieces of a batch (docs/architecture/37-fixed-assets.md 4.7, mockup 57). The preview is worked out by the server as the form changes: the amounts, the journal and the amount the
 * approval follows. The disposal posts at once when the rule lets the person; otherwise it becomes a request that waits for another user, and the button to approve at once is disabled with the reason.
 */
const props = defineProps<{ asset: AssetDetail | null; propertyId: number }>()
const emit = defineEmits<{ close: []; done: [status: 'POSTED' | 'PENDING'] }>()

const rules = useApprovalRulesStore()
const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const previewError = ref<ApiError | null>(null)
const preview = ref<AssetDisposalPreview | null>(null)
const accounts = ref<AssetAccountOption[]>([])
const form = ref({ kind: 'SALE' as 'SALE' | 'WRITE_OFF', reason_code: 'SOLD', disposal_date: '', units: 1, proceeds: '', proceeds_account_id: 0, note: '' })
let key = crypto.randomUUID()
let timer: ReturnType<typeof setTimeout> | undefined

const WRITE_OFF_REASONS = ['DAMAGED', 'LOST', 'OBSOLETE', 'DONATED', 'OTHER']
const isBatch = computed(() => (props.asset?.unit_count ?? 1) > 1)
const proceedsAccounts = computed(() => accounts.value.filter((a) => a.statement_group === 'CASH' || a.code === '1230'))

watch(() => props.asset, async (a) => {
  if (!a) return
  form.value = { kind: 'SALE', reason_code: 'SOLD', disposal_date: '', units: a.unit_count, proceeds: '', proceeds_account_id: 0, note: '' }
  error.value = null
  previewError.value = null
  preview.value = null
  key = crypto.randomUUID()
  void rules.load(props.propertyId)
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/accounts', { params: { path: { propertyId: props.propertyId }, query: { type: 'ASSET' } } })
    accounts.value = data?.data ?? []
  } catch {
    accounts.value = []
  }
}, { immediate: true })

function setKind(kind: 'SALE' | 'WRITE_OFF'): void {
  form.value.kind = kind
  form.value.reason_code = kind === 'SALE' ? 'SOLD' : 'DAMAGED'
  if (kind === 'WRITE_OFF') {
    form.value.proceeds = ''
    form.value.proceeds_account_id = 0
  }
}

const units = computed(() => Math.max(1, Math.min(props.asset?.unit_count ?? 1, Math.floor(Number(form.value.units) || 1))))
const complete = computed(() => {
  const f = form.value
  if (f.kind === 'WRITE_OFF') return f.reason_code !== 'OTHER' || f.note.trim() !== ''
  return Number(f.proceeds) > 0 && f.proceeds_account_id > 0
})

function body(): AssetDisposeRequest {
  const f = form.value
  return {
    kind: f.kind, reason_code: f.reason_code as AssetDisposeRequest['reason_code'], disposal_date: f.disposal_date || null, units: isBatch.value ? units.value : null,
    proceeds: f.kind === 'SALE' ? f.proceeds.trim() : null, proceeds_account_id: f.kind === 'SALE' ? f.proceeds_account_id || null : null, note: f.note.trim(),
  }
}

async function loadPreview(): Promise<void> {
  const a = props.asset
  if (!a || !complete.value) {
    preview.value = null
    return
  }
  previewError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/dispose/preview', { params: { path: { propertyId: props.propertyId, id: a.id } }, body: body() })
    preview.value = data ?? null
  } catch (e) {
    preview.value = null
    previewError.value = e instanceof ApiError ? e : null
  }
}
// the form changed: a new request, and a new preview after a short pause
watch(form, () => {
  key = crypto.randomUUID()
  clearTimeout(timer)
  timer = setTimeout(() => void loadPreview(), 350)
}, { deep: true })
onBeforeUnmount(() => clearTimeout(timer))

// What the rule says for the amount of the approval: nothing, someone else, or someone else above the limit.
const ruleLine = computed<'none' | 'distinct' | 'above' | 'within' | 'limit'>(() => {
  const r = rules.ruleOf('ASSET_DISPOSAL')
  if (!r || r.rule === 'FREE') return 'none'
  if (r.rule === 'DISTINCT') return 'distinct'
  if (!preview.value) return 'limit'
  return toMinor(preview.value.approval_amount, 3) > toMinor(r.threshold, 3) ? 'above' : 'within'
})
const limit = computed(() => formatMoney(rules.ruleOf('ASSET_DISPOSAL')?.threshold ?? '0'))
const needsOther = computed(() => ruleLine.value === 'distinct' || ruleLine.value === 'above')
const isLoss = computed(() => !!preview.value && toMinor(preview.value.gain_loss, 3) < 0n)

async function submit(): Promise<void> {
  const a = props.asset
  if (!a) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/dispose', { params: { path: { propertyId: props.propertyId, id: a.id }, header: { 'Idempotency-Key': key } }, body: body() })
    key = crypto.randomUUID()
    const status = data?.status ?? 'POSTED'
    toast.success(status === 'PENDING' ? t('assets.dispose.pending') : t('assets.dispose.posted', { number: data?.disposal?.number ?? '' }))
    emit('done', status)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="!!asset" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent v-if="asset" class="max-h-[92dvh] max-w-3xl overflow-y-auto p-5" data-testid="dispose-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit()">
        <DialogTitle>{{ t('assets.dispose.title') }}</DialogTitle>
        <DialogDescription data-testid="dispose-asset">{{ asset.number }} · {{ asset.name }} · {{ t('assets.dispose.values', { cost: formatMoney(asset.cost), accum: formatMoney(asset.accum_depr), carrying: formatMoney(asset.carrying_value) }) }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <fieldset class="m-0 flex flex-wrap gap-4 border-0 p-0" :aria-label="t('assets.dispose.kind')">
          <label class="flex cursor-pointer items-center gap-2 text-sm"><input type="radio" name="kind" value="SALE" class="accent-primary" :checked="form.kind === 'SALE'" data-testid="dispose-sale" @change="setKind('SALE')" /> {{ t('assets.dispose.sale') }}</label>
          <label class="flex cursor-pointer items-center gap-2 text-sm"><input type="radio" name="kind" value="WRITE_OFF" class="accent-primary" :checked="form.kind === 'WRITE_OFF'" data-testid="dispose-writeoff" @change="setKind('WRITE_OFF')" /> {{ t('assets.dispose.writeOff') }}</label>
        </fieldset>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField v-if="form.kind === 'WRITE_OFF'" :label="t('assets.dispose.reason')" :error="error?.fieldMessage('reason_code')">
            <template #default="{ id }"><NativeSelect :id="id" v-model="form.reason_code" name="reason_code" data-testid="dispose-reason"><option v-for="r in WRITE_OFF_REASONS" :key="r" :value="r">{{ t(`assets.dispose.reasonCode.${r}`) }}</option></NativeSelect></template>
          </FormField>
          <FormField :label="t('assets.dispose.date')" :hint="t('assets.dispose.dateHint')" :error="error?.fieldMessage('disposal_date')"><template #default="{ id }"><Input :id="id" v-model="form.disposal_date" name="disposal_date" type="date" /></template></FormField>
          <FormField v-if="isBatch" :label="t('assets.dispose.units', { n: asset.unit_count })" :error="error?.fieldMessage('units')"><template #default="{ id }"><Input :id="id" v-model.number="form.units" name="units" type="number" min="1" :max="asset.unit_count" inputmode="numeric" data-testid="dispose-units" /></template></FormField>
        </div>
        <div v-if="form.kind === 'SALE'" class="grid gap-3 sm:grid-cols-2" data-testid="dispose-proceeds">
          <FormField :label="t('assets.dispose.proceeds')" required :error="error?.fieldMessage('proceeds')"><template #default="{ id }"><MoneyInput :id="id" v-model="form.proceeds" :decimals="decimals" name="proceeds" data-testid="dispose-price" /></template></FormField>
          <FormField :label="t('assets.dispose.account')" required :error="error?.fieldMessage('proceeds_account_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.proceeds_account_id" name="proceeds_account_id" data-testid="dispose-account">
                <option :value="0">–</option>
                <option v-for="a in proceedsAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </div>
        <FormField :label="t('assets.dispose.note')" :error="error?.fieldMessage('note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>

        <ErrorNotice v-if="previewError" :error="previewError" inline />
        <section v-if="preview" class="flex flex-col gap-2 rounded-lg border border-border p-3" data-testid="dispose-preview">
          <h3 class="m-0 text-sm font-semibold">{{ t('assets.dispose.preview', { date: formatDate(preview.disposal_date) }) }}</h3>
          <dl class="m-0 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm tabular-nums sm:max-w-md">
            <dt class="text-muted-foreground">{{ t('assets.col.cost') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.cost_removed) }}</dd>
            <dt class="text-muted-foreground">{{ t('assets.col.accum') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.accum_removed) }}</dd>
            <dt class="text-muted-foreground">{{ t('assets.col.carrying') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.carrying_removed) }}</dd>
            <dt class="text-muted-foreground">{{ t('assets.dispose.proceeds') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.proceeds) }}</dd>
            <dt class="font-semibold">{{ isLoss ? t('assets.dispose.loss') : t('assets.dispose.gain') }}</dt><dd class="m-0 text-right font-semibold" data-testid="dispose-gain">{{ formatMoney(preview.gain_loss) }}</dd>
          </dl>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm tabular-nums" data-testid="dispose-journal">
              <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-1">{{ t('assets.depr.col.account') }}</th><th class="p-1 text-right">{{ t('assets.depr.col.debit') }}</th><th class="p-1 text-right">{{ t('assets.depr.col.credit') }}</th></tr></thead>
              <tbody>
                <tr v-for="(l, i) in preview.lines" :key="i" class="border-t border-border" :data-testid="`dispose-line-${l.account_code}`">
                  <td class="p-1">{{ l.account_code }} · {{ l.account_name }}</td>
                  <td class="p-1 text-right">{{ Number(l.debit) ? formatMoney(l.debit) : '' }}</td><td class="p-1 text-right">{{ Number(l.credit) ? formatMoney(l.credit) : '' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="m-0 text-sm" :class="needsOther ? 'text-warning-foreground' : 'text-muted-foreground'" data-testid="dispose-approval">
            {{ t(`assets.dispose.rule.${ruleLine}`, { amount: formatMoney(preview.approval_amount), limit }) }}
          </p>
          <p v-if="form.kind === 'SALE'" class="alert warning m-0" role="status" data-testid="dispose-vat">{{ preview.pkp_on_date ? t('assets.dispose.vatPkp') : t('assets.dispose.vat') }}</p>
        </section>
        <p v-else class="m-0 text-xs text-muted-foreground" data-testid="dispose-wait">{{ t('assets.dispose.fill') }}</p>

        <p v-if="needsOther" id="dispose-approve-note" class="m-0 text-right text-xs text-muted-foreground" data-testid="dispose-approve-note">{{ t('assets.dispose.approveNeedsOther') }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button v-if="needsOther" type="button" variant="outline" disabled aria-describedby="dispose-approve-note" data-testid="dispose-approve-now">{{ t('assets.dispose.approveNow') }}</Button>
          <Button type="submit" :disabled="busy || !preview" data-testid="dispose-confirm">{{ needsOther ? t('assets.dispose.request') : t('assets.dispose.approveNow') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
