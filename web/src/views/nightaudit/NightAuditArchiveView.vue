<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightAuditPackDelivery, NightAuditPackManifest, NightAuditPackRecipient, NightAuditPackSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DateInput } from '@/components/ui/date-input'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { downloadFile, openPdf } from '@/utils/documents'
import { guardForm } from '@/utils/forms'

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('nightaudit.pack_manage', pid.value))
const canView = computed(() => auth.can('nightaudit.pack_view', pid.value))
const canResend = computed(() => auth.can('nightaudit.pack_resend', pid.value))

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
const emailEnabled = ref(true)
const deliveries = ref<NightAuditPackDelivery[]>([])
const recipients = ref<NightAuditPackRecipient[]>([])
const resendIds = ref<number[]>([])
const resending = ref(false)

const columns = computed<Column<NightAuditPackSummary>[]>(() => [
  { key: 'business_date', label: t('nightAuditArchive.date'), format: 'date' },
  { key: 'status', label: t('nightAuditArchive.status') },
  { key: 'sections_ok', label: t('nightAuditArchive.sections') },
  { key: 'mail', label: t('nightAuditArchive.mail') },
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
    emailEnabled.value = data?.email_enabled ?? true
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
    await loadMail(date)
  } catch (e) {
    fail(e)
  }
}

/** The e-mail history of the pack, and (to send it again) the active recipients, every one chosen. */
async function loadMail(date: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const history = await api.GET('/api/v1/properties/{propertyId}/night-audit/packs/{date}/deliveries', { params: { path: { propertyId, date } } })
  deliveries.value = history.data?.items ?? []
  emailEnabled.value = history.data?.email_enabled ?? emailEnabled.value
  if (!canResend.value) {
    recipients.value = []
    return
  }
  const list = await api.GET('/api/v1/properties/{propertyId}/night-audit/pack-recipients', { params: { path: { propertyId } } })
  recipients.value = (list.data?.items ?? []).filter((r) => r.is_active)
  resendIds.value = recipients.value.map((r) => r.id)
}

async function resend(): Promise<void> {
  const propertyId = pid.value
  const d = detail.value
  if (propertyId === null || !d || !resendIds.value.length) return
  resending.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/packs/{date}/resend', {
      params: { path: { propertyId, date: d.business_date } }, body: { recipient_ids: resendIds.value },
    })
    toast.success(t('nightAuditArchive.resendDone', { n: data?.queued.length ?? 0, date: d.business_date }))
    await loadMail(d.business_date)
    await load()
  } catch (e) {
    fail(e)
  } finally {
    resending.value = false
  }
}

/** The text under a message that did not go: a section too large is named in the language of the page; any other error is what the mail server said. */
function failure(d: NightAuditPackDelivery): string {
  return d.last_error?.startsWith('PACK_TOO_LARGE') ? t('nightAuditArchive.tooLarge') : (d.last_error ?? '')
}

