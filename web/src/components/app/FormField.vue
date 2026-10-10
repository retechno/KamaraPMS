<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { t } from '@/i18n'
import { focusProgrammatically } from '@/lib/focus'

/**
 * A label, a control, a hint and an error, tied together for assistive technology. The control comes from the slot and
 * receives what it must carry: `<template #default="{ id, describedBy, invalid }"><Input :id="id" :aria-describedby="describedBy"
 * :aria-invalid="invalid" /></template>`.
 */
const props = defineProps<{ label: string; hint?: string; error?: string; required?: boolean; floatHint?: boolean }>()

const id = useId()
const hintId = `${id}-hint`
const errorId = `${id}-error`
const root = ref<HTMLElement | null>(null)
const invalid = computed(() => !!props.error)

// When a field is refused, the cursor goes to the first refused field of the form: the person starts correcting there. The first FormField of the form to be refused does it; the others find the focus
// already on an invalid field and leave it. A field that is invalid from the start (an error that was there before) does not take the focus.
watch(() => props.error, (now, before) => {
  if (!now || now === before) return
  void nextTick(() => {
    const form = root.value?.closest('form, [role=dialog]') ?? root.value?.parentElement
    const active = document.activeElement
    if (active instanceof HTMLElement && form?.contains(active) && active.getAttribute('aria-invalid') === 'true') return
    const target = form?.querySelector<HTMLElement>('[aria-invalid=true]')
    if (target) focusProgrammatically(target)
  })
})
const describedBy = computed(() => [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean).join(' ') || undefined)
</script>

<template>
  <div ref="root" class="relative flex min-w-0 flex-col gap-1.5" data-slot="form-field">
    <label :for="id" class="text-sm font-medium">
      {{ label }}
      <span v-if="required" class="text-destructive" :title="t('common.required')" aria-hidden="true">*</span>
    </label>
    <slot :id="id" :described-by="describedBy" :invalid="invalid" />
    <p v-if="hint" :id="hintId" :class="floatHint ? 'absolute left-0 top-full m-0 mt-0.5 whitespace-nowrap text-xs text-muted-foreground' : 'm-0 text-xs text-muted-foreground'">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="m-0 text-xs text-destructive" role="alert">{{ error }}</p>
  </div>
</template>
