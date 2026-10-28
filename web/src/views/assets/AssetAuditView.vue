<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetAuditDetail, AssetAuditExpected, AssetAuditLine } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QrScanner from '@/components/assets/QrScanner.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { formatDateTime } from '@/utils/format'

/**
 * One stock-take on the phone (docs/architecture/37-fixed-assets.md 4.10, mockup 62, a 390 px screen): scan the QR of the place, then the QR of each asset in it, or type the code. The result of each scan is FOUND, MOVED,
 * UNEXPECTED or DAMAGED; what was expected and not scanned becomes MISSING when the stock-take is closed. Closing turns the differences into proposals: nothing is posted here.
 */
const route = useRoute()
const { pid, canRead, canManage } = useAssetAccess()
const auditId = computed(() => Number(route.params.id))
const audit = ref<AssetAuditDetail | null>(null)
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const last = ref<AssetAuditLine | null>(null)
const closedWith = ref<{ missing: number; proposals: number } | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value || !Number.isFinite(auditId.value)) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/audits/{id}/detail', { params: { path: { propertyId, id: auditId.value } } })
    audit.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, auditId], load, { immediate: true })

const open = computed(() => audit.value?.status === 'IN_PROGRESS')
const wholeProperty = computed(() => audit.value !== null && audit.value.location_id === null)
// the place of a stock-take of the whole property: the QR of the place read last
const place = ref('')
const manual = ref('')
const scanning = ref(false)

async function scan(code: string, extra: { asset_id?: number; damaged?: boolean; note?: string } = {}): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !open.value || busy.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/assets/audits/{id}/lines', {
      params: { path: { propertyId, id: auditId.value } },
      body: { code: code || undefined, location_code: wholeProperty.value ? place.value || undefined : undefined, ...extra },
    })
    last.value = data?.line ?? null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

/** What the camera read: the QR of a place sets the place, any other code is an asset. */
function decoded(text: string): void {
  const code = text.trim()
  if (!code) return
  if (code.startsWith('FL:')) {
    place.value = code
    toast.success(t('assets.audit.placeSet'))
    return
  }
  void scan(code)
}
async function typed(): Promise<void> {
  const code = manual.value.trim()
  if (!code) return
  if (code.startsWith('FL:')) {
    place.value = code
    manual.value = ''
    return
  }
  await scan(code)
  if (!error.value) manual.value = ''
}

// an asset that is here but broken
const breaking = ref<AssetAuditExpected | null>(null)
const note = ref('')
async function markDamaged(): Promise<void> {
  const a = breaking.value
  if (!a) return
  await scan('', { asset_id: a.asset_id, damaged: true, note: note.value.trim() || undefined })
  if (!error.value) {
    breaking.value = null
    note.value = ''
  }
}

