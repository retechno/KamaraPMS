<script setup lang="ts">
import FilterBar from '@/components/app/FilterBar.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { localizeDates } from '@/utils/format'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, GlAccount, Journal } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from './accountApi'
import { fromMilli, totals } from './accountMeta'

const auth = useAuthStore()
const property = usePropertyStore()

const journals = ref<Journal[]>([])
const accounts = ref<GlAccount[]>([])
const opened = ref<Journal | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ from: '', to: '', type: '', q: '' })
const creating = ref(false)
const form = reactive({ date: '', description: '', reference: '', lines: [] as { account_id: number; debit: string; credit: string; description: string; department_id: number | null }[] })
const reversing = ref<{ reason: string; asking: boolean } | null>(null)
let postKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const postable = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active))
const sums = computed(() => totals(form.lines))
const balanced = computed(() => sums.value.valid && sums.value.debit === sums.value.credit && sums.value.debit > 0n)
/** The ledger of an account for the month of the journal, so the line can be seen among its neighbours. */
function ledgerLink(accountId: number, date: string): { path: string; query: Record<string, string> } {
  const [y, m] = date.split('-').map(Number) as [number, number]
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate()
  const mm = String(m).padStart(2, '0')
  return { path: '/accounting/ledger', query: { account: String(accountId), from: `${y}-${mm}-01`, to: `${y}-${mm}-${String(last).padStart(2, '0')}` } }
}
const activeFilters = computed(() => (filter.from ? 1 : 0) + (filter.to ? 1 : 0) + (filter.type ? 1 : 0))
const typeLabel = (k: string): string => t(`journals.t_${k}` as 'journals.t_MANUAL')
const TYPES = ['DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX'] as const
const columns = computed<Column<Journal>[]>(() => [
  { key: 'journal_date', label: t('journals.date'), format: 'date' as const },
  { key: 'journal_number', label: t('journals.number'), card: 'primary' as const },
  { key: 'journal_type', label: t('journals.type'), card: 'badge' as const },
  { key: 'description', label: t('journals.description'), card: 'secondary' as const },
  { key: 'total', label: t('journals.total'), align: 'right', format: 'money' as const, card: 'money' as const },
  { key: 'reversed', label: '', hideOnMobile: true },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/journals', {
      params: {
        path: { propertyId },
        query: { from: filter.from || undefined, to: filter.to || undefined, type: (filter.type || undefined) as 'MANUAL' | undefined, q: filter.q.trim() || undefined },
      },
    })
    journals.value = data?.data ?? []
    if (!accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(j: Journal): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === j.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/journals/{id}', { params: { path: { propertyId, id: j.id } } })
    opened.value = data ?? null
    reversing.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startNew(): void {
  Object.assign(form, { date: '', description: '', reference: '' })
  form.lines = [{ account_id: 0, debit: '', credit: '', description: '', department_id: null }, { account_id: 0, debit: '', credit: '', description: '', department_id: null }]
  error.value = null
  postKey = newIdempotencyKey()
  creating.value = true
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': postKey } },
      body: {
        journal_date: form.date, description: form.description, reference: form.reference || undefined,
        lines: form.lines.map((l) => ({ account_id: l.account_id, debit: l.debit.trim() || undefined, credit: l.credit.trim() || undefined, description: l.description || undefined, department_id: l.department_id ?? undefined })),
      },
    })
    postKey = newIdempotencyKey()
    creating.value = false
    notice.value = t('journals.posted', { number: data?.journal_number ?? '' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reverse(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const j = opened.value
  if (propertyId === null || j === null || reversing.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals/{id}/reverse', {
      params: { path: { propertyId, id: j.id } }, body: { reason: reversing.value.reason.trim(), approval },
    })
    notice.value = t('journals.reversed', { number: j.journal_number, by: data?.journal_number ?? '' })
    reversing.value = null
    opened.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function postPending(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals/post-pending', { params: { path: { propertyId } } })
    notice.value = data?.posted ? t('journals.journaledDays', { n: data.posted }) : t('journals.noPending')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  journals.value = []
  accounts.value = []
  opened.value = null
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('journals.title')">
    <template #actions>
      <Button v-if="can('accounting.close')" type="button" variant="outline" :disabled="busy" data-testid="post-pending" @click="postPending">{{ t('journals.postPending') }}</Button>
      <Button v-if="can('accounting.post') && !creating" type="button" data-testid="new-journal" @click="startNew">{{ t('journals.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="journal-error">
<template v-if="error.fieldErrors?.length">
      <br />
      <span v-for="(f, i) in error.fieldErrors.slice(0, 8)" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span>
    </template>
</ErrorNotice>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('journals.noAccess', { permission: 'accounting.view' }) }}</p>

  <template v-else>
    <Card v-if="creating" class="mb-4">
      <form v-autofocus novalidate data-testid="journal-form" @submit.prevent="post">
        <CardHeader><CardTitle>{{ t('journals.formTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('journals.date')" :error="fieldError('journal_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.date" name="date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('journals.reference')">
              <template #default="{ id }"><Input :id="id" v-model="form.reference" name="reference" maxlength="100" /></template>
            </FormField>
            <FormField class="sm:col-span-2" :label="t('journals.description')" :error="fieldError('description')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.description" name="description" maxlength="300" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <table class="mt-4 w-full border-collapse text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1.5 pr-3 font-medium">{{ t('journals.account') }}</th>
                <th class="px-3 text-right font-medium">{{ t('journals.debit') }}</th>
                <th class="px-3 text-right font-medium">{{ t('journals.credit') }}</th>
                <th class="px-3 font-medium">{{ t('journals.department') }}</th>
                <th class="px-3 font-medium">{{ t('journals.note') }}</th>
                <th />
              </tr>
            </thead>
            <tbody class="[&_td]:py-1.5 [&_td]:pr-3 [&_td]:align-top">
              <tr v-for="(l, i) in form.lines" :key="i" :data-testid="`line-${i}`">
                <td>
                  <Combobox v-model="l.account_id" :name="`account_${i}`" :aria-invalid="!!fieldError(`lines[${i}].account_id`)" :options="[{ value: 0, label: `${t('journals.chooseAccount')}` }, ...postable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                  <small v-if="fieldError(`lines[${i}].account_id`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].account_id`) }}</small>
                </td>
                <td><Input v-model="l.debit" class="text-right" :name="`debit_${i}`" inputmode="decimal" :disabled="l.credit.trim() !== ''" /></td>
                <td><Input v-model="l.credit" class="text-right" :name="`credit_${i}`" inputmode="decimal" :disabled="l.debit.trim() !== ''" /></td>
                <td>
                  <DepartmentSelect v-model="l.department_id" :name="`department_${i}`" :aria-invalid="!!fieldError(`lines[${i}].department_id`)" />
                  <small v-if="fieldError(`lines[${i}].department_id`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].department_id`) }}</small>
                </td>
                <td><Input v-model="l.description" :name="`note_${i}`" maxlength="300" /></td>
                <td><Button v-if="form.lines.length > 2" type="button" variant="outline" size="sm" :data-testid="`remove-line-${i}`" @click="form.lines.splice(i, 1)">{{ t('journals.remove') }}</Button></td>
              </tr>
            </tbody>
            <tfoot>
              <tr data-testid="line-totals" class="border-t border-border">
                <td class="pt-2"><Button type="button" variant="outline" size="sm" data-testid="add-line" @click="form.lines.push({ account_id: 0, debit: '', credit: '', description: '', department_id: null })">{{ t('journals.addLine') }}</Button></td>
                <td class="pt-2 pr-3 text-right tabular-nums">{{ $money(fromMilli(sums.debit)) }}</td>
                <td class="pt-2 pr-3 text-right tabular-nums">{{ $money(fromMilli(sums.credit)) }}</td>
                <td colspan="3" class="pt-2">
                  <span v-if="!sums.valid" class="text-destructive">{{ t('journals.notNumber') }}</span>
                  <span v-else-if="sums.debit !== sums.credit" class="text-destructive" data-testid="difference">{{ t('journals.outOfBalance', { amount: $money(fromMilli(sums.debit > sums.credit ? sums.debit - sums.credit : sums.credit - sums.debit)) }) }}</span>
                  <span v-else class="text-muted-foreground">{{ t('journals.balanced') }}</span>
                </td>
              </tr>
            </tfoot>
          </table>
          <small v-if="fieldError('lines')" role="alert" class="text-xs text-destructive">{{ fieldError('lines') }}</small>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !balanced || !form.date || !form.description.trim() || form.lines.some((l) => !l.account_id)" data-testid="journal-post">{{ t('journals.post') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <FilterBar class="mb-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-5" :active="activeFilters" @submit="load">
      <template #search>
        <FormField :label="t('journals.search')"><template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('journals.searchPlaceholder')" /></template></FormField>
      </template>

          <FormField :label="t('journals.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
          <FormField :label="t('journals.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
          <FormField :label="t('journals.type')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.type" name="type">
                <option value="">{{ t('journals.all') }}</option>
                <option v-for="ty in TYPES" :key="ty" :value="ty">{{ typeLabel(ty) }}</option>
              </NativeSelect>
            </template>
          </FormField>
      <template #actions>
        <div class="flex items-end"><Button type="submit" variant="outline" data-testid="apply">{{ t('journals.apply') }}</Button></div>
      </template>
    </FilterBar>
        <EmptyState v-if="loaded && !journals.length" :title="t('journals.empty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="journals"
          row-key="id"
          cards
          clickable
          :row-test-id="(j) => `journal-${j.journal_number}`"
          :row-class="(j) => (opened?.id === j.id ? 'bg-accent' : undefined)"
          :is-expanded="(j) => opened?.id === j.id"
          :detail-test-id="() => 'journal-detail'"
          :caption="t('journals.title')"
          data-testid="journals"
          @row-click="open"
        >
          <template #cell-journal_type="{ row }"><Badge variant="outline">{{ typeLabel(row.journal_type) }}</Badge></template>
          <template #cell-description="{ row }">{{ localizeDates(row.description) }}<small v-if="row.reference" class="text-muted-foreground"> · {{ row.reference }}</small></template>
          <template #cell-reversed="{ row }"><small v-if="row.reversed_by_number" class="text-muted-foreground">{{ t('journals.reversedBy', { number: row.reversed_by_number }) }}</small></template>
          <template #detail="{ row }">
            <template v-if="opened && opened.id === row.id">
              <table class="w-full border-collapse text-sm">
                <thead>
                  <tr class="border-b border-border text-left text-xs text-muted-foreground">
                    <th class="py-1 pr-3 font-medium">#</th><th class="px-3 font-medium">{{ t('journals.account') }}</th><th class="px-3 font-medium">{{ t('journals.detail') }}</th><th class="px-3 font-medium">{{ t('journals.department') }}</th>
                    <th class="px-3 text-right font-medium">{{ t('journals.debit') }}</th><th class="pl-3 text-right font-medium">{{ t('journals.credit') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="l in opened.lines" :key="l.line_no" class="border-b border-border">
                    <td class="py-1 pr-3">{{ l.line_no }}</td>
                    <td class="px-3"><RouterLink :to="ledgerLink(l.account_id, row.journal_date)" class="text-primary hover:underline" :data-testid="`ledger-link-${l.line_no}`">{{ l.account_code }} · {{ l.account_name }}</RouterLink></td>
                    <td class="px-3"><small class="text-muted-foreground">{{ l.description }}</small></td>
                    <td class="px-3" :data-testid="`line-department-${l.line_no}`"><small v-if="l.department_code" :title="l.department_name">{{ l.department_code }}</small></td>
                    <td class="px-3 text-right tabular-nums">{{ Number(l.debit) ? l.debit : '' }}</td>
                    <td class="pl-3 text-right tabular-nums">{{ Number(l.credit) ? l.credit : '' }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-if="opened.reason" class="mb-0 mt-2 text-sm text-muted-foreground">{{ t('journals.reasonLine', { reason: opened.reason }) }}</p>
              <div v-if="can('accounting.post') && opened.journal_type === 'MANUAL' && !opened.reversed_by_journal_id" class="mt-2">
                <Button v-if="!reversing" type="button" variant="outline" size="sm" data-testid="reverse" @click="reversing = { reason: '', asking: false }">{{ t('journals.reverseEllipsis') }}</Button>
                <form v-else class="flex flex-wrap items-end gap-3" novalidate @submit.prevent="reversing.asking = true">
                  <FormField class="w-80" :label="t('journals.reason')">
                    <template #default="{ id }"><Input :id="id" v-model="reversing.reason" name="reason" maxlength="500" /></template>
                  </FormField>
                  <Button type="button" variant="outline" @click="reversing = null">{{ t('common.cancel') }}</Button>
                  <Button type="submit" :disabled="!reversing.reason.trim()" data-testid="reverse-ask">{{ t('journals.reverseWithApproval') }}</Button>
                </form>
              </div>
            </template>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
  <ApprovalDialog v-if="reversing?.asking" :title="t('journals.approveReversal')" :busy="busy" :error="dialogError" @approve="reverse" @cancel="reversing = null; dialogError = null" />
</template>
