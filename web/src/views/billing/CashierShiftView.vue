<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, CashierSettings, CashierShift, GlAccount } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from '@/views/accounting/accountApi'

const auth = useAuthStore()
const property = usePropertyStore()

const current = ref<CashierShift | null>(null)
const shifts = ref<CashierShift[]>([])
const settings = ref<CashierSettings | null>(null)
const chart = ref<GlAccount[]>([])
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const loaded = ref(false)

const openForm = reactive({ drawer: '', opening_float: '' })
const move = reactive({ kind: 'DROP' as 'DROP' | 'PAY_IN' | 'PAY_OUT', amount: '', account_id: 0, reason: '' })
const closing = reactive({ open: false, counted: '', reason: '', asking: false })
const settingsForm = reactive({ require_shift_for_cash: true, max_variance: '0', block_night_audit: true })
// One key per attempt: kept while a request may have been lost, renewed once the server has answered.
let moveKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const canShift = computed(() => can('cashier.shift'))
const canManage = computed(() => can('cashier.shift_manage'))
const canSee = computed(() => canShift.value || canManage.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const postable = computed(() => chart.value.filter((a) => a.is_postable && a.is_active && a.code !== '1110'))

const columns = computed<Column<CashierShift>[]>(() => [
  { key: 'number', label: t('shifts.number') },
  { key: 'user_name', label: t('shifts.cashier') },
  { key: 'drawer', label: t('shifts.drawer') },
  { key: 'business_date_opened', label: t('shifts.opened'), format: 'date' as const },
  { key: 'expected_cash', label: t('shifts.expected'), align: 'right', format: 'money' as const },
  { key: 'counted_cash', label: t('shifts.counted'), align: 'right', format: 'money' as const },
  { key: 'over_short', label: t('shifts.overShort'), align: 'right' },
  { key: 'status', label: t('setup.status') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canSee.value) return
  error.value = null
  try {
    const [c, l, s] = await Promise.all([
      canShift.value ? api.GET('/api/v1/properties/{propertyId}/cashier/shifts/current', { params: { path: { propertyId } } }) : Promise.resolve({ data: undefined }),
      api.GET('/api/v1/properties/{propertyId}/cashier/shifts', { params: { path: { propertyId }, query: { limit: 30 } } }),
      api.GET('/api/v1/properties/{propertyId}/cashier/settings', { params: { path: { propertyId } } }),
    ])
    current.value = c.data?.shift ?? null
    shifts.value = l.data?.data ?? []
    settings.value = s.data ?? null
    if (s.data) Object.assign(settingsForm, s.data)
    if (canShift.value && !current.value && !openForm.opening_float) {
      const f = await api.GET('/api/v1/properties/{propertyId}/cashier/shifts/suggested-float', { params: { path: { propertyId }, query: { drawer: openForm.drawer || undefined } } })
      openForm.opening_float = f.data?.opening_float ?? ''
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadChart(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || chart.value.length || !auth.can('accounting.view', propertyId)) return
  try {
    chart.value = await listAccounts(propertyId, { active: true })
  } catch {
    // the cashier then types the id of the account
  }
}

async function openShift(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/cashier/shifts', {
      params: { path: { propertyId } },
      body: { drawer: openForm.drawer.trim() || undefined, opening_float: openForm.opening_float.trim() || undefined },
    })
    notice.value = t('shifts.opened_notice')
    openForm.opening_float = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function moveCash(): Promise<void> {
  const propertyId = pid.value
  const sh = current.value
  if (propertyId === null || !sh) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/cashier/shifts/{id}/movements', {
      params: { path: { propertyId, id: sh.id }, header: { 'Idempotency-Key': moveKey } },
      body: { kind: move.kind, amount: move.amount.trim(), account_id: move.kind === 'DROP' ? undefined : move.account_id || undefined, reason: move.reason.trim() },
    })
    moveKey = newIdempotencyKey()
    move.amount = ''
    move.reason = ''
    notice.value = t('shifts.moved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) moveKey = newIdempotencyKey() // the server answered: the next submit is a new attempt
  } finally {
    busy.value = false
  }
}

function startClose(): void {
  closing.open = true
  closing.asking = false
  closing.counted = ''
  closing.reason = ''
  error.value = null
  notice.value = ''
}

/** The difference the count makes against the cash expected, as the server will take it (shown as a hint). */
const difference = computed(() => {
  const exp = Number(current.value?.cash?.expected ?? 'NaN')
  const counted = Number(closing.counted)
  if (!closing.counted.trim() || Number.isNaN(exp) || Number.isNaN(counted)) return null
  return counted - exp
})

async function close(approval?: Approval): Promise<void> {
  const propertyId = pid.value
  const sh = current.value
  if (propertyId === null || !sh) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/cashier/shifts/{id}/close', {
      params: { path: { propertyId, id: sh.id } },
      body: { counted_cash: closing.counted.trim(), reason: closing.reason.trim() || undefined, approval },
    })
    closing.open = false
    closing.asking = false
    notice.value = t('shifts.closedNotice')
    await load()
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    if (failure?.code === 'APPROVAL_REQUIRED') {
      closing.asking = true // the difference is beyond the limit: ask for the approval
    } else if (failure && failure.code !== 'APPROVAL_INVALID_CREDENTIALS') {
      error.value = failure
      closing.asking = false
    } else {
      dialogError.value = failure
    }
  } finally {
    busy.value = false
  }
}

