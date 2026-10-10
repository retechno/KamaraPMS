<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlPeriod } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { i18n, t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const periods = ref<GlPeriod[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const reopening = ref<{ start: string; reason: string } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const monthLabel = (start: string): string => new Date(`${start}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { month: 'long', year: 'numeric', timeZone: 'UTC' })

const columns = computed<Column<GlPeriod>[]>(() => [
  { key: 'month', label: t('accounting.pMonth') },
  { key: 'status', label: t('accounting.status') },
  { key: 'days', label: t('accounting.pDays') },
  { key: 'actions', label: '', align: 'right' },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/periods', { params: { path: { propertyId } } })
    periods.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function close(p: GlPeriod): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !(await confirm({ title: t('accounting.pCloseTitle', { month: monthLabel(p.period_start) }), description: t('accounting.pCloseHint'), destructive: true }))) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/accounting/periods/{start}/close', { params: { path: { propertyId, start: p.period_start } } })
    notice.value = t('accounting.pIsClosed', { month: monthLabel(p.period_start) })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reopen(): Promise<void> {
  const propertyId = pid.value
  const r = reopening.value
  if (propertyId === null || r === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/accounting/periods/{start}/reopen', { params: { path: { propertyId, start: r.start } }, body: { reason: r.reason.trim() } })
    notice.value = t('accounting.pIsOpen', { month: monthLabel(r.start) })
    reopening.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  periods.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('accounting.pTitle')" />
  <ErrorNotice v-if="error" :error="error" inline data-testid="period-error" />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accounting.noAccessView', { what: t('accounting.whatPeriods'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <p class="mb-4 text-sm text-muted-foreground">{{ t('accounting.pIntro') }}</p>
    <Card v-if="reopening" class="mb-4">
      <form novalidate data-testid="reopen-form" @submit.prevent="reopen">
        <CardHeader><CardTitle>{{ t('accounting.reopenTitle', { label: monthLabel(reopening.start) }) }}</CardTitle></CardHeader>
        <CardContent>
          <FormField class="max-w-md" :label="t('accounting.reason')">
            <template #default="{ id }"><Input :id="id" v-model="reopening.reason" name="reason" maxlength="500" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="reopening = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !reopening.reason.trim()" data-testid="reopen-run">{{ t('accounting.reopen') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !periods.length" :title="t('accounting.pEmpty')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="periods" row-key="period_start" :row-test-id="(p) => `period-${p.period_start}`" :caption="t('accounting.pTitle')" data-testid="periods">
        <template #cell-month="{ row }">{{ monthLabel(row.period_start) }}</template>
        <template #cell-status="{ row }">
          <Badge :variant="row.status === 'CLOSED' ? 'outline' : 'success'">{{ row.status === 'CLOSED' ? t('accounting.closed') : t('accounting.open') }}</Badge>
          <small v-if="row.reopen_reason && row.status === 'OPEN'" class="text-muted-foreground"> · {{ t('accounting.fyReopened', { reason: row.reopen_reason }) }}</small>
        </template>
        <template #cell-days="{ row }">{{ t('accounting.fyOf', { n: row.posted_days, total: row.days }) }}</template>
        <template #cell-actions="{ row }">
          <div class="flex justify-end gap-2">
            <Button v-if="can('accounting.close') && row.closable" type="button" size="sm" :disabled="busy" :data-testid="`close-${row.period_start}`" @click="close(row)">{{ t('accounting.pClose') }}</Button>
            <Button v-if="can('accounting.close') && row.reopenable" type="button" variant="outline" size="sm" :data-testid="`reopen-${row.period_start}`" @click="reopening = { start: row.period_start, reason: '' }">{{ t('accounting.reopen') }}</Button>
          </div>
        </template>
      </DataTable>
    </Card>
  </template>
</template>
