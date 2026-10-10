<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CreatePropertyRequest, PatchPropertyRequest, PropertyWithDay } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { addDays, formatBusinessDate, timeZones, todayIn } from '@/utils/dates'

const props = defineProps<{ id?: string }>()
const router = useRouter()
const store = usePropertyStore()

const isNew = computed(() => !props.id)
const zones = timeZones()

const REFUND_CHOICES = [{ value: 'CASH' }, { value: 'CARD' }, { value: 'BANK_TRANSFER' }, { value: 'OTHER' }]

const form = reactive({
  code: '',
  name: '',
  address: '',
  city: '',
  country_code: '',
  phone: '',
  email: '',
  tax_id: '',
  document_footer: '',
  timezone: 'Asia/Jakarta',
  currency_code: 'IDR',
  currency_decimals: 0,
  check_in_time: '14:00',
  check_out_time: '12:00',
  night_audit_earliest_time: '20:00',
  require_room_inspection_for_checkin: false,
  night_audit_marks_occupied_dirty: true,
  refund_methods: ['CASH'] as string[],
  status: 'ACTIVE' as 'ACTIVE' | 'INACTIVE',
  opening_business_date: '',
})

let original: PropertyWithDay | null = null
// The property has financial data: the currency and the decimals are read-only and say why.
const currencyLocked = ref(false)
const loading = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)

// The opening business date is the property's local today (or yesterday), not the browser's.
const localToday = computed(() => todayIn(form.timezone))
watch(
  localToday,
  (today) => {
    if (isNew.value && today) form.opening_business_date = today
  },
  { immediate: true },
)

const openingHint = computed(() =>
  localToday.value ? t('propertyForm.openingHint', { today: formatBusinessDate(localToday.value), yesterday: formatBusinessDate(addDays(localToday.value, -1)) }) : undefined,
)

function fieldError(field: string): string | undefined {
  return error.value?.fieldMessage(field)
}

onMounted(async () => {
  if (isNew.value) return
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}', { params: { path: { propertyId: Number(props.id) } } })
    if (data) {
      original = data
      currencyLocked.value = data.currency_locked
      Object.assign(form, {
        code: data.code,
        name: data.name,
        address: data.address ?? '',
        city: data.city ?? '',
        country_code: data.country_code ?? '',
        phone: data.phone ?? '',
        email: data.email ?? '',
        tax_id: data.tax_id ?? '',
        document_footer: data.document_footer ?? '',
        timezone: data.timezone,
        currency_code: data.currency_code,
        currency_decimals: data.currency_decimals,
        check_in_time: data.check_in_time,
        check_out_time: data.check_out_time,
        night_audit_earliest_time: data.night_audit_earliest_time,
        require_room_inspection_for_checkin: data.require_room_inspection_for_checkin,
        night_audit_marks_occupied_dirty: data.night_audit_marks_occupied_dirty,
        refund_methods: [...data.refund_methods],
        status: data.status,
      })
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
})

