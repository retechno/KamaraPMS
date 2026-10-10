<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BillingInstruction, ChargeCode, Company } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

// Who pays what on one room of a reservation: the company that is billed for the room, for one charge code, or for everything else. The rest goes to the guest folio.
const props = defineProps<{ reservationId: number; lineId: number; editable: boolean }>()
const property = usePropertyStore()

type Scope = 'ALL' | 'ROOM' | 'CHARGE_CODE'
interface Row { scope: Scope; chargeCodeId: number; companyId: number }

const rules = ref<BillingInstruction[]>([])
const rows = ref<Row[]>([])
const editing = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const companies = ref<Company[]>([])
const codes = ref<ChargeCode[]>([])

const pid = computed(() => property.currentId)
const path = () => ({ propertyId: pid.value as number, id: props.reservationId, lineId: props.lineId })

async function load(): Promise<void> {
  if (pid.value === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/billing-instructions', { params: { path: path() } })
    rules.value = data?.instructions ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function startEdit(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    if (!companies.value.length) companies.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    if (!codes.value.length) codes.value = (await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))).filter((c) => c.is_active && c.charge_type !== 'ROOM')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return
  }
  rows.value = rules.value.map((r) => ({ scope: r.scope, chargeCodeId: r.charge_code_id ?? 0, companyId: r.company_id }))
  if (!rows.value.length) addRow()
  editing.value = true
}

function addRow(): void {
  rows.value.push({ scope: rows.value.some((r) => r.scope === 'ROOM') ? 'ALL' : 'ROOM', chargeCodeId: 0, companyId: companies.value[0]?.id ?? 0 })
}

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/billing-instructions', {
      params: { path: path() },
      body: { instructions: rows.value.filter((r) => r.companyId > 0).map((r) => ({ scope: r.scope, company_id: r.companyId, charge_code_id: r.scope === 'CHARGE_CODE' ? r.chargeCodeId : null })) },
    })
    rules.value = data?.instructions ?? []
    editing.value = false
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const scopeText = (r: BillingInstruction): string => (r.scope === 'CHARGE_CODE' ? `${t('billingInstructions.scope_CHARGE_CODE')} ${r.charge_code ?? ''}`.trim() : t(`billingInstructions.scope_${r.scope}`))

watch(() => [pid.value, props.reservationId, props.lineId], () => void load(), { immediate: true })
</script>

<template>
  <div class="mt-3 rounded-lg border border-border p-3 text-sm" :data-testid="`instructions-${lineId}`">
    <div class="flex flex-wrap items-center gap-2">
      <strong>{{ t('billingInstructions.title') }}</strong>
      <span v-if="!rules.length && !editing" class="text-muted-foreground" :data-testid="`instructions-none-${lineId}`">{{ t('billingInstructions.none') }}</span>
      <Button v-if="editable && !editing" variant="outline" size="sm" :data-testid="`edit-instructions-${lineId}`" @click="startEdit">{{ t('billingInstructions.edit') }}</Button>
    </div>
    <ul v-if="rules.length && !editing" class="m-0 mt-1 list-none p-0">
      <li v-for="(r, i) in rules" :key="i" :data-testid="`instruction-${lineId}-${i}`">{{ t('billingInstructions.pays', { scope: scopeText(r), company: r.company_name }) }}</li>
    </ul>
    <ErrorNotice v-if="error" :error="error" inline class="mt-2" :data-testid="`instructions-error-${lineId}`" />

    <form v-if="editing" class="mt-2 flex flex-col gap-2" novalidate :data-testid="`instructions-form-${lineId}`" @submit.prevent="save">
      <div v-for="(row, i) in rows" :key="i" class="flex flex-wrap items-end gap-2">
        <FormField class="w-44" :label="t('billingInstructions.scope')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="row.scope" :name="`scope_${i}`">
              <option v-for="s in ['ROOM', 'CHARGE_CODE', 'ALL']" :key="s" :value="s">{{ t(`billingInstructions.scope_${s}` as never) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField v-if="row.scope === 'CHARGE_CODE'" class="w-44" :label="t('billingInstructions.chargeCode')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="row.chargeCodeId" :name="`charge_code_${i}`">
              <option :value="0" />
              <option v-for="c in codes" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField class="w-52" :label="t('billingInstructions.company')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="row.companyId" :name="`company_${i}`">
              <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <Button type="button" variant="ghost" size="sm" :data-testid="`remove-rule-${i}`" @click="rows.splice(i, 1)">{{ t('billingInstructions.remove') }}</Button>
      </div>
      <p class="m-0 text-xs text-muted-foreground">{{ t('billingInstructions.hint') }}</p>
      <div class="flex gap-2">
        <Button type="button" variant="outline" size="sm" data-testid="add-rule" @click="addRow">{{ t('billingInstructions.add') }}</Button>
        <Button type="submit" size="sm" :disabled="busy" data-testid="save-instructions">{{ t('billingInstructions.save') }}</Button>
        <Button type="button" variant="ghost" size="sm" @click="editing = false">{{ t('common.close') }}</Button>
      </div>
    </form>
  </div>
</template>
