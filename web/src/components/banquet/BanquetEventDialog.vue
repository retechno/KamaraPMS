<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { fetchAll } from '@/api/paging'
import type { BanquetEvent, BanquetEventType } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'

/**
 * Opens an inquiry, or changes the header of an event (docs/architecture/36-banquet.md 6.3): its name, kind, booker (a guest or a company) and contact. The sessions are added on the event.
 * The booker who is a guest is found by name; a company is chosen from the list.
 */
const props = defineProps<{ open: boolean; propertyId: number; event?: BanquetEvent | null; types: BanquetEventType[] }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [event: BanquetEvent] }>()

const form = reactive({ name: '', event_type_id: 0, booker: 'COMPANY' as 'GUEST' | 'COMPANY', company_id: 0, guest_id: 0, contact_name: '', contact_phone: '', contact_email: '', notes: '' })
const companies = ref<{ id: number; name: string }[]>([])
const guestQuery = ref('')
const guests = ref<{ id: number; label: string }[]>([])
const guestName = ref('')
const error = ref<ApiError | null>(null)
const busy = ref(false)

const editing = computed(() => !!props.event)

watch(() => props.open, async (open) => {
  if (!open) return
  error.value = null
  const e = props.event
  Object.assign(form, e
    ? { name: e.name, event_type_id: e.event_type_id ?? 0, booker: e.booker_guest_id ? 'GUEST' : 'COMPANY', company_id: e.booker_company_id ?? 0, guest_id: e.booker_guest_id ?? 0, contact_name: e.contact_name ?? '', contact_phone: e.contact_phone ?? '', contact_email: e.contact_email ?? '', notes: e.notes ?? '' }
    : { name: '', event_type_id: 0, booker: 'COMPANY', company_id: 0, guest_id: 0, contact_name: '', contact_phone: '', contact_email: '', notes: '' })
  guestName.value = e?.booker_guest_id ? e.booker_name : ''
  guests.value = []
  guestQuery.value = ''
  if (!companies.value.length) {
    try {
      const propertyId = props.propertyId
      companies.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    } catch {
      companies.value = []
    }
  }
}, { immediate: true })

async function findGuests(): Promise<void> {
  if (!guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: props.propertyId, limit: 10 } } })
    guests.value = (data?.data ?? []).map((g) => ({ id: g.id, label: [g.first_name, g.last_name].filter(Boolean).join(' ') }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  const body = {
    name: form.name.trim(),
    event_type_id: form.event_type_id || null,
    booker_guest_id: form.booker === 'GUEST' ? form.guest_id || null : null,
    booker_company_id: form.booker === 'COMPANY' ? form.company_id || null : null,
    contact_name: form.contact_name.trim() || null,
    contact_phone: form.contact_phone.trim() || null,
    contact_email: form.contact_email.trim() || null,
    notes: form.notes.trim() || null,
    ...(props.event ? { version: props.event.version, option_until: props.event.option_until ?? null } : {}),
  }
  const propertyId = props.propertyId
  try {
    const res = props.event
      ? await api.PATCH('/api/v1/properties/{propertyId}/banquet/events/{id}', { params: { path: { propertyId, id: props.event.id } }, body })
      : await api.POST('/api/v1/properties/{propertyId}/banquet/events', { params: { path: { propertyId } }, body })
    if (res.data) emit('saved', res.data)
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="banquet-event-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ editing ? t('banquet.event.edit') : t('banquet.event.new') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.event.newHint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField :label="t('banquet.event.name')" required :error="error?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="150" /></template>
        </FormField>
        <FormField :label="t('banquet.event.type')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.event_type_id" name="event_type_id">
              <option :value="0">{{ t('banquet.event.noType') }}</option>
              <option v-for="x in types.filter((y) => y.is_active || y.id === event?.event_type_id)" :key="x.id" :value="x.id">{{ x.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('banquet.event.bookerKind')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.booker" name="booker">
              <option value="COMPANY">{{ t('banquet.event.bookerCompany') }}</option>
              <option value="GUEST">{{ t('banquet.event.bookerGuest') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField v-if="form.booker === 'COMPANY'" :label="t('banquet.event.company')" required :error="error?.fieldMessage('booker_company_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.company_id" name="booker_company_id">
              <option :value="0">{{ t('banquet.event.choose') }}</option>
              <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <div v-else class="flex flex-col gap-2">
          <FormField :label="t('banquet.event.guestSearch')" required :error="error?.fieldMessage('booker_guest_id')">
            <template #default="{ id }">
              <div class="flex gap-2">
                <Input :id="id" v-model="guestQuery" name="guest_query" @keydown.enter.prevent="findGuests" />
                <Button type="button" variant="outline" data-testid="guest-find" @click="findGuests">{{ t('banquet.event.find') }}</Button>
              </div>
            </template>
          </FormField>
          <p v-if="guestName && !guests.length" class="m-0 text-sm" data-testid="guest-chosen">{{ guestName }}</p>
          <ul v-if="guests.length" class="m-0 flex list-none flex-col gap-1 p-0">
            <li v-for="g in guests" :key="g.id">
              <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.guest_id" type="radio" name="guest_id" :value="g.id" class="size-4 accent-primary" />{{ g.label }}</label>
            </li>
          </ul>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.event.contactName')"><template #default="{ id }"><Input :id="id" v-model="form.contact_name" name="contact_name" maxlength="100" /></template></FormField>
          <FormField :label="t('banquet.event.contactPhone')"><template #default="{ id }"><Input :id="id" v-model="form.contact_phone" name="contact_phone" maxlength="40" /></template></FormField>
        </div>
        <FormField :label="t('banquet.event.contactEmail')" :error="error?.fieldMessage('contact_email')"><template #default="{ id }"><Input :id="id" v-model="form.contact_email" name="contact_email" type="email" maxlength="150" /></template></FormField>
        <FormField :label="t('banquet.event.notes')"><template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template></FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="banquet-event-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