async function saveSettings(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.PUT('/api/v1/properties/{propertyId}/cashier/settings', {
      params: { path: { propertyId } },
      body: { require_shift_for_cash: settingsForm.require_shift_for_cash, max_variance: settingsForm.max_variance.trim(), block_night_audit: settingsForm.block_night_audit },
    })
    notice.value = t('shifts.settingsSaved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => property.currentId, () => {
  current.value = null
  shifts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
watch(() => move.kind, (k) => {
  if (k !== 'DROP') void loadChart()
})
</script>

<template>
  <PageHeader :title="t('shifts.title')" :description="canSee ? t('shifts.intro') : undefined" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canSee" class="muted" data-testid="no-access">{{ t('shifts.noAccess', { permission: 'cashier.shift' }) }}</p>

  <template v-else>
    <Card v-if="canShift && loaded && !current" class="mb-4" data-testid="open-form">
      <form novalidate @submit.prevent="openShift">
        <CardHeader>
          <CardTitle>{{ t('shifts.openTitle') }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('shifts.openHint') }}</p>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-3">
          <FormField class="w-40" :label="t('shifts.drawer')" :error="fieldError('drawer')">
            <template #default="{ id }"><Input :id="id" v-model="openForm.drawer" name="drawer" placeholder="MAIN" maxlength="20" /></template>
          </FormField>
          <FormField class="w-48" :label="t('shifts.float')" :error="fieldError('opening_float')">
            <template #default="{ id }"><Input :id="id" v-model="openForm.opening_float" name="opening_float" inputmode="decimal" /></template>
          </FormField>
          <Button type="submit" :disabled="busy" data-testid="open-shift">{{ t('shifts.open') }}</Button>
        </CardContent>
      </form>
    </Card>

    <template v-if="current">
      <Card class="mb-4" data-testid="current-shift">
        <CardHeader>
          <CardTitle>{{ t('shifts.current', { number: current.number, drawer: current.drawer }) }}</CardTitle>
        </CardHeader>
        <CardContent>
          <dl v-if="current.cash" class="grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4" data-testid="cash">
            <div><dt class="text-muted-foreground">{{ t('shifts.float') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.opening_float) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.payments') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.payments) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.receipts') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.receipts) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.refunds') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.refunds) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.payIns') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.pay_ins) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.payOuts') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.pay_outs) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.drops') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.drops) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('shifts.voided') }}</dt><dd class="m-0 tabular-nums">{{ $money(current.cash.voided_after_close) }}</dd></div>
          </dl>
          <p v-if="current.cash" class="mb-0 mt-3 text-base" data-testid="expected">{{ t('shifts.expectedNow') }} <b class="tabular-nums">{{ $money(current.cash.expected) }}</b></p>
          <ul v-if="current.movements?.length" class="mt-3 list-none p-0 text-sm" data-testid="movements">
            <li v-for="m in current.movements" :key="m.id">{{ t(`shifts.kind_${m.kind}` as 'shifts.kind_DROP') }} {{ $money(m.amount) }} · {{ m.reason }}<span v-if="m.journal_number" class="text-muted-foreground"> · {{ m.journal_number }}</span></li>
          </ul>
        </CardContent>
      </Card>

      <Card class="mb-4" data-testid="move-form">
        <form novalidate @submit.prevent="moveCash">
          <CardHeader><CardTitle>{{ t('shifts.moveTitle') }}</CardTitle></CardHeader>
          <CardContent>
            <div class="grid gap-4 sm:grid-cols-4">
              <FormField :label="t('shifts.kind')" :error="fieldError('kind')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="move.kind" name="kind">
                    <option value="DROP">{{ t('shifts.kind_DROP') }}</option>
                    <option value="PAY_IN">{{ t('shifts.kind_PAY_IN') }}</option>
                    <option value="PAY_OUT">{{ t('shifts.kind_PAY_OUT') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="t('shifts.amount')" :error="fieldError('amount')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="move.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
              </FormField>
              <FormField v-if="move.kind !== 'DROP'" :label="t('shifts.account')" :hint="t('shifts.accountHint')" :error="fieldError('account_id')">
                <template #default="{ id, invalid }">
                  <Combobox v-if="postable.length" :id="id" v-model="move.account_id" name="account_id" :aria-invalid="invalid" :options="[{ value: 0, label: t('shifts.chooseAccount') }, ...postable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                  <Input v-else :id="id" :model-value="move.account_id || ''" name="account_id" inputmode="numeric" @update:model-value="(v) => (move.account_id = Number(v) || 0)" />
                </template>
              </FormField>
              <FormField :label="t('shifts.reason')" :error="fieldError('reason')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="move.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
              </FormField>
            </div>
            <div class="mt-4 flex justify-end">
              <Button type="submit" :disabled="busy || !move.amount.trim() || !move.reason.trim() || (move.kind !== 'DROP' && !move.account_id)" data-testid="record-move">{{ t('shifts.record') }}</Button>
            </div>
          </CardContent>
        </form>
      </Card>

      <Card v-if="!closing.open" class="mb-4">
        <CardContent class="pt-4"><Button type="button" variant="outline" data-testid="start-close" @click="startClose">{{ t('shifts.closeShift') }}</Button></CardContent>
      </Card>
      <Card v-else-if="!closing.asking" class="mb-4" data-testid="close-form">
        <form novalidate @submit.prevent="close()">
          <CardHeader>
            <CardTitle>{{ t('shifts.closeTitle') }}</CardTitle>
            <p class="m-0 text-sm text-muted-foreground">{{ t('shifts.closeHint') }}</p>
          </CardHeader>
          <CardContent>
            <div class="grid gap-4 sm:grid-cols-2">
              <FormField :label="t('shifts.countedCash')" :error="fieldError('counted_cash')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="closing.counted" name="counted_cash" inputmode="decimal" :aria-invalid="invalid" /></template>
              </FormField>
              <FormField :label="t('shifts.varianceReason')" :error="fieldError('reason')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="closing.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
              </FormField>
            </div>
            <p v-if="difference !== null" class="mt-3 text-sm" data-testid="difference">
              {{ difference === 0 ? t('shifts.exact') : difference < 0 ? t('shifts.short', { amount: $money(String(-difference)) }) : t('shifts.over', { amount: $money(String(difference)) }) }}
            </p>
            <div class="mt-4 flex justify-end gap-2">
              <Button type="button" variant="outline" @click="closing.open = false">{{ t('shifts.keepOpen') }}</Button>
              <Button type="submit" :disabled="busy || !closing.counted.trim()" data-testid="confirm-close">{{ t('shifts.close') }}</Button>
            </div>
          </CardContent>
        </form>
      </Card>
    </template>

    <Card v-if="can('cashier.settings') && settings" class="mb-4" data-testid="settings">
      <form novalidate @submit.prevent="saveSettings">
        <CardHeader><CardTitle>{{ t('shifts.settingsTitle') }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-3">
          <label class="flex items-center gap-2 text-sm"><input v-model="settingsForm.require_shift_for_cash" type="checkbox" name="require_shift_for_cash" class="size-4 accent-primary" /> {{ t('shifts.requireShift') }}</label>
          <label class="flex items-center gap-2 text-sm"><input v-model="settingsForm.block_night_audit" type="checkbox" name="block_night_audit" class="size-4 accent-primary" /> {{ t('shifts.blockAudit') }}</label>
          <FormField class="w-56" :label="t('shifts.maxVariance')" :hint="t('shifts.maxVarianceHint')" :error="fieldError('max_variance')">
            <template #default="{ id }"><Input :id="id" v-model="settingsForm.max_variance" name="max_variance" inputmode="decimal" /></template>
          </FormField>
          <div><Button type="submit" :disabled="busy">{{ t('shifts.saveSettings') }}</Button></div>
        </CardContent>
      </form>
    </Card>

    <Card data-testid="shift-list">
      <CardHeader><CardTitle>{{ canManage ? t('shifts.allShifts') : t('shifts.myShifts') }}</CardTitle></CardHeader>
      <CardContent>
        <EmptyState v-if="loaded && !shifts.length" :title="t('shifts.empty')" data-testid="empty" />
        <DataTable v-else :columns="columns" :rows="shifts" row-key="id" :row-test-id="(s) => `shift-${s.number}`" :caption="t('shifts.title')">
          <template #cell-over_short="{ row }">{{ row.over_short === null ? '-' : $money(row.over_short) }}</template>
          <template #cell-status="{ row }"><Badge :variant="row.status === 'OPEN' ? 'success' : 'outline'">{{ row.status === 'OPEN' ? t('shifts.statusOpen') : t('shifts.statusClosed') }}</Badge></template>
        </DataTable>
      </CardContent>
    </Card>
  </template>

  <ApprovalDialog v-if="closing.asking" :title="t('shifts.approveTitle')" :message="t('shifts.approveMessage')" :busy="busy" :error="dialogError" @approve="close" @cancel="closing.asking = false; dialogError = null" />
</template>
