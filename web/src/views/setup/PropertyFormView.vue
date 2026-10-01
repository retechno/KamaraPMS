<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CreatePropertyRequest, PatchPropertyRequest, PropertyWithDay } from '@/api/types'
import { usePropertyStore } from '@/stores/property'
import { addDays, formatBusinessDate, timeZones, todayIn } from '@/utils/dates'

const props = defineProps<{ id?: string }>()
const router = useRouter()
const store = usePropertyStore()

const isNew = computed(() => !props.id)
const zones = timeZones()

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
  status: 'ACTIVE' as 'ACTIVE' | 'INACTIVE',
  opening_business_date: '',
})

let original: PropertyWithDay | null = null
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
  for (const k of keys) {
    const now = k === 'currency_decimals' ? Number(form[k]) : form[k]
    const before = original[k] ?? ''
    if (now !== before) patch[k] = now
  }
  return patch as PatchPropertyRequest
}
</script>

<template>
  <h1 class="page-title">{{ isNew ? 'New property' : `Property ${form.code}` }}</h1>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
  </p>

  <form v-if="!loading" class="card" novalidate @submit.prevent="submit">
    <h2>Identity</h2>
    <div class="form-grid">
      <label class="field">
        <span>Code</span>
        <input v-model="form.code" name="code" :disabled="!isNew" :aria-invalid="!!fieldError('code')" required />
        <small class="hint">Short and permanent, e.g. BALI. A-Z, 0-9, - or _.</small>
        <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
      </label>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" required />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Address</span>
        <input v-model="form.address" name="address" />
      </label>
      <label class="field">
        <span>City</span>
        <input v-model="form.city" name="city" />
      </label>
      <label class="field">
        <span>Country</span>
        <input v-model="form.country_code" name="country_code" maxlength="2" placeholder="ID" :aria-invalid="!!fieldError('country_code')" />
        <small v-if="fieldError('country_code')" class="error-text">{{ fieldError('country_code') }}</small>
      </label>
      <label class="field">
        <span>Phone</span>
        <input v-model="form.phone" name="phone" maxlength="40" />
        <small class="hint">Printed on invoices, receipts and confirmations.</small>
      </label>
      <label class="field">
        <span>E-mail</span>
        <input v-model="form.email" name="email" type="email" maxlength="254" :aria-invalid="!!fieldError('email')" />
        <small v-if="fieldError('email')" class="error-text">{{ fieldError('email') }}</small>
      </label>
      <label class="field">
        <span>Tax registration number</span>
        <input v-model="form.tax_id" name="tax_id" maxlength="40" />
        <small class="hint">Printed on invoices (for example the NPWP).</small>
      </label>
      <label class="field">
        <span>Document footer</span>
        <input v-model="form.document_footer" name="document_footer" maxlength="500" placeholder="Thank you for staying with us" />
      </label>
      <label v-if="!isNew" class="field">
        <span>Status</span>
        <select v-model="form.status" name="status">
          <option value="ACTIVE">Active</option>
          <option value="INACTIVE">Inactive</option>
        </select>
      </label>
    </div>

    <h2 style="margin-top: 24px">Time and money</h2>
    <div class="form-grid">
      <label class="field">
        <span>Time zone</span>
        <input v-model="form.timezone" name="timezone" list="tz-list" :aria-invalid="!!fieldError('timezone')" />
        <datalist id="tz-list"><option v-for="z in zones" :key="z" :value="z" /></datalist>
        <small class="hint">The hotel's local time; business dates and night audit follow it.</small>
        <small v-if="fieldError('timezone')" class="error-text">{{ fieldError('timezone') }}</small>
      </label>
      <label class="field">
        <span>Currency</span>
        <input v-model="form.currency_code" name="currency_code" maxlength="3" :aria-invalid="!!fieldError('currency_code')" />
        <small v-if="fieldError('currency_code')" class="error-text">{{ fieldError('currency_code') }}</small>
      </label>
      <label class="field">
        <span>Currency decimals</span>
        <select v-model.number="form.currency_decimals" name="currency_decimals" :aria-invalid="!!fieldError('currency_decimals')">
          <option :value="0">0 (e.g. IDR, JPY)</option>
          <option :value="2">2 (e.g. USD, SGD)</option>
          <option :value="3">3 (e.g. KWD)</option>
        </select>
        <small class="hint">Currency and decimals lock once the first financial transaction is posted.</small>
        <small v-if="fieldError('currency_decimals')" class="error-text">{{ fieldError('currency_decimals') }}</small>
      </label>
    </div>

    <h2 style="margin-top: 24px">Policies</h2>
    <div class="form-grid">
      <label class="field">
        <span>Check-in time</span>
        <input v-model="form.check_in_time" type="time" name="check_in_time" :aria-invalid="!!fieldError('check_in_time')" />
        <small v-if="fieldError('check_in_time')" class="error-text">{{ fieldError('check_in_time') }}</small>
      </label>
      <label class="field">
        <span>Check-out time</span>
        <input v-model="form.check_out_time" type="time" name="check_out_time" :aria-invalid="!!fieldError('check_out_time')" />
        <small v-if="fieldError('check_out_time')" class="error-text">{{ fieldError('check_out_time') }}</small>
      </label>
      <label class="field">
        <span>Night audit earliest time</span>
        <input v-model="form.night_audit_earliest_time" type="time" name="night_audit_earliest_time" />
        <small class="hint">The day can be closed from this local time (or any time after midnight).</small>
      </label>
    </div>
    <div style="margin-top: 14px; display: grid; gap: 10px">
      <label class="check">
        <input v-model="form.require_room_inspection_for_checkin" type="checkbox" name="require_room_inspection_for_checkin" />
        <span>Require INSPECTED rooms for check-in (otherwise CLEAN is enough)</span>
      </label>
      <label class="check">
        <input v-model="form.night_audit_marks_occupied_dirty" type="checkbox" name="night_audit_marks_occupied_dirty" />
        <span>Night audit marks occupied rooms DIRTY (stay-over cleaning)</span>
      </label>
    </div>

    <template v-if="isNew">
      <h2 style="margin-top: 24px">Business day</h2>
      <div class="form-grid">
        <label class="field">
          <span>Opening business date</span>
          <input v-model="form.opening_business_date" type="date" name="opening_business_date" :aria-invalid="!!fieldError('opening_business_date')" />
          <small v-if="localToday" class="hint">
            Today at the property is {{ formatBusinessDate(localToday) }}; you may also open on
            {{ formatBusinessDate(addDays(localToday, -1)) }}. After this, only night audit moves the date.
          </small>
          <small v-if="fieldError('opening_business_date')" class="error-text">{{ fieldError('opening_business_date') }}</small>
        </label>
      </div>
    </template>

    <div class="form-actions">
      <button type="button" @click="router.push('/setup/properties')">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">{{ saving ? 'Saving…' : isNew ? 'Create property' : 'Save changes' }}</button>
    </div>
  </form>
</template>