async function submit(): Promise<void> {
  saving.value = true
  error.value = null
  try {
    let saved: PropertyWithDay | undefined
    if (isNew.value) {
      const body: CreatePropertyRequest = {
        code: form.code,
        name: form.name,
        address: form.address || undefined,
        city: form.city || undefined,
        country_code: form.country_code || undefined,
        phone: form.phone || undefined,
        email: form.email || undefined,
        tax_id: form.tax_id || undefined,
        document_footer: form.document_footer || undefined,
        timezone: form.timezone,
        currency_code: form.currency_code,
        currency_decimals: Number(form.currency_decimals),
        check_in_time: form.check_in_time,
        check_out_time: form.check_out_time,
        night_audit_earliest_time: form.night_audit_earliest_time,
        require_room_inspection_for_checkin: form.require_room_inspection_for_checkin,
        night_audit_marks_occupied_dirty: form.night_audit_marks_occupied_dirty,
        refund_methods: form.refund_methods as CreatePropertyRequest['refund_methods'],
        opening_business_date: form.opening_business_date,
      }
      saved = (await api.POST('/api/v1/properties', { body })).data
    } else {
      saved = (await api.PATCH('/api/v1/properties/{propertyId}', {
        params: { path: { propertyId: Number(props.id) } },
        body: changedFields(),
      })).data
    }
    if (saved) {
      store.upsert(saved)
      if (isNew.value || store.currentId === null) await store.select(saved.id)
      await router.push('/setup/properties')
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

/** Only fields that differ from the loaded property are sent. */
function changedFields(): PatchPropertyRequest {
  const patch: Record<string, unknown> = {}
  if (!original) return patch
  const keys = [
    'name', 'address', 'city', 'country_code', 'phone', 'email', 'tax_id', 'document_footer', 'timezone', 'currency_code', 'currency_decimals', 'check_in_time',
    'check_out_time', 'night_audit_earliest_time', 'require_room_inspection_for_checkin',
    'night_audit_marks_occupied_dirty', 'status',
  ] as const
  if (form.refund_methods.length && [...form.refund_methods].sort().join() !== [...original.refund_methods].sort().join()) patch.refund_methods = form.refund_methods
  for (const k of keys) {
    const now = k === 'currency_decimals' ? Number(form[k]) : form[k]
    const before = original[k] ?? ''
    if (now !== before) patch[k] = now
  }
  return patch as PatchPropertyRequest
}
</script>

<template>
  <PageHeader :title="isNew ? t('propertyForm.new') : t('propertyForm.property', { code: form.code })" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
  </p>

  <form v-autofocus v-if="!loading" novalidate @submit.prevent="submit">
    <Card class="mb-4">
      <CardHeader><CardTitle>{{ t('propertyForm.identity') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('propertyForm.code')" :hint="t('propertyForm.codeHint')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="!isNew" :aria-invalid="invalid" required /></template>
          </FormField>
          <FormField :label="t('propertyForm.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" required /></template>
          </FormField>
          <FormField :label="t('propertyForm.address')">
            <template #default="{ id }"><Input :id="id" v-model="form.address" name="address" /></template>
          </FormField>
          <FormField :label="t('propertyForm.city')">
            <template #default="{ id }"><Input :id="id" v-model="form.city" name="city" /></template>
          </FormField>
          <FormField :label="t('propertyForm.country')" :error="fieldError('country_code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.country_code" name="country_code" maxlength="2" placeholder="ID" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('propertyForm.phone')" :hint="t('propertyForm.phoneHint')">
            <template #default="{ id }"><Input :id="id" v-model="form.phone" name="phone" maxlength="40" /></template>
          </FormField>
          <FormField :label="t('propertyForm.email')" :error="fieldError('email')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.email" name="email" type="email" maxlength="254" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('propertyForm.taxId')" :hint="t('propertyForm.taxIdHint')">
            <template #default="{ id }"><Input :id="id" v-model="form.tax_id" name="tax_id" maxlength="40" /></template>
          </FormField>
          <FormField :label="t('propertyForm.footer')">
            <template #default="{ id }"><Input :id="id" v-model="form.document_footer" name="document_footer" maxlength="500" :placeholder="t('propertyForm.footerPlaceholder')" /></template>
          </FormField>
          <FormField v-if="!isNew" :label="t('setup.status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.status" name="status">
                <option value="ACTIVE">{{ t('setup.active') }}</option>
                <option value="INACTIVE">{{ t('setup.inactive') }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </div>
      </CardContent>
    </Card>

    <Card class="mb-4">
      <CardHeader><CardTitle>{{ t('propertyForm.timeMoney') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormField :label="t('propertyForm.timezone')" :hint="t('propertyForm.timezoneHint')" :error="fieldError('timezone')">
            <template #default="{ id, invalid }">
              <Input :id="id" v-model="form.timezone" name="timezone" list="tz-list" :aria-invalid="invalid" />
              <datalist id="tz-list"><option v-for="z in zones" :key="z" :value="z" /></datalist>
            </template>
          </FormField>
          <FormField :label="t('propertyForm.currency')" :error="fieldError('currency_code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.currency_code" name="currency_code" maxlength="3" :readonly="currencyLocked" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('propertyForm.decimals')" :hint="t('propertyForm.decimalsHint')" :error="fieldError('currency_decimals')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.currency_decimals" name="currency_decimals" :disabled="currencyLocked" :aria-invalid="invalid">
                <option :value="0">{{ t('propertyForm.decimals0') }}</option>
                <option :value="2">{{ t('propertyForm.decimals2') }}</option>
                <option :value="3">{{ t('propertyForm.decimals3') }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </div>
        <p v-if="currencyLocked" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="currency-locked">{{ t('propertyForm.currencyLocked') }}</p>
      </CardContent>
    </Card>

    <Card class="mb-4">
      <CardHeader><CardTitle>{{ t('propertyForm.policies') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormField :label="t('propertyForm.checkIn')" :error="fieldError('check_in_time')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.check_in_time" type="time" name="check_in_time" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('propertyForm.checkOut')" :error="fieldError('check_out_time')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.check_out_time" type="time" name="check_out_time" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('propertyForm.auditEarliest')" :hint="t('propertyForm.auditEarliestHint')">
            <template #default="{ id }"><Input :id="id" v-model="form.night_audit_earliest_time" type="time" name="night_audit_earliest_time" /></template>
          </FormField>
        </div>
        <div class="mt-4 grid gap-2.5">
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.require_room_inspection_for_checkin" type="checkbox" name="require_room_inspection_for_checkin" class="size-4 accent-primary" />
            <span>{{ t('propertyForm.requireInspection') }}</span>
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.night_audit_marks_occupied_dirty" type="checkbox" name="night_audit_marks_occupied_dirty" class="size-4 accent-primary" />
            <span>{{ t('propertyForm.markDirty') }}</span>
          </label>
          <fieldset class="flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2" data-testid="refund-methods">
            <legend class="px-1 text-sm font-medium">{{ t('propertyForm.refundBy') }}</legend>
            <label v-for="m in REFUND_CHOICES" :key="m.value" class="flex items-center gap-1.5 text-sm">
              <input v-model="form.refund_methods" type="checkbox" name="refund_methods" :value="m.value" class="size-4 accent-primary" />
              <span>{{ t(`propertyForm.refund_${m.value}`) }}</span>
            </label>
            <small class="w-full text-xs text-muted-foreground">{{ t('propertyForm.refundHint') }}</small>
            <small v-if="fieldError('refund_methods')" role="alert" class="w-full text-xs text-destructive">{{ fieldError('refund_methods') }}</small>
          </fieldset>
        </div>
      </CardContent>
    </Card>

    <Card v-if="isNew" class="mb-4">
      <CardHeader><CardTitle>{{ t('propertyForm.businessDay') }}</CardTitle></CardHeader>
      <CardContent>
        <FormField class="max-w-sm" :label="t('propertyForm.openingDate')" :hint="openingHint" :error="fieldError('opening_business_date')">
          <template #default="{ id, invalid }"><Input :id="id" v-model="form.opening_business_date" type="date" name="opening_business_date" :aria-invalid="invalid" /></template>
        </FormField>
      </CardContent>
    </Card>

    <div class="flex justify-end gap-2">
      <Button type="button" variant="outline" @click="router.push('/setup/properties')">{{ t('common.cancel') }}</Button>
      <Button type="submit" :disabled="saving">{{ saving ? t('propertyForm.saving') : isNew ? t('propertyForm.create') : t('propertyForm.saveChanges') }}</Button>
    </div>
  </form>
</template>
