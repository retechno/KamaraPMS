<script setup lang="ts">
import type { ApiError } from '@/api/problem'
import FormField from '@/components/app/FormField.vue'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'

import type { GuestForm } from './guestForm'

const form = defineModel<GuestForm>({ required: true })
defineProps<{ error: ApiError | null; disabled?: boolean }>()

const fields = [
  { key: 'first_name' },
  { key: 'last_name' },
  { key: 'email', type: 'email' },
  { key: 'phone', hint: 'guestFields.phoneHint' },
  { key: 'date_of_birth', type: 'date' },
  { key: 'nationality', hint: 'guestFields.isoHint', max: 2 },
  { key: 'country_code', hint: 'guestFields.isoHint', max: 2 },
  { key: 'id_type', hint: 'guestFields.idTypeHint' },
  { key: 'id_number' },
  { key: 'city' },
  { key: 'address' },
] as const
</script>

<template>
  <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
    <FormField v-for="f in fields" :key="f.key" :label="t(`guestFields.${f.key}`)" :hint="'hint' in f ? t(f.hint) : undefined" :error="error?.fieldMessage(f.key)">
      <template #default="{ id, invalid }">
        <Input
          :id="id"
          v-model="form[f.key]"
          :name="f.key"
          :type="'type' in f ? f.type : 'text'"
          :maxlength="'max' in f ? f.max : undefined"
          :disabled="disabled"
          :aria-invalid="invalid"
        />
      </template>
    </FormField>
    <FormField :label="t('guestFields.gender')">
      <template #default="{ id }">
        <NativeSelect :id="id" v-model="form.gender" name="gender" :disabled="disabled">
          <option value="">{{ t('guestFields.notRecorded') }}</option>
          <option value="MALE">{{ t('guestFields.male') }}</option>
          <option value="FEMALE">{{ t('guestFields.female') }}</option>
          <option value="OTHER">{{ t('guestFields.other') }}</option>
          <option value="UNDISCLOSED">{{ t('guestFields.undisclosed') }}</option>
        </NativeSelect>
      </template>
    </FormField>
    <FormField class="sm:col-span-2 lg:col-span-3" :label="t('guestFields.notes')" :error="error?.fieldMessage('notes')">
      <template #default="{ id, invalid }"><Input :id="id" v-model="form.notes" name="notes" :disabled="disabled" :aria-invalid="invalid" /></template>
    </FormField>
  </div>
</template>
