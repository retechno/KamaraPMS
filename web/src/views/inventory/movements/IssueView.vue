<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, InventoryIssue, InventoryItem, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'
import { PURPOSES, type Purpose, WASTE_REASONS, postedVariant } from '@/utils/movements'

/**
 * An issue of goods out of a warehouse (docs/architecture/35-inventory.md 5.4, mockup 25): to a department (use), waste, a staff meal, entertainment. Each purpose has its account (the category's issue account, the
 * waste account, the key of the staff meal or of the entertainment) and its fields: a waste has a reason, a staff meal and an entertainment have the covers, an entertainment has the host. It posts at once; a
 * mistake is corrected on the same day with a reversing document. The automatic amenities of a close are shown here as a document too and opened on their own page.
 */
interface Line {
  item_id: number
  qty: string
  expense_account_id: number
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const issueId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => issueId.value === null)

const issue = ref<InventoryIssue | null>(null)
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const departments = ref<Department[]>([])
const accounts = ref<{ id: number; code: string; name: string }[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive<{ purpose: Purpose; warehouse_id: number; department_id: number; reason_code: string; covers: string; beneficiary: string; note: string }>({
  purpose: 'USE', warehouse_id: 0, department_id: 0, reason_code: 'SPOILAGE', covers: '', beneficiary: '', note: '',
})
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())
const reverseOpen = ref(false)
const reverseReason = ref('')

const itemOf = (id: number): InventoryItem | undefined => items.value.find((i) => i.id === id)
const stockOf = (id: number): string => String(itemOf(id)?.stock.filter((s) => s.warehouse_id === form.warehouse_id).reduce((a, s) => a + Number(s.qty), 0) ?? 0)
const freeItems = (own: number): InventoryItem[] => items.value.filter((i) => i.id === own || !lines.value.some((l) => l.item_id === i.id))
/** The department of a document that needs one: every purpose but a waste (which takes the department of its warehouse when none is named). */
const needsDepartment = computed(() => form.purpose !== 'WASTE')
const needsCovers = computed(() => form.purpose === 'STAFF_MEAL' || form.purpose === 'ENTERTAINMENT')

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (issueId.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/issues/{id}', { params: { path: { propertyId, id: issueId.value } } })
      issue.value = data ?? null
    } else {
      issue.value = null
      const path = { params: { path: { propertyId } } }
      const [w, i] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
        api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
      ])
      warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
      items.value = i.data?.items ?? []
      departments.value = (await loadDepartments(propertyId)).filter((d) => d.is_active)
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query: { account_type: 'EXPENSE', postable: true, active: true } } })
        accounts.value = (((data as unknown as { data?: { id: number; code: string; name: string }[] } | undefined)?.data) ?? []).map((a) => ({ id: a.id, code: a.code, name: a.name }))
      } catch {
        accounts.value = [] // a person who may not read the chart uses the account of the category
      }
      const asked = String(route.query.purpose ?? '')
      form.purpose = (PURPOSES as readonly string[]).includes(asked) ? (asked as Purpose) : 'USE'
      form.warehouse_id = warehouses.value[0]?.id ?? 0
      form.department_id = 0
      lines.value = []
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, issueId, () => route.query.purpose], load, { immediate: true })

