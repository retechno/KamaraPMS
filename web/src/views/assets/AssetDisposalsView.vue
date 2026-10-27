<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, AssetDisposal, AssetDisposalRequest } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'

/** The disposals (docs/architecture/37-fixed-assets.md 4.7, mockup 57): the requests that wait for an approver, and the disposals that were posted, with the reversal of a posted one. */
const { pid, canRead, canDispose } = useAssetAccess()
const tab = ref<'pending' | 'disposals'>('pending')
const requests = ref<AssetDisposalRequest[]>([])
const disposals = ref<AssetDisposal[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  try {
    const [r, d] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/disposal-requests', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/disposals', { params: { path: { propertyId } } }),
    ])
    requests.value = r.data ?? []
    disposals.value = d.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const pending = computed(() => requests.value.filter((r) => r.status === 'PENDING'))
const decided = computed(() => requests.value.filter((r) => r.status !== 'PENDING'))
const kindText = (k: string, reason: string): string => `${t(`assets.dispose.kindShort.${k}`)}${te(`assets.dispose.reasonCode.${reason}`) && k === 'WRITE_OFF' ? ` · ${t(`assets.dispose.reasonCode.${reason}`)}` : ''}`

// approve
async function approve(r: AssetDisposalRequest): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/disposal-requests/{id}/approve', { params: { path: { propertyId, id: r.id } } })
    toast.success(t('assets.disposals.approved', { number: data?.disposal?.number ?? '' }))
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// reject or withdraw
const rejecting = ref<AssetDisposalRequest | null>(null)
const note = ref('')
const formError = ref<ApiError | null>(null)
async function reject(): Promise<void> {
  const propertyId = pid.value
  const r = rejecting.value
  if (propertyId === null || !r) return
  busy.value = true
  formError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/disposal-requests/{id}/reject', { params: { path: { propertyId, id: r.id } }, body: { note: note.value.trim() } })
    rejecting.value = null
    toast.success(t('assets.disposals.rejected'))
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// reverse a posted disposal: the reason, then the approval of the corrections
const reversing = ref<AssetDisposal | null>(null)
const approving = ref(false)
const reason = ref('')
const approveError = ref<ApiError | null>(null)
async function reverse(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const d = reversing.value
  if (propertyId === null || !d) return
  busy.value = true
  approveError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/disposals/{id}/reverse', { params: { path: { propertyId, id: d.id } }, body: { reason: reason.value.trim(), approval } })
    approving.value = false
    reversing.value = null
    toast.success(t('assets.disposals.reversed', { number: d.number }))
    await load()
  } catch (e) {
    approveError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <RouterLink to="/assets" class="text-sm text-primary hover:underline">← {{ t('assets.register.title') }}</RouterLink>
  <PageHeader :title="t('assets.disposals.title')" :description="t('assets.disposals.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else>
    <div class="flex gap-2" role="tablist">
      <button type="button" role="tab" :aria-selected="tab === 'pending'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'pending' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-pending" @click="tab = 'pending'">{{ t('assets.disposals.tabPending', { n: pending.length }) }}</button>
      <button type="button" role="tab" :aria-selected="tab === 'disposals'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'disposals' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-disposals" @click="tab = 'disposals'">{{ t('assets.disposals.tabDisposals', { n: disposals.length }) }}</button>
    </div>

    <Card v-if="tab === 'pending'" class="flex flex-col gap-3 p-4" data-testid="pending-list">
      <p v-if="loaded && !pending.length" class="muted m-0" data-testid="pending-empty">{{ t('assets.disposals.noPending') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="r in pending" :key="r.id" class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border pb-2 text-sm" :data-testid="`request-${r.id}`">
          <span class="min-w-48 flex-1"><RouterLink :to="`/assets/${r.asset_id}`" class="font-semibold text-primary hover:underline">{{ r.asset_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ r.asset_number }} · {{ kindText(r.kind, r.reason_code) }} · {{ formatDate(r.disposal_date) }}</span></span>
          <span class="tabular-nums">{{ t('assets.disposals.amount', { amount: formatMoney(r.approval_amount) }) }}</span>
          <span class="text-xs text-muted-foreground">{{ t('assets.disposals.requestedBy', { name: r.requested_by_name ?? '–', at: formatDateTime(r.requested_at) }) }}</span>
          <span v-if="canDispose" class="flex gap-2">
            <Button size="sm" :disabled="busy" :data-testid="`approve-${r.id}`" @click="approve(r)">{{ t('assets.disposals.approve') }}</Button>
            <Button size="sm" variant="outline" :data-testid="`reject-${r.id}`" @click="rejecting = r; note = ''; formError = null">{{ t('assets.disposals.reject') }}</Button>
          </span>
        </li>
      </ul>
      <template v-if="decided.length">
        <h3 class="m-0 mt-2 text-sm font-semibold">{{ t('assets.disposals.decided') }}</h3>
        <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
          <li v-for="r in decided" :key="r.id" class="flex flex-wrap items-baseline gap-x-3 border-b border-border py-1" :data-testid="`decided-${r.id}`">
            <span class="flex-1">{{ r.asset_number }} · {{ r.asset_name }} · {{ kindText(r.kind, r.reason_code) }}</span>
            <Badge :variant="r.status === 'APPROVED' ? 'success' : 'outline'">{{ t(`assets.disposals.status.${r.status}`) }}</Badge>
            <span class="text-xs text-muted-foreground">{{ r.decided_by_name ?? '' }}<template v-if="r.decision_note"> · {{ r.decision_note }}</template></span>
          </li>
        </ul>
      </template>
    </Card>

    <Card v-else class="flex flex-col gap-2 p-4" data-testid="disposal-list">
      <p v-if="loaded && !disposals.length" class="muted m-0">{{ t('assets.disposals.noDisposals') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full border-collapse text-sm tabular-nums">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.disposals.col.number') }}</th><th class="p-2">{{ t('assets.col.asset') }}</th><th class="p-2 text-right">{{ t('assets.col.carrying') }}</th><th class="p-2 text-right">{{ t('assets.dispose.proceeds') }}</th><th class="p-2 text-right">{{ t('assets.disposals.col.gain') }}</th><th class="p-2">{{ t('assets.col.status') }}</th><th class="p-2" /></tr></thead>
          <tbody>
            <tr v-for="d in disposals" :key="d.id" class="border-t border-border" :data-testid="`disposal-${d.number}`">
              <td class="p-2">{{ d.number }}<div class="text-xs text-muted-foreground">{{ formatDate(d.disposal_date) }} · {{ d.journal_number }}</div></td>
              <td class="p-2"><RouterLink :to="`/assets/${d.asset_id}`" class="text-primary hover:underline">{{ d.asset_name }}</RouterLink><div class="text-xs text-muted-foreground">{{ d.asset_number }} · {{ kindText(d.kind, d.reason_code) }}</div></td>
              <td class="p-2 text-right">{{ formatMoney(d.carrying_removed) }}</td><td class="p-2 text-right">{{ formatMoney(d.proceeds) }}</td><td class="p-2 text-right">{{ formatMoney(d.gain_loss) }}</td>
              <td class="p-2"><Badge :variant="d.status === 'POSTED' ? 'success' : 'outline'">{{ t(`assets.disposals.status.${d.status}`) }}</Badge></td>
              <td class="p-2 text-right"><Button v-if="canDispose && d.status === 'POSTED'" size="sm" variant="outline" :data-testid="`reverse-${d.number}`" @click="reversing = d; reason = ''; approving = false">{{ t('assets.disposals.reverse') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </Card>
  </template>

  <Dialog :open="!!rejecting" @update:open="(v) => { if (!v) rejecting = null }">
    <DialogContent class="max-w-md p-5" data-testid="reject-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="reject">
        <DialogTitle>{{ t('assets.disposals.rejectTitle') }}</DialogTitle>
        <DialogDescription>{{ t('assets.disposals.rejectHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.disposals.note')" required :error="formError?.fieldMessage('note')"><template #default="{ id }"><Input :id="id" v-model="note" name="note" maxlength="300" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="rejecting = null">{{ t('common.cancel') }}</Button><Button type="submit" variant="destructive" :disabled="busy || !note.trim()" data-testid="reject-confirm">{{ t('assets.disposals.reject') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="!!reversing && !approving" @update:open="(v) => { if (!v) reversing = null }">
    <DialogContent class="max-w-md p-5" data-testid="reverse-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="approving = true">
        <DialogTitle>{{ t('assets.disposals.reverseTitle', { number: reversing?.number ?? '' }) }}</DialogTitle>
        <DialogDescription>{{ t('assets.disposals.reverseHint') }}</DialogDescription>
        <FormField :label="t('assets.asset.reason')" required><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="500" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="reversing = null">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="!reason.trim()" data-testid="reverse-next">{{ t('assets.disposals.reverse') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog
    v-if="approving && reversing" kind="ASSET_DISPOSAL" :amount="reversing.carrying_removed" :title="t('assets.disposals.approvalTitle')" :message="t('assets.disposals.approvalMessage')" :busy="busy" :error="approveError"
    @approve="reverse" @cancel="approving = false"
  />
</template>
