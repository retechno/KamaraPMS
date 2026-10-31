<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightAuditPackManifest, NightAuditPackSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { downloadFile, openPdf } from '@/utils/documents'

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('nightaudit.pack_manage', pid.value))
const canView = computed(() => auth.can('nightaudit.pack_view', pid.value))

/** The sections that have a table (CSV); the others are JSON only. */
const CSV_SECTIONS = ['FLASH', 'REVENUE', 'TAX', 'PAYMENTS', 'Z_REPORTS', 'GUEST_LEDGER', 'DEPOSIT_LEDGER', 'CORRECTIONS', 'CANCELLATIONS', 'IN_HOUSE']

const filter = ref({ from: '', to: '' })
const rows = ref<NightAuditPackSummary[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const error = ref<ApiError | null>(null)
const detail = ref<NightAuditPackManifest | null>(null)
const picked = ref<string[]>([])
const lateDate = ref('')
const notice = ref('')

const columns = computed<Column<NightAuditPackSummary>[]>(() => [
  { key: 'business_date', label: t('nightAuditArchive.date'), format: 'date' },
  { key: 'status', label: t('nightAuditArchive.status') },
  { key: 'sections_ok', label: t('nightAuditArchive.sections') },
  { key: 'built_at', label: t('nightAuditArchive.built'), format: 'datetime' },
  { key: 'open', label: '', align: 'right' },
])

function fail(e: unknown): void {
  error.value = e instanceof ApiError ? e : null
}

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canView.value) return
  loading.value = true
  error.value = null
  try {
    const query: Record<string, string> = {}
    if (filter.value.from) query.from = filter.value.from
    if (filter.value.to) query.to = filter.value.to
    if (more && cursor.value) query.cursor = cursor.value
    const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/packs', { params: { path: { propertyId }, query } })
    rows.value = more ? [...rows.value, ...(data?.items ?? [])] : (data?.items ?? [])
    cursor.value = data?.next_cursor ?? null
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}

async function open(date: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/packs/{date}', { params: { path: { propertyId, date } } })
    detail.value = (data as NightAuditPackManifest | undefined) ?? null
    picked.value = []
  } catch (e) {
    fail(e)
  }
}

const base = (date: string) => `/api/v1/properties/${pid.value}/night-audit/packs/${date}`

async function pdf(): Promise<void> {
  if (!detail.value) return
  const sections = picked.value.length ? `?sections=${picked.value.join(',')}` : ''
  try {
    await openPdf(`${base(detail.value.business_date)}.pdf${sections}`)
  } catch (e) {
    fail(e)
  }
}

async function csv(code: string): Promise<void> {
  if (!detail.value) return
  try {
    await downloadFile(`${base(detail.value.business_date)}/sections/${code}?format=csv`)
  } catch (e) {
    fail(e)
  }
}

async function json(code: string): Promise<void> {
  if (!detail.value) return
  try {
    await downloadFile(`${base(detail.value.business_date)}/sections/${code}?format=json`, 'application/json')
  } catch (e) {
    fail(e)
  }
}

async function rebuild(date: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/packs/{date}/rebuild', { params: { path: { propertyId, date } } })
    detail.value = (data as NightAuditPackManifest | undefined) ?? null
    notice.value = t('nightAuditArchive.rebuilt', { date })
    await load()
  } catch (e) {
    fail(e)
  }
}

const hasFailed = computed(() => detail.value?.sections.some((s) => s.status === 'FAILED') ?? false)
const hasData = (s: { status: string }) => s.status === 'OK'