function addLine(): void {
  const it = freeItems(0)[0]
  if (it) lines.value.push({ item_id: it.id, qty: '', expense_account_id: 0 })
}
const tooMuch = (l: Line): boolean => isDecimal(l.qty, 6) && Number(l.qty) > Number(stockOf(l.item_id))
const complete = computed(
  () =>
    form.warehouse_id > 0 &&
    lines.value.length > 0 &&
    lines.value.every((l) => l.item_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0 && !tooMuch(l)) &&
    (!needsDepartment.value || form.department_id > 0) &&
    (!needsCovers.value || (/^\d+$/.test(form.covers) && Number(form.covers) > 0)) &&
    (form.purpose !== 'ENTERTAINMENT' || form.beneficiary.trim() !== ''),
)

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/issues', {
      params: { path: { propertyId } },
      body: {
        client_uuid: clientUuid.value,
        warehouse_id: form.warehouse_id,
        purpose: form.purpose,
        department_id: form.department_id || null,
        reason_code: form.purpose === 'WASTE' ? (form.reason_code as 'SPOILAGE') : null,
        covers: needsCovers.value ? Number(form.covers) : null,
        beneficiary: form.purpose === 'ENTERTAINMENT' ? form.beneficiary.trim() : null,
        note: form.note.trim() || null,
        lines: lines.value.map((l) => ({ item_id: l.item_id, qty: l.qty.trim(), expense_account_id: form.purpose === 'USE' && l.expense_account_id ? l.expense_account_id : null })),
      },
    })
    if (data) await router.replace(`/inventory/issues/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !issue.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/issues/{id}/reverse', { params: { path: { propertyId, id: issue.value.id } }, body: { reason: reverseReason.value.trim() } })
    if (data) {
      reverseOpen.value = false
      reverseReason.value = ''
      await router.push(`/inventory/issues/${data.id}`)
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t(`inventory.issue.newTitle.${form.purpose}`) : t('inventory.issue.viewTitle', { number: issue?.iss_number ?? '' })" :description="isNew ? t(`inventory.issue.hint.${form.purpose}`) : ''">
    <template v-if="issue" #marks>
      <Badge :variant="postedVariant(issue.status)" data-testid="issue-status">{{ t(`inventory.postedStatus.${issue.status}`) }}</Badge>
      <Badge variant="outline" data-testid="issue-purpose">{{ issue.origin === 'AMENITY' ? t('inventory.issues.amenity') : t(`inventory.issues.purpose.${issue.purpose}`) }}</Badge>
    </template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/issues" data-testid="back">{{ t('inventory.issue.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <form v-else-if="loaded && isNew" novalidate class="flex flex-col gap-4" data-testid="issue-form" @submit.prevent="post">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="issue-error" />
    <div class="flex flex-wrap gap-2" role="tablist" data-testid="purpose-tabs">
      <Button v-for="p in PURPOSES" :key="p" type="button" size="sm" role="tab" :aria-selected="form.purpose === p" :variant="form.purpose === p ? 'default' : 'outline'" :data-testid="`purpose-${p}`" @click="form.purpose = p">{{ t(`inventory.issues.purpose.${p}`) }}</Button>
    </div>
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <FormField :label="t('inventory.issue.warehouse')" :error="fieldError('warehouse_id')" required>
        <template #default="{ id, invalid }"><NativeSelect :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect></template>
      </FormField>
      <FormField :label="t('inventory.issue.department')" :error="fieldError('department_id')" :required="needsDepartment" :hint="form.purpose === 'WASTE' ? t('inventory.issue.wasteDepartmentHint') : ''">
        <template #default="{ id, invalid }">
          <NativeSelect :id="id" v-model.number="form.department_id" name="department_id" :aria-invalid="invalid">
            <option :value="0">{{ form.purpose === 'WASTE' ? t('inventory.issue.ofWarehouse') : '—' }}</option>
            <option v-for="d in departments" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField v-if="form.purpose === 'WASTE'" :label="t('inventory.issue.reason')" :error="fieldError('reason_code')" required>
        <template #default="{ id }"><NativeSelect :id="id" v-model="form.reason_code" name="reason_code"><option v-for="r in WASTE_REASONS" :key="r" :value="r">{{ t(`inventory.issue.reasons.${r}`) }}</option></NativeSelect></template>
      </FormField>
      <FormField v-if="needsCovers" :label="form.purpose === 'STAFF_MEAL' ? t('inventory.issue.meals') : t('inventory.issue.guests')" :error="fieldError('covers')" required>
        <template #default="{ id }"><Input :id="id" v-model="form.covers" name="covers" inputmode="numeric" maxlength="6" class="w-28" data-testid="covers" /></template>
      </FormField>
      <FormField v-if="form.purpose === 'ENTERTAINMENT'" :label="t('inventory.issue.beneficiary')" :error="fieldError('beneficiary')" required>
        <template #default="{ id }"><Input :id="id" v-model="form.beneficiary" name="beneficiary" maxlength="100" data-testid="beneficiary" /></template>
      </FormField>
    </div>
    <FormField :label="t('inventory.request.note')" :error="fieldError('note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>

    <div class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="issue-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.issue.stock') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.issue.used') }}</th>
            <th v-if="form.purpose === 'USE'" class="py-1 pr-2 font-medium">{{ t('inventory.issue.account') }}</th><th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
            <td class="py-2 pr-2">
              <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.request.item')" :aria-invalid="!!lineError(i, 'item_id')">
                <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
              </NativeSelect>
              <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
            </td>
            <td class="py-2 pr-2 text-right text-muted-foreground" :data-testid="`stock-${i}`">{{ formatQty(stockOf(l.item_id)) }} {{ itemOf(l.item_id)?.stock_unit_code }}</td>
            <td class="py-2 pr-2">
              <QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.issue.used')" :aria-invalid="tooMuch(l) || !!lineError(i, 'qty')" />
              <p v-if="tooMuch(l)" class="m-0 mt-1 text-xs text-destructive" :data-testid="`too-much-${i}`">{{ t('inventory.issue.tooMuch') }}</p>
              <p v-else-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
            </td>
            <td v-if="form.purpose === 'USE'" class="py-2 pr-2">
              <NativeSelect v-model.number="l.expense_account_id" :name="`account_${i}`" class="max-w-64" :aria-label="t('inventory.issue.account')" :aria-invalid="!!lineError(i, 'expense_account_id')">
                <option :value="0">{{ t('inventory.issue.categoryAccount') }}</option>
                <option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </NativeSelect>
              <p v-if="lineError(i, 'expense_account_id')" class="m-0 mt-1 text-xs text-destructive" :data-testid="`account-error-${i}`">{{ lineError(i, 'expense_account_id') }}</p>
            </td>
            <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="muted text-sm">{{ t(`inventory.issue.rule.${form.purpose}`) }}</p>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <Button type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.request.addLine') }}</Button>
      <div class="flex flex-wrap gap-2">
        <Button as-child variant="outline"><RouterLink to="/inventory/issues">{{ t('common.cancel') }}</RouterLink></Button>
        <Button type="submit" :disabled="busy || !complete" data-testid="issue-post">{{ t(`inventory.issue.post.${form.purpose}`) }}</Button>
      </div>
    </div>
  </form>

  <section v-else-if="loaded && issue" class="flex flex-col gap-4" data-testid="issue-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="issue-error" />
    <p v-if="issue.held_lines > 0" class="alert warning" data-testid="held-note">{{ t('inventory.issue.held', { n: issue.held_lines }) }}</p>
    <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm" data-testid="issue-facts">
      <dt class="text-muted-foreground">{{ t('inventory.issue.warehouse') }}</dt><dd class="m-0">{{ issue.warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.transfer.businessDate') }}</dt><dd class="m-0">{{ formatDate(issue.business_date) }}<template v-if="issue.source_date && issue.source_date !== issue.business_date"> · {{ t('inventory.issue.forNight', { date: formatDate(issue.source_date) }) }}</template></dd>
      <dt v-if="issue.department_name" class="text-muted-foreground">{{ t('inventory.issue.department') }}</dt><dd v-if="issue.department_name" class="m-0">{{ issue.department_name }}</dd>
      <dt v-if="issue.reason_code" class="text-muted-foreground">{{ t('inventory.issue.reason') }}</dt><dd v-if="issue.reason_code" class="m-0">{{ t(`inventory.issue.reasons.${issue.reason_code}`) }}</dd>
      <dt v-if="issue.covers" class="text-muted-foreground">{{ issue.purpose === 'STAFF_MEAL' ? t('inventory.issue.meals') : t('inventory.issue.guests') }}</dt>
      <dd v-if="issue.covers" class="m-0">{{ issue.covers }}<template v-if="issue.cost_per_cover"> · {{ t('inventory.issue.perCover', { cost: formatMoney(issue.cost_per_cover) }) }}</template></dd>
      <dt v-if="issue.beneficiary" class="text-muted-foreground">{{ t('inventory.issue.beneficiary') }}</dt><dd v-if="issue.beneficiary" class="m-0">{{ issue.beneficiary }}</dd>
      <dt v-if="issue.note" class="text-muted-foreground">{{ t('inventory.request.note') }}</dt><dd v-if="issue.note" class="m-0">{{ issue.note }}</dd>
      <dt v-if="issue.reverses_number" class="text-muted-foreground">{{ t('inventory.issue.reverses') }}</dt><dd v-if="issue.reverses_number" class="m-0">{{ issue.reverses_number }} · {{ issue.reverse_reason }}</dd>
      <dt v-if="issue.reversed_by_number" class="text-muted-foreground">{{ t('inventory.issue.reversedBy') }}</dt><dd v-if="issue.reversed_by_number" class="m-0">{{ issue.reversed_by_number }}</dd>
    </dl>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[40rem] text-sm" data-testid="issue-lines-read">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.issue.used') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.issue.cost') }}</th>
            <th class="py-1 font-medium">{{ t('inventory.issue.account') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in issue.lines" :key="l.line_no" class="border-t border-border" :data-testid="`read-line-${l.line_no}`">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty_stock) }} {{ l.stock_unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ l.value !== null ? formatMoney(l.value) : t('inventory.issue.held1') }}</td>
            <td class="py-2">{{ l.expense_account_code ? `${l.expense_account_code} · ${l.expense_account_name}` : '—' }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td colspan="2" class="py-2 pr-2 text-right font-medium">{{ t('inventory.issue.total') }}</td><td class="py-2 pr-2 text-right font-semibold" data-testid="issue-total">{{ formatMoney(issue.total_value) }}</td><td /></tr></tfoot>
      </table>
    </div>
    <p v-if="issue.origin === 'AMENITY'" class="text-sm"><RouterLink :to="`/inventory/amenities/runs/${issue.source_date ?? issue.business_date}`" class="text-primary hover:underline" data-testid="amenity-link">{{ t('inventory.issue.openAmenity') }}</RouterLink></p>
    <p class="muted text-sm">{{ t('inventory.issue.correction') }}</p>
    <div v-if="issue.reversible && issue.origin === 'MANUAL' && can('inventory.move')" class="flex justify-end">
      <Button type="button" variant="outline" data-testid="issue-reverse" @click="reverseOpen = true">{{ t('inventory.issue.reverse') }}</Button>
    </div>
  </section>

  <Dialog :open="reverseOpen" @update:open="(v: boolean) => (reverseOpen = v)">
    <DialogContent class="max-w-md p-5" data-testid="reverse-dialog">
      <DialogTitle>{{ t('inventory.issue.reverseTitle', { number: issue?.iss_number ?? '' }) }}</DialogTitle>
      <DialogDescription class="mt-2">{{ t('inventory.issue.reverseHint') }}</DialogDescription>
      <ErrorNotice v-if="formError" :error="formError" inline class="mt-2" />
      <FormField class="mt-3" :label="t('inventory.receipt.reverseReasonLabel')" required><template #default="{ id }"><Input :id="id" v-model="reverseReason" name="reason" maxlength="300" data-testid="reverse-reason" /></template></FormField>
      <div class="mt-5 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="reverseOpen = false">{{ t('common.cancel') }}</Button>
        <Button type="button" variant="destructive" :disabled="busy || !reverseReason.trim()" data-testid="reverse-confirm" @click="reverse">{{ t('inventory.issue.reverse') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
