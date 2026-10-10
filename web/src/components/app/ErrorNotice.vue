<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ApiError } from '@/api/problem'
import { restoreFocus, trackFocus } from '@/composables/focusMemory'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'

/**
 * Where a failure of an action goes. The form keeps the `error` it got (its fields read their own messages from it); this decides what the person sees of the rest:
 *
 * - A refusal that names fields (validation: required, format, not valid) stays inline, as a banner over the form beside the messages under the fields, because that is where it is corrected.
 * - Anything else (a business rule that says no, a conflict, a failure of the network or of the server, a refusal of permission) is a toast: it is read, it does not move the form, and it does
 *   not stay on the page after it has been read. A toast is raised once for an error, however many times the page draws it.
 *
 * `inline` forces the banner for an error that has to stay in front of the person (a refusal they must act on in place).
 *
 * A banner is for people: the message, the next step when the language files have one for the code (`errorActions.<CODE>`), and the code itself folded away under "Technical details".
 */
defineOptions({ inheritAttrs: false })
const props = defineProps<{ error: ApiError | null; inline?: boolean }>()

const inline = computed(() => !!props.error && (props.inline || props.error.fieldErrors.length > 0))
const action = computed(() => {
  const key = `errorActions.${props.error?.code}`
  return props.error && te(key) ? t(key as never) : ''
})

// A zero-size mark that tells which dialog (or page) the notice belongs to, drawn when nothing else is.
const anchor = ref<HTMLElement | null>(null)
trackFocus()

watch(() => props.error, (e) => {
  if (!e) return
  if (!inline.value) toast.fromError(e)
  // The failed save left the focus on the page (its button was disabled while it ran): give it back.
  void restoreFocus(() => anchor.value)
}, { immediate: true })
</script>

<template>
  <div v-if="error && inline" ref="anchor" v-bind="$attrs" :class="['alert', $attrs.class]" role="alert" :data-testid="($attrs['data-testid'] as string | undefined) ?? 'form-error'">
    <p class="m-0">{{ error.message }}</p>
    <p v-if="action" class="m-0 mt-1 text-sm" data-slot="error-action">{{ action }}</p>
    <slot />
    <details class="mt-1 text-xs" data-slot="error-details">
      <summary class="cursor-pointer">{{ t('common.technicalDetails') }}</summary>
      <code>{{ error.code }}</code><span v-if="error.requestId"> · {{ t('common.requestId', { id: error.requestId }) }}</span>
    </details>
  </div>
  <span v-else ref="anchor" hidden data-slot="error-anchor" />
</template>
