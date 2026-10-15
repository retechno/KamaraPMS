<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, PosOutlet, PosPriceList, PosPrinter, PosRuleProfile } from '@/api/types'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The outlets of the property (docs/architecture/34-pos.md): what each sells, how an order reaches the kitchen, its tax and service profile, price list, cash drawer, receipt printer and how
 * a complimentary is recorded. The code is the start of its bill numbers and never changes.
 */
const property = usePropertyStore()
const outlets = ref<PosOutlet[]>([])
const departments = ref<Department[]>([])
const profiles = ref<PosRuleProfile[]>([])
const lists = ref<PosPriceList[]>([])
const printers = ref<PosPrinter[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')

const TYPES = ['RESTAURANT', 'BAR', 'CAFE', 'ROOM_SERVICE', 'SPA', 'LAUNDRY', 'BUSINESS_CENTER', 'MINIBAR', 'OTHER'] as const
const MODES = ['PRINT', 'KDS', 'DIRECT'] as const

interface Form {
  id: number | null
  version: number
  code: string
  name: string
  outlet_type: string
  order_mode: string
  department_id: number
  rule_profile_id: number
  price_list_id: number
  drawer: string
  receipt_printer_id: number
  uses_tables: boolean
  guest_count_required: boolean
  rounding_unit: number
  allow_room_charge: boolean
  allow_company_charge: boolean
  allow_comp: boolean
  comp_treatment: string
  max_discount_pct_no_approval: string
  block_night_audit_with_open_bills: boolean
  is_active: boolean
  sort_order: number
}

const blank = (): Form => ({
  id: null, version: 0, code: '', name: '', outlet_type: 'RESTAURANT', order_mode: 'PRINT', department_id: 0, rule_profile_id: 0, price_list_id: 0, drawer: '', receipt_printer_id: 0,
  uses_tables: true, guest_count_required: false, rounding_unit: 0, allow_room_charge: true, allow_company_charge: false, allow_comp: false, comp_treatment: 'PAYMENT_COMP',
  max_discount_pct_no_approval: '0', block_night_audit_with_open_bills: false, is_active: true, sort_order: 0,
})
const form = reactive<Form>(blank())
const dialog = ref(false)
const saving = ref(false)
const formError = ref<ApiError | null>(null)
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const isNew = computed(() => form.id === null)

const pid = computed(() => property.currentId)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [o, p, l, pr] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/outlets', path),
      api.GET('/api/v1/properties/{propertyId}/pos/rule-profiles', path),
      api.GET('/api/v1/properties/{propertyId}/pos/price-lists', path),
      api.GET('/api/v1/properties/{propertyId}/pos/printers', path),
    ])
    outlets.value = o.data?.data ?? []
    profiles.value = p.data?.data ?? []
    lists.value = l.data?.data ?? []
    printers.value = pr.data?.data ?? []
    departments.value = await loadDepartments(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function openNew(): void {
  Object.assign(form, blank(), {
    department_id: departments.value.find((d) => d.level === 2 && d.is_active)?.id ?? departments.value.find((d) => d.is_active)?.id ?? 0,
    rule_profile_id: profiles.value.find((p) => p.is_active)?.id ?? 0,
    price_list_id: lists.value.find((l) => l.is_active)?.id ?? 0,
  })
  formError.value = null
  dialog.value = true
}

function openEdit(o: PosOutlet): void {
  Object.assign(form, {
    id: o.id, version: o.version, code: o.code, name: o.name, outlet_type: o.outlet_type, order_mode: o.order_mode, department_id: o.department_id, rule_profile_id: o.rule_profile_id,
    price_list_id: o.price_list_id, drawer: o.drawer ?? '', receipt_printer_id: o.receipt_printer_id ?? 0, uses_tables: o.uses_tables, guest_count_required: o.guest_count_required,
    rounding_unit: o.rounding_unit, allow_room_charge: o.allow_room_charge, allow_company_charge: o.allow_company_charge, allow_comp: o.allow_comp, comp_treatment: o.comp_treatment,
    max_discount_pct_no_approval: o.max_discount_pct_no_approval, block_night_audit_with_open_bills: o.block_night_audit_with_open_bills, is_active: o.is_active, sort_order: o.sort_order,
  })
  formError.value = null
  dialog.value = true
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  formError.value = null
  const body = {
    code: form.code.trim().toUpperCase(), name: form.name.trim(), outlet_type: form.outlet_type as (typeof TYPES)[number], order_mode: form.order_mode as 'PRINT' | 'KDS' | 'DIRECT', department_id: form.department_id,
    rule_profile_id: form.rule_profile_id, price_list_id: form.price_list_id, drawer: form.drawer.trim() || null, receipt_printer_id: form.receipt_printer_id || null,
    uses_tables: form.uses_tables, guest_count_required: form.guest_count_required, rounding_unit: form.rounding_unit as 0 | 100 | 500 | 1000, allow_room_charge: form.allow_room_charge,
    allow_company_charge: form.allow_company_charge, allow_comp: form.allow_comp, comp_treatment: form.comp_treatment as 'PAYMENT_COMP' | 'DISCOUNT_100',
    max_discount_pct_no_approval: form.max_discount_pct_no_approval.trim() || '0', block_night_audit_with_open_bills: form.block_night_audit_with_open_bills, is_active: form.is_active,
    sort_order: form.sort_order, version: isNew.value ? undefined : form.version,
  }
  try {
    if (isNew.value) {
      await api.POST('/api/v1/properties/{propertyId}/pos/outlets', { params: { path: { propertyId } }, body })
    } else {
      await api.PUT('/api/v1/properties/{propertyId}/pos/outlets/{id}', { params: { path: { propertyId, id: form.id! } }, body })
    }
    dialog.value = false
    notice.value = t('pos.outlets.saved')
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

const profileName = (id: number) => profiles.value.find((p) => p.id === id)?.name ?? ''
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('pos.outlets.title')">
    <template #actions>
      <Button data-testid="outlet-new" @click="openNew">{{ t('pos.outlets.new') }}</Button>
    </template>
  </PageHeader>
  <p class="muted">{{ t('pos.outlets.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else-if="loaded">
    <p v-if="outlets.length === 0" class="muted" data-testid="outlets-empty">{{ t('pos.outlets.empty') }}</p>
    <ul v-else class="m-0 mt-3 grid list-none gap-3 p-0 sm:grid-cols-2 xl:grid-cols-3" data-testid="outlets">
      <li v-for="o in outlets" :key="o.id" class="rounded-xl border border-border bg-card p-4" :data-testid="`outlet-${o.code}`">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <strong class="block truncate">{{ o.name }}</strong>
            <span class="text-xs text-muted-foreground">{{ o.code }} · {{ t(`pos.outletType.${o.outlet_type}`) }}</span>
          </div>
          <Badge :variant="o.is_active ? 'success' : 'outline'">{{ o.is_active ? t('common.active') : t('common.inactive') }}</Badge>
        </div>
        <dl class="mb-3 mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
          <dt class="text-muted-foreground">{{ t('pos.outlets.orderMode') }}</dt><dd class="m-0">{{ t(`pos.orderMode.${o.order_mode}`) }}</dd>
          <dt class="text-muted-foreground">{{ t('pos.outlets.department') }}</dt><dd class="m-0">{{ o.department_name }}</dd>
          <dt class="text-muted-foreground">{{ t('pos.outlets.profile') }}</dt><dd class="m-0">{{ profileName(o.rule_profile_id) || o.rule_profile_name }}</dd>
          <dt class="text-muted-foreground">{{ t('pos.outlets.priceList') }}</dt><dd class="m-0">{{ o.price_list_name }}</dd>
          <dt class="text-muted-foreground">{{ t('pos.outlets.drawer') }}</dt><dd class="m-0">{{ o.drawer ?? '—' }}</dd>
          <dt class="text-muted-foreground">{{ t('pos.outlets.compTreatment') }}</dt><dd class="m-0">{{ t(`pos.compTreatment.${o.comp_treatment}`) }}</dd>
        </dl>
        <Button variant="outline" size="sm" :data-testid="`outlet-edit-${o.code}`" @click="openEdit(o)">{{ t('common.edit') }}</Button>
      </li>
    </ul>
  </template>

  <Dialog :open="dialog" @update:open="(v) => (dialog = v)">
    <DialogContent class="max-h-[90vh] max-w-2xl overflow-y-auto p-5" data-testid="outlet-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-4" @submit.prevent="save">
        <div>
          <DialogTitle>{{ isNew ? t('pos.outlets.new') : t('pos.outlets.edit', { name: form.name }) }}</DialogTitle>
          <DialogDescription class="mt-1">{{ t('pos.outlets.formHint') }}</DialogDescription>
        </div>
        <ErrorNotice v-if="formError" :error="formError" inline data-testid="outlet-error" />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('pos.outlets.code')" :hint="t('pos.outlets.codeHint')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="6" :disabled="!isNew" :aria-invalid="invalid" class="uppercase" /></template>
          </FormField>
          <FormField :label="t('pos.outlets.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="100" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('pos.outlets.type')" :error="fieldError('outlet_type')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.outlet_type" name="outlet_type">
                <option v-for="x in TYPES" :key="x" :value="x">{{ t(`pos.outletType.${x}`) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.orderMode')" :hint="t(`pos.orderModeHint.${form.order_mode}`)" :error="fieldError('order_mode')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.order_mode" name="order_mode">
                <option v-for="x in MODES" :key="x" :value="x">{{ t(`pos.orderMode.${x}`) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.department')" :error="fieldError('department_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.department_id" name="department_id" :aria-invalid="invalid">
                <option :value="0" disabled>{{ t('common.choose') }}</option>
                <option v-for="d in departments.filter((x) => x.is_active)" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.profile')" :error="fieldError('rule_profile_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.rule_profile_id" name="rule_profile_id" :aria-invalid="invalid">
                <option :value="0" disabled>{{ t('common.choose') }}</option>
                <option v-for="p in profiles.filter((x) => x.is_active)" :key="p.id" :value="p.id">{{ p.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.priceList')" :error="fieldError('price_list_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.price_list_id" name="price_list_id" :aria-invalid="invalid">
                <option :value="0" disabled>{{ t('common.choose') }}</option>
                <option v-for="l in lists.filter((x) => x.is_active)" :key="l.id" :value="l.id">{{ l.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.receiptPrinter')" :error="fieldError('receipt_printer_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.receipt_printer_id" name="receipt_printer_id">
                <option :value="0">{{ t('pos.outlets.noPrinter') }}</option>
                <option v-for="p in printers.filter((x) => x.is_active && x.kind === 'RECEIPT')" :key="p.id" :value="p.id">{{ p.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.outlets.drawer')" :hint="t('pos.outlets.drawerHint')" :error="fieldError('drawer')">
            <template #default="{ id }"><Input :id="id" v-model="form.drawer" name="drawer" maxlength="20" /></template>
          </FormField>
          <FormField :label="t('pos.outlets.rounding')" :error="fieldError('rounding_unit')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.rounding_unit" name="rounding_unit">
                <option v-for="u in [0, 100, 500, 1000]" :key="u" :value="u">{{ u === 0 ? t('pos.outlets.noRounding') : t('pos.outlets.roundTo', { n: u }) }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </div>

        <fieldset class="m-0 rounded-lg border border-border p-3" data-testid="comp-treatment">
          <legend class="px-1 text-sm font-semibold">{{ t('pos.outlets.compTreatment') }}</legend>
          <label v-for="m in ['PAYMENT_COMP', 'DISCOUNT_100']" :key="m" class="flex cursor-pointer items-start gap-2 py-1.5 text-sm">
            <input v-model="form.comp_treatment" type="radio" name="comp_treatment" :value="m" class="mt-1 accent-primary" :data-testid="`comp-${m}`" />
            <span><strong class="block">{{ t(`pos.compTreatment.${m}`) }}</strong><span class="text-xs text-muted-foreground">{{ t(`pos.compTreatmentHint.${m}`) }}</span></span>
          </label>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('pos.outlets.compTaxNote') }}</p>
        </fieldset>

        <div class="grid gap-x-4 gap-y-1 sm:grid-cols-2">
          <label v-for="f in ([
            ['uses_tables', 'usesTables'], ['guest_count_required', 'guestCountRequired'], ['allow_room_charge', 'allowRoomCharge'], ['allow_company_charge', 'allowCompanyCharge'],
            ['allow_comp', 'allowComp'], ['block_night_audit_with_open_bills', 'blockAudit'], ['is_active', 'active'],
          ] as const)" :key="f[0]" class="flex min-h-11 cursor-pointer items-center gap-2 text-sm">
            <input v-model="form[f[0]]" type="checkbox" :name="f[0]" class="size-4 accent-primary" />{{ t(`pos.outlets.${f[1]}`) }}
          </label>
        </div>
        <FormField :label="t('pos.outlets.maxDiscount')" :hint="t('pos.outlets.maxDiscountHint')" :error="fieldError('max_discount_pct_no_approval')">
          <template #default="{ id }"><Input :id="id" v-model="form.max_discount_pct_no_approval" name="max_discount_pct_no_approval" inputmode="decimal" class="max-w-32" /></template>
        </FormField>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="dialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving || !form.name.trim() || !form.code.trim() || !form.department_id || !form.rule_profile_id || !form.price_list_id" data-testid="outlet-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
