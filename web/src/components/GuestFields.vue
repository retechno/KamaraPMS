<script setup lang="ts">
import type { ApiError } from '@/api/problem'

import type { GuestForm } from './guestForm'

const form = defineModel<GuestForm>({ required: true })
defineProps<{ error: ApiError | null; disabled?: boolean }>()
</script>

<template>
  <div class="form-grid">
    <label v-for="f in [
      { key: 'first_name', label: 'First name' },
      { key: 'last_name', label: 'Last name' },
      { key: 'email', label: 'Email', type: 'email' },
      { key: 'phone', label: 'Phone', hint: 'e.g. +62 812-3456-7890' },
      { key: 'date_of_birth', label: 'Date of birth', type: 'date' },
      { key: 'nationality', label: 'Nationality', hint: 'ISO code, e.g. ID', max: 2 },
      { key: 'country_code', label: 'Country of residence', hint: 'ISO code, e.g. ID', max: 2 },
      { key: 'id_type', label: 'ID type', hint: 'e.g. PASSPORT, KTP' },
      { key: 'id_number', label: 'ID number' },
      { key: 'city', label: 'City' },
      { key: 'address', label: 'Address' },
    ] as const" :key="f.key" class="field">
      <span>{{ f.label }}</span>
      <input
        v-model="form[f.key]"
        :name="f.key"
        :type="'type' in f ? f.type : 'text'"
        :maxlength="'max' in f ? f.max : undefined"
        :disabled="disabled"
        :aria-invalid="!!error?.fieldMessage(f.key)"
      />
      <small v-if="'hint' in f" class="hint">{{ f.hint }}</small>
      <small v-if="error?.fieldMessage(f.key)" class="error-text">{{ error.fieldMessage(f.key) }}</small>
    </label>
    <label class="field">
      <span>Gender</span>
      <select v-model="form.gender" name="gender" :disabled="disabled">
        <option value="">Not recorded</option>
        <option value="MALE">Male</option>
        <option value="FEMALE">Female</option>
        <option value="OTHER">Other</option>
        <option value="UNDISCLOSED">Prefer not to say</option>
      </select>
    </label>
    <label class="field wide">
      <span>Notes</span>
      <input v-model="form.notes" name="notes" :disabled="disabled" :aria-invalid="!!error?.fieldMessage('notes')" />
    </label>
  </div>
</template>

<style scoped>
.wide {
  grid-column: 1 / -1;
}
</style>
