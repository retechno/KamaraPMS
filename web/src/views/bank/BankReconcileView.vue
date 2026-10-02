<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, BankStatementDetail, GlAccount, UnclearedLine } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
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
const selectedLine = ref<number | null>(null)
const picked = ref<number[]>([])
const adjust = reactive({ open: false, account_id: 0, description: '' })
const reopening = ref<{ reason: string; asking: boolean } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const sid = computed(() => Number(props.id))
const isOpen = computed(() => statement.value?.status === 'OPEN')
const editable = computed(() => isOpen.value && can('bank.reconcile'))
const pickedTotal = computed(() => {
  let sum = 0n
  for (const u of uncleared.value) if (picked.value.includes(u.journal_line_id)) sum += toMilli(u.amount) ?? 0n
  return sum
})
const line = computed(() => statement.value?.lines.find((l) => l.id === selectedLine.value) ?? null)
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
    if (selectedLine.value !== null && statement.value?.lines.find((l) => l.id === selectedLine.value)?.matched) selectedLine.value = null
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

async function match(): Promise<void> {
  const ok = await act(() => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/clearings', { params: base(), body: { statement_line_id: selectedLine.value, journal_line_ids: picked.value } }), 'Matched.')
  if (ok) picked.value = []
}

async function unmatch(clearingId: number): Promise<void> {
  await act(() => api.DELETE('/api/v1/properties/{propertyId}/bank/statements/{id}/clearings/{clearingId}', { params: { path: { ...base().path, clearingId } } }), 'Matching undone.')
}

async function autoMatch(): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/auto-match', { params: base() })
    notice.value = `${data?.matched ?? 0} line(s) matched, ${data?.remaining ?? 0} left for you.`
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
    `Line ${l.line_no} posted to the books and matched.`,
  )
  if (ok) {
    adjust.open = false
    selectedLine.value = null
  }
}