watch(pid, () => {
  detail.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('nightAuditArchive.title')" :description="t('nightAuditArchive.description')" />

  <ErrorNotice :error="error" />
  <p v-if="notice" class="alert" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('nightAuditArchive.selectProperty') }}</p>
  <p v-else-if="!canView" class="muted" data-testid="no-access">{{ t('nightAuditArchive.noAccess') }}</p>

  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3" data-testid="filters" @submit.prevent="load()">
      <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.from') }}<Input v-model="filter.from" type="date" name="from" /></label>
      <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.to') }}<Input v-model="filter.to" type="date" name="to" /></label>
      <Button type="submit" variant="outline" size="sm" data-testid="show">{{ t('nightAuditArchive.show') }}</Button>
    </form>

    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="business_date"
      :loading="loading"
      :empty-title="t('nightAuditArchive.empty')"
      :empty-description="t('nightAuditArchive.emptyHint')"
      :row-test-id="(r) => `pack-row-${r.business_date}`"
      data-testid="packs"
    >
      <template #cell-status="{ row }">
        <Badge :variant="row.status === 'COMPLETE' ? 'success' : 'warning'">{{ row.status === 'COMPLETE' ? t('nightAuditArchive.complete') : t('nightAuditArchive.partial') }}</Badge>
        <small v-if="row.built_late" class="ml-2 text-muted-foreground">{{ t('nightAuditArchive.builtLate') }}</small>
      </template>
      <template #cell-sections_ok="{ row }">{{ t('nightAuditArchive.sectionsValue', { ok: row.sections_ok, failed: row.sections_failed }) }}</template>
      <template #cell-open="{ row }">
        <Button variant="outline" size="sm" :data-testid="`open-${row.business_date}`" @click="open(row.business_date)">{{ t('nightAuditArchive.open') }}</Button>
      </template>
    </DataTable>
    <div v-if="cursor" class="mt-3 flex justify-center">
      <Button variant="outline" size="sm" :disabled="loading" data-testid="load-more" @click="load(true)">{{ t('nightAuditArchive.loadMore') }}</Button>
    </div>

    <Card v-if="detail" class="mt-4" data-testid="detail">
      <CardHeader>
        <CardTitle>{{ t('nightAuditArchive.detailTitle', { date: $date(detail.business_date) }) }}</CardTitle>
        <p class="m-0 text-sm text-muted-foreground">
          <Badge :variant="detail.status === 'COMPLETE' ? 'success' : 'warning'">{{ detail.status === 'COMPLETE' ? t('nightAuditArchive.complete') : t('nightAuditArchive.partial') }}</Badge>
          {{ t('nightAuditArchive.builtBy', { user: detail.built_by, ms: detail.build_ms }) }}
          <template v-if="detail.built_late"> · {{ t('nightAuditArchive.builtLate') }}</template>
        </p>
      </CardHeader>
      <CardContent>
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left">
              <th scope="col">{{ t('nightAuditArchive.pick') }}</th>
              <th scope="col">{{ t('nightAuditArchive.sectionCol') }}</th>
              <th scope="col">{{ t('nightAuditArchive.sectionStatus') }}</th>
              <th scope="col">{{ t('nightAuditArchive.size') }}</th>
              <th scope="col" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in detail.sections" :key="s.section" :data-testid="`section-${s.section}`">
              <td><input v-model="picked" type="checkbox" :value="s.section" :disabled="!hasData(s)" :aria-label="t(`nightAuditArchive.section.${s.section}` as never)" /></td>
              <td>{{ t(`nightAuditArchive.section.${s.section}` as never) }}</td>
              <td>
                <Badge :variant="s.status === 'OK' ? 'success' : s.status === 'FAILED' ? 'destructive' : 'secondary'">{{ t(`nightAuditArchive.state.${s.status}` as never) }}</Badge>
                <small v-if="s.status === 'FAILED' && !s.rebuildable" class="ml-2 text-muted-foreground">{{ t('nightAuditArchive.notRebuildable') }}</small>
              </td>
              <td>{{ hasData(s) ? s.size_bytes : '' }}</td>
              <td class="text-right">
                <template v-if="hasData(s)">
                  <Button variant="ghost" size="sm" :data-testid="`json-${s.section}`" @click="json(s.section)">{{ t('nightAuditArchive.json') }}</Button>
                  <Button v-if="CSV_SECTIONS.includes(s.section)" variant="ghost" size="sm" :data-testid="`csv-${s.section}`" @click="csv(s.section)">{{ t('nightAuditArchive.csv') }}</Button>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
        <p class="mb-0 mt-3 text-xs text-muted-foreground">{{ t('nightAuditArchive.pdfNote') }}</p>
        <div class="mt-3 flex flex-wrap gap-2">
          <Button size="sm" data-testid="pdf-all" @click="picked = []; pdf()">{{ t('nightAuditArchive.pdfAll') }}</Button>
          <Button variant="outline" size="sm" :disabled="!picked.length" data-testid="pdf-chosen" @click="pdf">{{ t('nightAuditArchive.pdfChosen') }}</Button>
          <Button v-if="canManage && hasFailed" variant="outline" size="sm" data-testid="rebuild" @click="rebuild(detail.business_date)">{{ t('nightAuditArchive.rebuild') }}</Button>
        </div>
        <p v-if="canManage && hasFailed" class="mb-0 mt-2 text-xs text-muted-foreground">{{ t('nightAuditArchive.rebuildHint') }}</p>
      </CardContent>
    </Card>

    <Card v-if="canManage" class="mt-4" data-testid="late">
      <CardHeader><CardTitle>{{ t('nightAuditArchive.buildLate') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="mt-0 text-sm text-muted-foreground">{{ t('nightAuditArchive.buildLateHint') }}</p>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="lateDate && rebuild(lateDate)">
          <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.buildDate') }}<Input v-model="lateDate" type="date" name="late_date" /></label>
          <Button type="submit" size="sm" :disabled="!lateDate" data-testid="build-late">{{ t('nightAuditArchive.build') }}</Button>
        </form>
      </CardContent>
    </Card>
  </template>
</template>