const shortHash = (h: string) => `${h.slice(0, 4)}…${h.slice(-4)}`

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
    <form class="mb-8 flex flex-wrap items-end gap-3" data-testid="filters" @submit.prevent="guardForm($event) && load()">
      <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.from') }}<DateInput v-model="filter.from" name="from" /></label>
      <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.to') }}<DateInput v-model="filter.to" name="to" /></label>
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
      <template #cell-mail="{ row }">
        <span v-if="row.mail_sent + row.mail_failed + row.mail_queued === 0" class="text-muted-foreground">{{ emailEnabled ? t('nightAuditArchive.mailNone') : t('nightAuditArchive.mailOff') }}</span>
        <template v-else>
          <span v-if="row.mail_sent">{{ t('nightAuditArchive.mailSent', { n: row.mail_sent }) }}</span>
          <span v-if="row.mail_queued"> · {{ t('nightAuditArchive.mailQueued', { n: row.mail_queued }) }}</span>
          <Badge v-if="row.mail_failed" variant="destructive" class="ml-1">{{ t('nightAuditArchive.mailFailed', { n: row.mail_failed }) }}</Badge>
        </template>
      </template>
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

    <Card v-if="detail" class="mt-4" data-testid="history">
      <CardHeader>
        <CardTitle>{{ t('nightAuditArchive.historyTitle') }}</CardTitle>
        <p v-if="!emailEnabled" class="m-0 text-sm text-warning" data-testid="mail-off">{{ t('nightAuditArchive.mailOffNote') }}</p>
      </CardHeader>
      <CardContent>
        <p v-if="!deliveries.length" class="m-0 text-sm text-muted-foreground" data-testid="history-empty">{{ t('nightAuditArchive.historyNone') }}</p>
        <ul v-else class="m-0 grid list-none gap-3 p-0">
          <li v-for="d in deliveries" :key="d.id" class="flex flex-wrap items-start justify-between gap-2 border-b pb-3 text-sm" :data-testid="`delivery-${d.id}`">
            <div>
              <div class="font-medium">{{ d.email }}</div>
              <small class="text-muted-foreground">
                {{ t('nightAuditArchive.historySections', { n: d.sections.length }) }}
                <template v-if="d.parts > 1"> · {{ t('nightAuditArchive.historyPart', { part: d.part, parts: d.parts }) }}</template>
                · {{ t('nightAuditArchive.historyQueued', { user: d.queued_by }) }} {{ $dateTime(d.queued_at) }}
                <template v-if="d.status !== 'QUEUED' && d.attempts > 1"> · {{ t('nightAuditArchive.historyAttempts', { n: d.attempts }) }}</template>
              </small>
              <div v-if="d.pdf_sha256" class="text-xs text-muted-foreground" data-testid="delivery-hash">
                {{ t('nightAuditArchive.historyHash', { hash: shortHash(d.pdf_sha256) }) }} · {{ t('nightAuditArchive.historyRenderer', { version: d.renderer ?? '' }) }}
              </div>
              <div v-if="failure(d)" class="text-xs text-destructive">{{ failure(d) }}</div>
            </div>
            <div class="text-right">
              <Badge :variant="d.status === 'SENT' ? 'success' : d.status === 'FAILED' ? 'destructive' : 'secondary'">{{ t(`nightAuditArchive.deliveryState.${d.status}` as never) }}</Badge>
              <div v-if="d.sent_at" class="text-xs text-muted-foreground">{{ t('nightAuditArchive.historySent', { time: $dateTime(d.sent_at) }) }}</div>
            </div>
          </li>
        </ul>

        <div v-if="canResend" class="mt-4" data-testid="resend">
          <h3 class="mb-2 mt-0 text-sm font-semibold">{{ t('nightAuditArchive.resendTitle') }}</h3>
          <p v-if="!emailEnabled" class="m-0 text-sm text-muted-foreground">{{ t('nightAuditArchive.mailOffNote') }}</p>
          <p v-else-if="!recipients.length" class="m-0 text-sm text-muted-foreground" data-testid="resend-none">{{ t('nightAuditArchive.resendNone') }}</p>
          <template v-else>
            <label v-for="r in recipients" :key="r.id" class="flex cursor-pointer items-center gap-2 text-sm">
              <input v-model="resendIds" type="checkbox" :value="r.id" :name="`resend_${r.id}`" class="size-4 accent-primary" />
              {{ r.email }} <small class="text-muted-foreground">· {{ t('nightAuditArchive.historySections', { n: r.sections.length }) }}</small>
            </label>
            <p class="mb-0 mt-2 text-xs text-muted-foreground">{{ t('nightAuditArchive.resendHint') }}</p>
            <Button class="mt-3" size="sm" :disabled="resending || !resendIds.length" data-testid="resend-send" @click="resend">{{ t('nightAuditArchive.resendTo', { n: resendIds.length }) }}</Button>
          </template>
        </div>
        <p v-if="canManage" class="mb-0 mt-3 text-xs"><RouterLink to="/night-audit/recipients">{{ t('nightAuditArchive.recipientsLink') }}</RouterLink></p>
      </CardContent>
    </Card>

    <Card v-if="canManage" class="mt-4" data-testid="late">
      <CardHeader><CardTitle>{{ t('nightAuditArchive.buildLate') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="mt-0 text-sm text-muted-foreground">{{ t('nightAuditArchive.buildLateHint') }}</p>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="guardForm($event) && lateDate && rebuild(lateDate)">
          <label class="grid gap-1 text-sm">{{ t('nightAuditArchive.buildDate') }}<DateInput v-model="lateDate" name="late_date" /></label>
          <Button type="submit" size="sm" :disabled="!lateDate" data-testid="build-late">{{ t('nightAuditArchive.build') }}</Button>
        </form>
      </CardContent>
    </Card>
  </template>
</template>
