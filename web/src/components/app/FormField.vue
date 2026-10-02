<script setup lang="ts">
import { computed, useId } from 'vue'
import { t } from '@/i18n'

/**
 * A label, a control, a hint and an error, tied together for assistive technology. The control comes from the slot and
 * receives what it must carry: `<template #default="{ id, describedBy, invalid }"><Input :id="id" :aria-describedby="describedBy"
 * :aria-invalid="invalid" /></template>`.
 */
const props = defineProps<{ label: string; hint?: string; error?: string; required?: boolean }>()

const id = useId()
const hintId = `${id}-hint`
const errorId = `${id}-error`
const invalid = computed(() => !!props.error)
const describedBy = computed(() => [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean).join(' ') || undefined)
</script>

<template>
  <div class="flex min-w-0 flex-col gap-1.5" data-slot="form-field">
    <label :for="id" class="text-sm font-medium">
      {{ label }}
      <span v-if="required" class="text-destructive" :title="t('common.required')" aria-hidden="true">*</span>
    </label>
    <slot :id="id" :described-by="describedBy" :invalid="invalid" />
    <p v-if="hint" :id="hintId" class="m-0 text-xs text-muted-foreground">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="m-0 text-xs text-destructive" role="alert">{{ error }}</p>
  </div>
</template>