async function reconcile(): Promise<void> {
  await act(() => api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/reconcile', { params: base() }), 'The statement is reconciled.')
}

async function reopen(approval: Approval): Promise<void> {
  if (reopening.value === null) return
  dialogError.value = null
  busy.value = true
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements/{id}/reopen', { params: base(), body: { reason: reopening.value.reason.trim(), approval } })
    if (data) statement.value = data
    notice.value = 'The statement is open again.'
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
  selectedLine.value = null
  picked.value = []
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Reconcile {{ statement ? `${statement.bank_name}: ${statement.period_from} – ${statement.period_to}` : 'a statement' }}</h1>
    <RouterLink to="/bank/statements">All statements</RouterLink>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="reconcile-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">Your role at this property cannot see bank statements: the <code>bank.view</code> permission is needed.</p>
  <template v-else-if="statement">
    <section class="card summary" data-testid="summary">
      <dl>
        <div><dt>Bank closing balance</dt><dd data-testid="closing">{{ statement.summary.statement_closing }}</dd></div>
        <div><dt>Money in transit (in the books, not at the bank)</dt><dd>{{ statement.summary.uncleared_in }}</dd></div>
        <div><dt>Outstanding payments</dt><dd>{{ statement.summary.uncleared_out }}</dd></div>
        <div><dt>Adjusted bank balance</dt><dd data-testid="adjusted">{{ statement.summary.adjusted_bank }}</dd></div>
        <div><dt>Book balance</dt><dd data-testid="book">{{ statement.summary.book_balance }}</dd></div>
        <div><dt>Difference</dt><dd data-testid="difference" :class="{ bad: Number(statement.summary.difference) !== 0 }">{{ statement.summary.difference }}</dd></div>
      </dl>
      <p v-if="statement.status === 'RECONCILED'" class="notice" data-testid="reconciled">Reconciled{{ statement.reconciled_at ? ` on ${statement.reconciled_at.slice(0, 10)}` : '' }}. A reconciled statement is final.</p>
      <ul v-else-if="statement.summary.blockers.length" class="blockers" data-testid="blockers">
        <li v-for="b in statement.summary.blockers" :key="b">{{ b }}</li>
      </ul>
      <p v-else class="notice" data-testid="ready">Everything is matched: the statement can be reconciled.</p>
      <div class="form-actions">
        <button v-if="editable" type="button" :disabled="busy" data-testid="auto-match" @click="autoMatch">Match automatically</button>
        <button v-if="editable" type="button" class="btn-primary" :disabled="busy || !statement.summary.can_reconcile" data-testid="reconcile" @click="reconcile">Reconcile</button>
        <button v-if="statement.status === 'RECONCILED' && can('bank.reconcile')" type="button" data-testid="reopen" @click="reopening = { reason: '', asking: false }">Reopen…</button>
      </div>
    </section>

    <form v-if="reopening && !reopening.asking" class="card" novalidate data-testid="reopen-form" @submit.prevent="reopening.asking = true">
      <label class="field"><span>Reason for reopening</span><input v-model="reopening.reason" name="reason" maxlength="500" /></label>
      <div class="form-actions">
        <button type="button" @click="reopening = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="!reopening.reason.trim()" data-testid="reopen-ask">Continue</button>
      </div>
    </form>

    <div class="panes">
      <section class="card">
        <h2>Bank statement lines</h2>
        <table class="list" data-testid="lines">
          <thead><tr><th v-if="editable" /><th>Date</th><th>Detail</th><th class="num">Amount</th><th>Matched with</th></tr></thead>
          <tbody>
            <tr v-for="l in statement.lines" :key="l.id" :class="{ done: l.matched, selected: selectedLine === l.id }" :data-testid="`line-${l.line_no}`">
              <td v-if="editable"><input v-model="selectedLine" type="radio" name="line" :value="l.id" :disabled="l.matched" :data-testid="`pick-line-${l.line_no}`" /></td>
              <td>{{ l.line_date }}</td>
              <td>{{ l.description }}<small v-if="l.reference" class="muted"> · {{ l.reference }}</small></td>
              <td class="num">{{ l.amount }}</td>
              <td>
                <span v-for="c in l.clearings" :key="c.id" class="chip">
                  {{ c.journal_number }} · {{ c.amount }}
                  <button v-if="editable" type="button" class="link" :data-testid="`unmatch-${c.id}`" @click="unmatch(c.id)">undo</button>
                </span>
                <small v-if="!l.matched && l.clearings.length" class="error-text">{{ l.cleared }} of {{ l.amount }}</small>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="editable && line && !line.matched" class="adjust">
          <button v-if="!adjust.open" type="button" data-testid="adjust-open" @click="startAdjust">Post line {{ line.line_no }} to the books…</button>
          <form v-else novalidate data-testid="adjust-form" @submit.prevent="postAdjust">
            <p class="muted">Posts {{ line.description || `line ${line.line_no}` }} ({{ line.amount }}) against the account you choose, dated {{ line.line_date }}, and matches it.</p>
            <label class="field">
              <span>Account</span>
              <select v-model.number="adjust.account_id" name="adjust_account">
                <option :value="0">Choose an account</option>
                <option v-for="a in chargeable" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </select>
            </label>
            <label class="field"><span>Description</span><input v-model="adjust.description" name="adjust_description" maxlength="300" /></label>
            <button type="button" @click="adjust.open = false">Cancel</button>
            <button type="submit" class="btn-primary" :disabled="busy || !adjust.account_id" data-testid="adjust-post">Post and match</button>
          </form>
        </div>
      </section>

      <section class="card">
        <h2>Journal lines not cleared yet</h2>
        <p v-if="!uncleared.length" class="muted" data-testid="no-uncleared">Everything in the books up to {{ statement.period_to }} is cleared.</p>
        <table v-else class="list" data-testid="uncleared">
          <thead><tr><th v-if="editable" /><th>Date</th><th>Journal</th><th>Detail</th><th class="num">Amount</th></tr></thead>
          <tbody>
            <tr v-for="u in uncleared" :key="u.journal_line_id" :data-testid="`uncleared-${u.journal_line_id}`">
              <td v-if="editable"><input v-model="picked" type="checkbox" :value="u.journal_line_id" /></td>
              <td>{{ u.journal_date }}</td>
              <td>{{ u.journal_number }}</td>
              <td>{{ u.description }}</td>
              <td class="num">{{ u.amount }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="editable && picked.length" class="form-actions">
          <span class="muted" data-testid="picked-total">{{ picked.length }} selected · {{ fromMilli(pickedTotal) }}</span>
          <button type="button" class="btn-primary" :disabled="busy" data-testid="match" @click="match">{{ selectedLine !== null ? `Match with line ${line?.line_no}` : 'Clear without a statement line' }}</button>
        </div>
        <p v-if="editable && picked.length && selectedLine === null" class="muted">Without a statement line only lines that offset each other, or from before the first statement, can be cleared.</p>
      </section>
    </div>
  </template>
  <ApprovalDialog v-if="reopening?.asking" title="Approve reopening" :busy="busy" :error="dialogError" @approve="reopen" @cancel="reopening = null; dialogError = null" />
</template>

<style scoped>
.summary dl {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 8px 16px;
  margin: 0 0 8px;
}
.summary dt {
  font-size: 0.8rem;
  color: var(--muted, #6b7280);
}
.summary dd {
  margin: 0;
  font-weight: 600;
}
.bad {
  color: var(--danger, #b91c1c);
}
.blockers {
  margin: 4px 0 8px;
  color: var(--danger, #b91c1c);
}
.panes {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
  gap: 12px;
}
.num {
  text-align: right;
  white-space: nowrap;
}
.done td {
  color: var(--muted, #6b7280);
}
.selected td {
  background: var(--accent-soft);
}
.chip {
  display: inline-block;
  margin-right: 6px;
  font-size: 0.85rem;
}
.link {
  border: 0;
  background: none;
  color: inherit;
  text-decoration: underline;
  cursor: pointer;
  padding: 0 2px;
}
.adjust {
  margin-top: 8px;
}
</style>
