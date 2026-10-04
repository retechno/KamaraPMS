<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, BankStatementDetail, GlAccount, UnclearedLine } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from '@/views/accounting/accountApi'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

const props = defineProps<{ id: string }>()

const auth = useAuthStore()
const property = usePropertyStore()

const statement = ref<BankStatementDetail | null>(null)
const uncleared = ref<UnclearedLine[]>([])
const accounts = ref<GlAccount[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const selectedLines = ref<number[]>([])
const settle = reactive({ open: false, key: 'CARD', fee_account_id: 0, description: '', lines: [] as UnclearedLine[], picked: [] as number[] })
const picked = ref<number[]>([])
const suggestion = ref<{ matched: boolean; difference: string; expected_fee: string } | null>(null)
const adjust = reactive({ open: false, account_id: 0, description: '' })
const reopening = ref<{ reason: string; asking: boolean } | null>(null)

type StatementLine = BankStatementDetail['lines'][number]
const lineColumns = computed<Column<StatementLine>[]>(() => [
  ...(editable.value ? [{ key: 'pick', label: '' }] : []),
  { key: 'line_date', label: t('reconcile.date'), format: 'date' as const },
  { key: 'description', label: t('reconcile.detail') },
  { key: 'amount', label: t('reconcile.amount'), align: 'right' as const, format: 'money' as const },
  { key: 'clearings', label: t('reconcile.matchedWith') },
])
const unclearedColumns = computed<Column<UnclearedLine>[]>(() => [
  ...(editable.value ? [{ key: 'pick', label: '' }] : []),
  { key: 'journal_date', label: t('reconcile.date'), format: 'date' as const },
  { key: 'journal_number', label: t('reconcile.journal') },
  { key: 'description', label: t('reconcile.detail') },
  { key: 'remaining', label: t('reconcile.left'), align: 'right' as const, format: 'money' as const },
])
const settleColumns = computed<Column<UnclearedLine>[]>(() => [
  { key: 'pick', label: '' },
  { key: 'journal_date', label: t('reconcile.date'), format: 'date' as const },
  { key: 'description', label: t('reconcile.detail') },
  { key: 'amount', label: t('reconcile.amount'), align: 'right' as const, format: 'money' as const },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const sid = computed(() => Number(props.id))
const isOpen = computed(() => statement.value?.status === 'OPEN')
const editable = computed(() => isOpen.value && can('bank.reconcile'))
const pickedTotal = computed(() => {
  let sum = 0n
  for (const u of uncleared.value) if (picked.value.includes(u.journal_line_id)) sum += toMilli(u.remaining) ?? 0n
  return sum
})
const chosenLines = computed(() => (statement.value?.lines ?? []).filter((l) => selectedLines.value.includes(l.id)))
const neededTotal = computed(() => chosenLines.value.reduce((sum, l) => sum + (toMilli(l.amount) ?? 0n) - (toMilli(l.cleared) ?? 0n), 0n))
/** The one statement line a line action (post to the books, settle) works on. */
const line = computed(() => (chosenLines.value.length === 1 ? (chosenLines.value[0] ?? null) : null))
const settleGross = computed(() => settle.lines.filter((l) => settle.picked.includes(l.journal_line_id)).reduce((sum, l) => sum + (toMilli(l.amount) ?? 0n), 0n))
const settleNet = computed(() => toMilli(line.value?.amount ?? '0') ?? 0n)
const chargeable = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active))
const base = () => ({ path: { propertyId: pid.value as number, id: sid.value } })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('bank.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/statements/{id}', { params: base() })
    statement.value = data ?? null
    const res = await api.GET('/api/v1/properties/{propertyId}/bank/statements/{id}/uncleared', { params: base() })
    uncleared.value = res.data?.data ?? []
    picked.value = picked.value.filter((p) => uncleared.value.some((u) => u.journal_line_id === p))
    selectedLines.value = selectedLines.value.filter((id) => statement.value?.lines.find((l) => l.id === id && !l.matched))
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

/** Runs one of the actions on the statement and shows the statement it answers with. */
async function act(run: () => Promise<{ data?: BankStatementDetail }>, done: string): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await run()
    if (data) statement.value = data
    notice.value = done
    await load()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

/**
 * Spreads the journal lines chosen over the statement lines chosen, in order, each taking what it can: a journal line
 * holding the total of several transfers is cleared in parts by the statement lines, several journal lines can make up
 * one statement line. Amounts are thousandths, never floats.
 */
function allocations(): { statement_line_id: number; journal_line_id: number; amount: string }[] {
  const left = new Map(uncleared.value.filter((u) => picked.value.includes(u.journal_line_id)).map((u) => [u.journal_line_id, toMilli(u.remaining) ?? 0n]))
  const out: { statement_line_id: number; journal_line_id: number; amount: string }[] = []
  for (const l of chosenLines.value) {
    let need = (toMilli(l.amount) ?? 0n) - (toMilli(l.cleared) ?? 0n)
    for (const [jid, rem] of left) {
      if (need === 0n) break
      if (rem === 0n || (rem > 0n) !== (need > 0n)) continue
      const take = (rem > 0n ? rem : -rem) < (need > 0n ? need : -need) ? rem : need
      out.push({ statement_line_id: l.id, journal_line_id: jid, amount: fromMilli(take) })
      left.set(jid, rem - take)
      need -= take
    }
  }
  return out
}

async function match(): Promise<void> {
  const parts = chosenLines.value.length ? allocations() : []
  if (chosenLines.value.length && !parts.length) {
    error.value = null
    notice.value = t('reconcile.nothingMatched')
    return
  }
  const body = chosenLines.value.length ? { allocations: parts } : { statement_line_id: null, journal_line_ids: picked.value }
  const ok = await act(() => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/clearings', { params: base(), body }), t('reconcile.matched'))
  if (ok) picked.value = []
}

async function unmatch(clearingId: number): Promise<void> {
  await act(() => api.DELETE('/api/v1/properties/{propertyId}/bank/statements/{id}/clearings/{clearingId}', { params: { path: { ...base().path, clearingId } } }), t('reconcile.unmatched'))
}

async function autoMatch(): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/auto-match', { params: base() })
    notice.value = t('reconcile.autoResult', { matched: data?.matched ?? 0, remaining: data?.remaining ?? 0 })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function startAdjust(): void {
  Object.assign(adjust, { open: true, account_id: 0, description: '' })
}

async function postAdjust(): Promise<void> {
  const l = line.value
  if (!l) return
  const ok = await act(
    () => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/lines/{lineId}/adjust', { params: { path: { ...base().path, lineId: l.id } }, body: { account_id: adjust.account_id, description: adjust.description || undefined } }),
    t('reconcile.posted', { n: l.line_no }),
  )
  if (ok) {
    adjust.open = false
    selectedLines.value = []
  }
}

async function startSettle(): Promise<void> {
  Object.assign(settle, { open: true, fee_account_id: 0, description: '', picked: [] })
  await loadSettleLines()
}

async function suggest(): Promise<void> {
  const l = line.value
  if (!l) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/statements/{id}/lines/{lineId}/settlement-proposal', { params: { path: { ...base().path, lineId: l.id }, query: { account_key: settle.key as 'CARD' } } })
    settle.picked = data?.journal_line_ids ?? []
    suggestion.value = data ? { matched: data.matched, difference: data.difference, expected_fee: data.expected_fee } : null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadSettleLines(): Promise<void> {
  settle.picked = []
  suggestion.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/statements/{id}/settlement-lines', { params: { ...base(), query: { account_key: settle.key as 'CARD' } } })
    settle.lines = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function postSettle(): Promise<void> {
  const l = line.value
  if (!l) return
  const ok = await act(
    () => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/lines/{lineId}/settle', {
      params: { path: { ...base().path, lineId: l.id } },
      body: { account_key: settle.key as 'CARD', journal_line_ids: settle.picked, fee_account_id: settle.fee_account_id || undefined, description: settle.description || undefined },
    }),
    t('reconcile.settled', { n: l.line_no }),
  )
  if (ok) {
    settle.open = false
    selectedLines.value = []
  }
}

async function reconcile(): Promise<void> {
  await act(() => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/reconcile', { params: base() }), t('reconcile.isReconciled'))
}

async function reopen(approval: Approval): Promise<void> {
  if (reopening.value === null) return
  dialogError.value = null
  busy.value = true
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/reopen', { params: base(), body: { reason: reopening.value.reason.trim(), approval } })
    if (data) statement.value = data
    notice.value = t('reconcile.isReopened')
    reopening.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch([() => pid.value, sid], () => {
  statement.value = null
  uncleared.value = []
  loaded.value = false
  selectedLines.value = []
  picked.value = []
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('reconcile.title', { statement: statement ? `${statement.bank_name}: ${statement.period_from} – ${statement.period_to}` : t('reconcile.aStatement') })">
    <template #actions><RouterLink to="/bank/statements" class="text-sm text-primary hover:underline">{{ t('reconcile.all') }}</RouterLink></template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="reconcile-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">{{ t('reconcile.noAccess', { permission: 'bank.view' }) }}</p>
  <template v-else-if="statement">
    <Card class="mb-4" data-testid="summary">
      <CardContent class="pt-4">
        <dl class="m-0 grid gap-x-4 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.closing') }}</dt><dd class="m-0 font-semibold" data-testid="closing">{{ $money(statement.summary.statement_closing) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.inTransit') }}</dt><dd class="m-0 font-semibold">{{ $money(statement.summary.uncleared_in) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.outstanding') }}</dt><dd class="m-0 font-semibold">{{ $money(statement.summary.uncleared_out) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.adjusted') }}</dt><dd class="m-0 font-semibold" data-testid="adjusted">{{ $money(statement.summary.adjusted_bank) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.book') }}</dt><dd class="m-0 font-semibold" data-testid="book">{{ $money(statement.summary.book_balance) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('reconcile.difference') }}</dt><dd :class="['m-0 font-semibold', Number(statement.summary.difference) !== 0 && 'text-destructive']" data-testid="difference">{{ $money(statement.summary.difference) }}</dd></div>
        </dl>
        <p v-if="statement.status === 'RECONCILED'" class="notice mt-3" data-testid="reconciled">{{ statement.reconciled_at ? t('reconcile.reconciledOn', { date: statement.reconciled_at.slice(0, 10) }) : t('reconcile.reconciledNoDate') }}</p>
        <ul v-else-if="statement.summary.blockers.length" class="mb-0 mt-3 text-destructive" data-testid="blockers">
          <li v-for="b in statement.summary.blockers" :key="b">{{ b }}</li>
        </ul>
        <p v-else class="notice mt-3" data-testid="ready">{{ t('reconcile.ready') }}</p>
        <div class="mt-4 flex flex-wrap justify-end gap-2">
          <Button v-if="editable" type="button" variant="outline" :disabled="busy" data-testid="auto-match" @click="autoMatch">{{ t('reconcile.autoMatch') }}</Button>
          <Button v-if="editable" type="button" :disabled="busy || !statement.summary.can_reconcile" data-testid="reconcile" @click="reconcile">{{ t('reconcile.reconcile') }}</Button>
          <Button v-if="statement.status === 'RECONCILED' && can('bank.reconcile')" type="button" variant="outline" data-testid="reopen" @click="reopening = { reason: '', asking: false }">{{ t('reconcile.reopen') }}</Button>
        </div>
      </CardContent>
    </Card>

    <Card v-if="reopening && !reopening.asking" class="mb-4">
      <form novalidate data-testid="reopen-form" @submit.prevent="reopening.asking = true">
        <CardContent class="pt-4">
          <FormField class="max-w-md" :label="t('reconcile.reopenReason')">
            <template #default="{ id }"><Input :id="id" v-model="reopening.reason" name="reason" maxlength="500" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="reopening = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="!reopening.reason.trim()" data-testid="reopen-ask">{{ t('reconcile.continue') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <div class="grid gap-4 xl:grid-cols-2">
      <Card>
        <CardHeader><CardTitle>{{ t('reconcile.statementLines') }}</CardTitle></CardHeader>
        <CardContent>
          <DataTable :columns="lineColumns" :rows="statement.lines" row-key="id" :row-test-id="(l) => `line-${l.line_no}`" :row-class="(l) => (l.matched ? 'done text-muted-foreground' : selectedLines.includes(l.id) ? 'bg-accent' : undefined)" :caption="t('reconcile.statementLines')" data-testid="lines">
            <template #cell-pick="{ row }"><input v-model="selectedLines" type="checkbox" class="size-4 accent-primary" name="line" :value="row.id" :disabled="row.matched" :data-testid="`pick-line-${row.line_no}`" /></template>
            <template #cell-description="{ row }">{{ row.description }}<small v-if="row.reference" class="text-muted-foreground"> · {{ row.reference }}</small></template>
            <template #cell-clearings="{ row }">
              <span v-for="c in row.clearings" :key="c.id" class="mr-1.5 inline-block text-sm">
                {{ c.journal_number }} · {{ $money(c.amount) }}
                <button v-if="editable" type="button" class="cursor-pointer border-0 bg-transparent px-0.5 underline" :data-testid="`unmatch-${c.id}`" @click="unmatch(c.id)">{{ t('reconcile.undo') }}</button>
              </span>
              <small v-if="!row.matched && row.clearings.length" class="text-destructive">{{ t('reconcile.partOf', { cleared: $money(row.cleared), amount: $money(row.amount) }) }}</small>
            </template>
          </DataTable>
          <div v-if="editable && line && !line.matched" class="mt-3">
            <div class="flex flex-wrap gap-2">
              <Button v-if="!adjust.open && !settle.open" type="button" variant="outline" size="sm" data-testid="adjust-open" @click="startAdjust">{{ t('reconcile.postLine', { n: line.line_no }) }}</Button>
              <Button v-if="!adjust.open && !settle.open && Number(line.amount) > 0" type="button" variant="outline" size="sm" data-testid="settle-open" @click="startSettle">{{ t('reconcile.settleOpen') }}</Button>
            </div>
            <form v-if="adjust.open" class="grid gap-3" novalidate data-testid="adjust-form" @submit.prevent="postAdjust">
              <p class="m-0 text-sm text-muted-foreground">{{ t('reconcile.postHint', { what: line.description || t('reconcile.lineN', { n: line.line_no }), amount: $money(line.amount), date: $date(line.line_date) }) }}</p>
              <FormField :label="t('reconcile.account')">
                <template #default="{ id }">
                  <Combobox :id="id" v-model="adjust.account_id" name="adjust_account" :options="[{ value: 0, label: `${t('reconcile.chooseAccount')}` }, ...chargeable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                </template>
              </FormField>
              <FormField :label="t('reconcile.description')">
                <template #default="{ id }"><Input :id="id" v-model="adjust.description" name="adjust_description" maxlength="300" /></template>
              </FormField>
              <div class="flex justify-end gap-2">
                <Button type="button" variant="outline" @click="adjust.open = false">{{ t('common.cancel') }}</Button>
                <Button type="submit" :disabled="busy || !adjust.account_id" data-testid="adjust-post">{{ t('reconcile.postAndMatch') }}</Button>
              </div>
            </form>
            <form v-if="settle.open" class="grid gap-3" novalidate data-testid="settle-form" @submit.prevent="postSettle">
              <p class="m-0 text-sm text-muted-foreground">{{ t('reconcile.settleHint', { amount: $money(line.amount) }) }}</p>
              <FormField :label="t('reconcile.paymentsOf')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="settle.key" name="settle_key" @change="loadSettleLines">
                    <option value="CARD">{{ t('reconcile.card') }}</option>
                    <option value="OTHER_PAYMENT">{{ t('reconcile.ewallet') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <div v-if="settle.lines.length" class="flex flex-wrap items-center gap-3">
                <Button type="button" variant="outline" size="sm" data-testid="suggest" @click="suggest">{{ t('reconcile.suggest') }}</Button>
                <small v-if="suggestion" data-testid="suggestion" :class="suggestion.matched ? 'text-muted-foreground' : 'text-destructive'">{{ suggestion.matched ? t('reconcile.suggestMatched', { fee: $money(suggestion.expected_fee) }) : t('reconcile.suggestOff', { difference: $money(suggestion.difference) }) }}</small>
              </div>
              <p v-if="!settle.lines.length" class="m-0 text-sm text-muted-foreground" data-testid="no-settle-lines">{{ t('reconcile.noSettle') }}</p>
              <DataTable v-else :columns="settleColumns" :rows="settle.lines" row-key="journal_line_id" :row-test-id="(u) => `settle-${u.journal_line_id}`" :caption="t('reconcile.paymentsOf')" data-testid="settle-lines">
                <template #cell-pick="{ row }"><input v-model="settle.picked" type="checkbox" class="size-4 accent-primary" :value="row.journal_line_id" /></template>
              </DataTable>
              <p v-if="settle.picked.length" class="m-0 text-sm" data-testid="settle-summary">
                {{ t('reconcile.settleSummary', { gross: $money(fromMilli(settleGross)), net: $money(fromMilli(settleNet)) }) }}
                <b :class="settleGross < settleNet && 'text-destructive'">{{ t('reconcile.commission', { amount: $money(fromMilli(settleGross - settleNet)) }) }}</b>
              </p>
              <FormField v-if="settleGross > settleNet" :label="t('reconcile.commissionAccount')">
                <template #default="{ id }">
                  <Combobox :id="id" v-model="settle.fee_account_id" name="settle_fee" :options="[{ value: 0, label: `${t('reconcile.chooseAccount')}` }, ...chargeable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                </template>
              </FormField>
              <FormField :label="t('reconcile.description')">
                <template #default="{ id }"><Input :id="id" v-model="settle.description" name="settle_description" maxlength="300" /></template>
              </FormField>
              <div class="flex justify-end gap-2">
                <Button type="button" variant="outline" @click="settle.open = false">{{ t('common.cancel') }}</Button>
                <Button type="submit" :disabled="busy || !settle.picked.length || settleGross < settleNet || (settleGross > settleNet && !settle.fee_account_id)" data-testid="settle-post">{{ t('reconcile.settleAndMatch') }}</Button>
              </div>
            </form>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>{{ t('reconcile.unclearedTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <p v-if="!uncleared.length" class="m-0 text-sm text-muted-foreground" data-testid="no-uncleared">{{ t('reconcile.allCleared', { date: statement.period_to }) }}</p>
          <DataTable v-else :columns="unclearedColumns" :rows="uncleared" row-key="journal_line_id" :row-test-id="(u) => `uncleared-${u.journal_line_id}`" :caption="t('reconcile.unclearedTitle')" data-testid="uncleared">
            <template #cell-pick="{ row }"><input v-model="picked" type="checkbox" class="size-4 accent-primary" :value="row.journal_line_id" /></template>
            <template #cell-remaining="{ row }">{{ $money(row.remaining) }}<small v-if="row.cleared !== '0'" class="text-muted-foreground">{{ ` ${t('reconcile.ofAmount', { amount: $money(row.amount) })}` }}</small></template>
          </DataTable>
          <div v-if="editable && picked.length" class="mt-3 flex items-center justify-end gap-3">
            <span class="text-sm text-muted-foreground" data-testid="picked-total">{{ t('reconcile.selected', { n: picked.length, total: $money(fromMilli(pickedTotal)) }) }}</span>
            <Button type="button" :disabled="busy" data-testid="match" @click="match">{{ chosenLines.length === 1 ? t('reconcile.matchLine', { n: chosenLines[0]?.line_no ?? '' }) : chosenLines.length ? t('reconcile.matchLines', { n: chosenLines.length }) : t('reconcile.clearWithout') }}</Button>
          </div>
          <p v-if="editable && picked.length && chosenLines.length > 1" class="mb-0 mt-2 text-sm text-muted-foreground" data-testid="spread-hint">{{ t('reconcile.spreadHint', { n: chosenLines.length, total: $money(fromMilli(neededTotal)) }) }}</p>
          <p v-if="editable && picked.length && !chosenLines.length" class="mb-0 mt-2 text-sm text-muted-foreground">{{ t('reconcile.withoutLineHint') }}</p>
        </CardContent>
      </Card>
    </div>
  </template>
  <ApprovalDialog v-if="reopening?.asking" :title="t('reconcile.approveReopen')" :busy="busy" :error="dialogError" @approve="reopen" @cancel="reopening = null; dialogError = null" />
</template>
