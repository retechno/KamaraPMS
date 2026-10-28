<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetAuditSummary, AssetLocation, AssetProposal } from '@/api/types'
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
import { t } from '@/i18n'
import { formatDateTime, formatMoney } from '@/utils/format'

/**
 * The stock-takes (docs/architecture/37-fixed-assets.md 4.10, mockup 62): open one for a place, and decide the proposals of the closed ones. A stock-take posts nothing: a difference becomes a proposal, a move by whoever
 * manages the assets and a write-off by whoever disposes of them, through the same use cases and approval as the screens of the asset.
 */
const router = useRouter()
const { pid, canRead, canManage, canDispose } = useAssetAccess()
const tab = ref<'audits' | 'proposals'>('audits')
const audits = ref<AssetAuditSummary[]>([])
const proposals = ref<AssetProposal[]>([])
const locations = ref<AssetLocation[]>([])
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
    const [a, p, l] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/audits', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/proposals', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } }),
    ])
    audits.value = a.data?.data ?? []
    proposals.value = p.data?.data ?? []
    locations.value = l.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

// open a stock-take
const locationId = ref(0)
let key = crypto.randomUUID()
watch(locationId, () => { key = crypto.randomUUID() })
async function start(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/audits', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': key } },
      body: { location_id: locationId.value || null },
    })
    key = crypto.randomUUID()
    if (data) await router.push(`/assets/audits/${data.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// the proposals
const pending = computed(() => proposals.value.filter((p) => p.status === 'PENDING'))
const decided = computed(() => proposals.value.filter((p) => p.status !== 'PENDING'))
const canDecide = (p: AssetProposal): boolean => (p.kind === 'MOVE' ? canManage.value : canDispose.value)
async function apply(p: AssetProposal): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/proposals/{id}/apply', { params: { path: { propertyId, id: p.id } }, body: {} })
    toast.success(t(data?.proposal?.result_type === 'DISPOSAL_REQUEST' ? 'assets.proposal.appliedRequest' : 'assets.proposal.applied'))
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const rejecting = ref<AssetProposal | null>(null)
const note = ref('')
const formError = ref<ApiError | null>(null)
async function reject(): Promise<void> {
  const propertyId = pid.value
  const p = rejecting.value
  if (propertyId === null || !p) return
  busy.value = true
  formError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/proposals/{id}/reject', { params: { path: { propertyId, id: p.id } }, body: { note: note.value.trim() } })
    toast.success(t('assets.proposal.rejected'))
    rejecting.value = null
    note.value = ''
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const tabClass = (on: boolean): string => `min-h-9 rounded-full border px-3 text-sm ${on ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'}`
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="asset-audits">
    <PageHeader :title="t('assets.audit.title')" :description="t('assets.audit.intro')">
      <RouterLink to="/assets/reports/audit-differences" class="text-sm print:hidden">{{ t('assets.audit.differencesLink') }}</RouterLink>
    </PageHeader>
    <ErrorNotice :error="error" />
    <p v-if="loaded && !canRead" class="muted m-0">{{ t('assets.reports.denied') }}</p>
    <template v-else>
      <Card v-if="canManage" class="flex flex-wrap items-end gap-3 p-4" data-testid="audit-start">
        <FormField :label="t('assets.audit.location')" :hint="t('assets.audit.locationHint')"><template #default="{ id }"><NativeSelect :id="id" v-model.number="locationId" name="location_id" data-testid="audit-location"><option :value="0">{{ t('assets.audit.wholeProperty') }}</option><option v-for="l in locations.filter((x) => x.is_active)" :key="l.id" :value="l.id">{{ l.code }} · {{ l.name }}</option></NativeSelect></template></FormField>
        <Button :disabled="busy" data-testid="audit-open" @click="start">{{ t('assets.audit.open') }}</Button>
      </Card>
      <div class="flex gap-2" role="tablist">
        <button type="button" role="tab" :aria-selected="tab === 'audits'" :class="tabClass(tab === 'audits')" data-testid="tab-audits" @click="tab = 'audits'">{{ t('assets.audit.tabAudits') }}</button>
        <button type="button" role="tab" :aria-selected="tab === 'proposals'" :class="tabClass(tab === 'proposals')" data-testid="tab-proposals" @click="tab = 'proposals'">{{ t('assets.audit.tabProposals', { n: pending.length }) }}</button>
      </div>

      <Card v-if="tab === 'audits'" class="flex flex-col gap-2 p-4" data-testid="audit-list">
        <p v-if="loaded && !audits.length" class="muted m-0" data-testid="audit-none">{{ t('assets.audit.none') }}</p>
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
          <li v-for="a in audits" :key="a.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border pb-2" :data-testid="`audit-${a.number}`">
            <RouterLink :to="`/assets/audits/${a.id}`" class="font-medium">{{ a.number }}</RouterLink>
            <span class="flex-1">{{ a.location_name ?? t('assets.audit.wholeProperty') }}</span>
            <Badge :variant="a.status === 'IN_PROGRESS' ? 'warning' : 'outline'">{{ t(`assets.audit.status.${a.status}`) }}</Badge>
            <span class="text-xs text-muted-foreground">{{ t('assets.audit.counts', { scanned: a.scanned, missing: a.missing }) }}</span>
            <span v-if="a.pending_proposals" class="text-xs text-muted-foreground">{{ t('assets.audit.pendingProposals', { n: a.pending_proposals }) }}</span>
            <span class="text-xs text-muted-foreground">{{ formatDateTime(a.started_at) }}</span>
          </li>
        </ul>
      </Card>

      <Card v-else class="flex flex-col gap-3 p-4" data-testid="proposal-list">
        <p class="m-0 text-sm text-muted-foreground">{{ t('assets.proposal.hint') }}</p>
        <p v-if="loaded && !pending.length" class="muted m-0" data-testid="proposal-none">{{ t('assets.proposal.none') }}</p>
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
          <li v-for="p in pending" :key="p.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border pb-2" :data-testid="`proposal-${p.id}`">
            <span class="flex-1">
              <b>{{ p.asset_number }}</b> · {{ p.asset_name }}
              <span class="block text-xs text-muted-foreground">{{ p.audit_number }} · {{ t(`assets.audit.result.${p.line_result}`) }}<template v-if="p.kind === 'MOVE'"> → {{ p.to_location_name ?? '–' }}</template><template v-else> · {{ t('assets.proposal.carrying', { amount: formatMoney(p.carrying_value) }) }}</template></span>
            </span>
            <Badge variant="outline">{{ t(`assets.audit.proposalKind.${p.kind}`) }}</Badge>
            <template v-if="canDecide(p)">
              <Button size="sm" :disabled="busy" :data-testid="`proposal-apply-${p.id}`" @click="apply(p)">{{ t(p.kind === 'MOVE' ? 'assets.proposal.applyMove' : 'assets.proposal.applyWriteOff') }}</Button>
              <Button size="sm" variant="outline" :disabled="busy" :data-testid="`proposal-reject-${p.id}`" @click="rejecting = p">{{ t('assets.proposal.reject') }}</Button>
            </template>
            <span v-else class="text-xs text-muted-foreground">{{ t('assets.proposal.needsPermission') }}</span>
          </li>
        </ul>
        <h3 v-if="decided.length" class="m-0 text-sm font-semibold">{{ t('assets.proposal.decided') }}</h3>
        <ul v-if="decided.length" class="m-0 flex list-none flex-col gap-1 p-0 text-xs text-muted-foreground">
          <li v-for="p in decided" :key="p.id">{{ p.asset_number }} · {{ t(`assets.audit.proposalKind.${p.kind}`) }} · {{ t(`assets.audit.proposalStatus.${p.status}`) }}<template v-if="p.decision_note"> · {{ p.decision_note }}</template></li>
        </ul>
      </Card>
    </template>

    <Dialog :open="!!rejecting" @update:open="(o: boolean) => { if (!o) rejecting = null }">
      <DialogContent>
        <DialogTitle>{{ t('assets.proposal.rejectTitle') }}</DialogTitle>
        <DialogDescription>{{ rejecting ? `${rejecting.asset_number} · ${rejecting.asset_name}` : '' }}</DialogDescription>
        <ErrorNotice :error="formError" />
        <FormField :label="t('assets.proposal.reason')" required><template #default="{ id }"><Input :id="id" v-model="note" name="note" maxlength="500" data-testid="proposal-note" /></template></FormField>
        <div class="flex justify-end gap-2">
          <Button variant="outline" @click="rejecting = null">{{ t('common.cancel') }}</Button>
          <Button :disabled="busy || !note.trim()" data-testid="proposal-reject-confirm" @click="reject">{{ t('assets.proposal.reject') }}</Button>
        </div>
      </DialogContent>
    </Dialog>
  </section>
</template>