// closing
const closing = ref(false)
async function close(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/audits/{id}/close', { params: { path: { propertyId, id: auditId.value } } })
    closedWith.value = data ? { missing: data.missing, proposals: data.proposals } : null
    closing.value = false
    scanning.value = false
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const tone = (r: string): 'success' | 'warning' | 'outline' => (r === 'FOUND' ? 'success' : r === 'MOVED' || r === 'UNEXPECTED' ? 'outline' : 'warning')
const progress = computed(() => {
  const c = audit.value?.counts
  if (!c) return ''
  return t('assets.audit.progress', { done: c.found + c.moved + c.damaged, total: c.expected })
})
</script>

<template>
  <section class="mx-auto flex w-full max-w-md flex-col gap-3" data-testid="asset-audit">
    <PageHeader :title="audit ? `${audit.number}` : t('assets.audit.title')" :description="audit ? (audit.location_name ?? t('assets.audit.wholeProperty')) : undefined">
      <RouterLink to="/assets/audits" class="text-sm">{{ t('assets.audit.back') }}</RouterLink>
    </PageHeader>
    <ErrorNotice :error="error" />
    <p v-if="loaded && !canRead" class="muted m-0">{{ t('assets.reports.denied') }}</p>
    <template v-else-if="audit">
      <Card class="flex flex-wrap items-center gap-2 p-3" data-testid="audit-counts">
        <Badge :variant="open ? 'warning' : 'outline'">{{ t(`assets.audit.status.${audit.status}`) }}</Badge>
        <b class="text-sm" data-testid="audit-progress">{{ progress }}</b>
        <span class="text-xs text-muted-foreground">{{ t('assets.audit.countsLine', { found: audit.counts.found, moved: audit.counts.moved, unexpected: audit.counts.unexpected, damaged: audit.counts.damaged }) }}</span>
      </Card>

      <template v-if="open && canManage">
        <Card v-if="wholeProperty" class="flex flex-col gap-1 p-3 text-sm" data-testid="audit-place">
          <span class="text-xs text-muted-foreground">{{ t('assets.audit.placeLabel') }}</span>
          <b>{{ place || t('assets.audit.placeNone') }}</b>
          <span class="text-xs text-muted-foreground">{{ t('assets.audit.placeHint') }}</span>
        </Card>
        <Button class="min-h-11" :variant="scanning ? 'outline' : 'default'" data-testid="audit-camera" @click="scanning = !scanning">{{ scanning ? t('assets.audit.cameraOff') : t('assets.audit.cameraOn') }}</Button>
        <QrScanner :active="scanning" @decoded="decoded" @failed="scanning = false" />
        <form class="flex items-end gap-2" data-testid="audit-manual-form" @submit.prevent="typed">
          <FormField :label="t('assets.audit.typeCode')" class="flex-1"><template #default="{ id }"><Input :id="id" v-model="manual" name="code" autocomplete="off" autocapitalize="characters" class="min-h-11" :placeholder="t('assets.audit.codePlaceholder')" data-testid="audit-code" /></template></FormField>
          <Button type="submit" class="min-h-11" :disabled="busy || !manual.trim()" data-testid="audit-scan">{{ t('assets.audit.scan') }}</Button>
        </form>
        <p v-if="last" class="m-0 rounded-md border border-border p-2 text-sm" role="status" data-testid="audit-last">
          <b>{{ last.asset_number }}</b> · {{ last.asset_name }} <Badge :variant="tone(last.result)" class="ml-1">{{ t(`assets.audit.result.${last.result}`) }}</Badge>
          <span v-if="last.result === 'MOVED'" class="block text-xs text-muted-foreground">{{ t('assets.audit.movedHint', { from: last.expected_location_name ?? '–' }) }}</span>
        </p>
      </template>

      <Card v-if="audit.lines.length" class="flex flex-col gap-1 p-3" data-testid="audit-lines">
        <h2 class="m-0 text-sm font-semibold">{{ t('assets.audit.scanned') }}</h2>
        <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
          <li v-for="l in audit.lines" :key="l.asset_id" class="flex flex-wrap items-center gap-2 border-b border-border py-1" :data-testid="`audit-line-${l.asset_number}`">
            <span class="flex-1"><b>{{ l.asset_number }}</b> · {{ l.asset_name }}<span v-if="l.result === 'MOVED'" class="block text-xs text-muted-foreground">{{ t('assets.audit.registeredAt', { place: l.expected_location_name ?? '–' }) }}</span><span v-if="l.note" class="block text-xs text-muted-foreground">{{ l.note }}</span></span>
            <Badge :variant="tone(l.result)">{{ t(`assets.audit.result.${l.result}`) }}</Badge>
            <span v-if="l.scanned_at" class="text-xs text-muted-foreground">{{ formatDateTime(l.scanned_at) }}</span>
          </li>
        </ul>
      </Card>

      <Card v-if="open && audit.unscanned.length" class="flex flex-col gap-1 p-3" data-testid="audit-unscanned">
        <h2 class="m-0 text-sm font-semibold">{{ t('assets.audit.unscanned', { n: audit.unscanned.length }) }}</h2>
        <p class="m-0 text-xs text-muted-foreground">{{ t('assets.audit.unscannedHint') }}</p>
        <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
          <li v-for="u in audit.unscanned" :key="u.asset_id" class="flex flex-wrap items-center gap-2 border-b border-border py-1" :data-testid="`audit-unscanned-${u.number}`">
            <span class="flex-1"><b>{{ u.number }}</b> · {{ u.name }}<span v-if="u.disposal_pending" class="block text-xs text-muted-foreground">{{ t('assets.audit.disposalPending') }}</span></span>
            <Button v-if="canManage" size="sm" variant="outline" class="min-h-9" :disabled="busy" :data-testid="`audit-damaged-${u.number}`" @click="breaking = u">{{ t('assets.audit.markDamaged') }}</Button>
          </li>
        </ul>
      </Card>

      <Button v-if="open && canManage" class="min-h-11" variant="outline" data-testid="audit-close" @click="closing = true">{{ t('assets.audit.close') }}</Button>
      <Card v-if="closedWith || !open" class="flex flex-col gap-1 p-3 text-sm" data-testid="audit-closed">
        <b>{{ t('assets.audit.closedTitle') }}</b>
        <span v-if="closedWith">{{ t('assets.audit.closedResult', { missing: closedWith.missing, proposals: closedWith.proposals }) }}</span>
        <span v-else-if="audit.closed_at" class="text-muted-foreground">{{ formatDateTime(audit.closed_at) }} · {{ t('assets.audit.countsMissing', { n: audit.counts.missing }) }}</span>
        <RouterLink to="/assets/audits" data-testid="audit-to-proposals">{{ t('assets.audit.toProposals') }}</RouterLink>
      </Card>
    </template>

    <Dialog :open="!!breaking" @update:open="(o: boolean) => { if (!o) breaking = null }">
      <DialogContent>
        <DialogTitle>{{ t('assets.audit.damagedTitle') }}</DialogTitle>
        <DialogDescription>{{ breaking ? `${breaking.number} · ${breaking.name}` : '' }}</DialogDescription>
        <FormField :label="t('assets.audit.damagedNote')"><template #default="{ id }"><Input :id="id" v-model="note" name="note" maxlength="500" data-testid="audit-damaged-note" /></template></FormField>
        <div class="flex justify-end gap-2">
          <Button variant="outline" @click="breaking = null">{{ t('common.cancel') }}</Button>
          <Button :disabled="busy" data-testid="audit-damaged-confirm" @click="markDamaged">{{ t('assets.audit.markDamaged') }}</Button>
        </div>
      </DialogContent>
    </Dialog>

    <Dialog :open="closing" @update:open="(o: boolean) => { if (!o) closing = false }">
      <DialogContent>
        <DialogTitle>{{ t('assets.audit.closeTitle') }}</DialogTitle>
        <DialogDescription>{{ t('assets.audit.closeMessage', { n: audit?.unscanned.length ?? 0 }) }}</DialogDescription>
        <div class="flex justify-end gap-2">
          <Button variant="outline" @click="closing = false">{{ t('common.cancel') }}</Button>
          <Button :disabled="busy" data-testid="audit-close-confirm" @click="close">{{ t('assets.audit.close') }}</Button>
        </div>
      </DialogContent>
    </Dialog>
  </section>
</template>
