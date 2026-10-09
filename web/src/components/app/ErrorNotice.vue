<script setup lang="ts">
import { computed, watch } from 'vue'
import type { ApiError } from '@/api/problem'
import { toast } from '@/composables/useToast'

/**
 * Where a failure of an action goes. The form keeps the `error` it got (its fields read their own messages from it); this decides what the person sees of the rest:
 *
 * - A refusal that names fields (validation: required, format, not valid) stays inline, as a banner over the form beside the messages under the fields, because that is where it is corrected.
 * - Anything else (a business rule that says no, a conflict, a failure of the network or of the server, a refusal of permission) is a toast: it is read, it does not move the form, and it does
 *   not stay on the page after it has been read. A toast is raised once for an error, however many times the page draws it.
 *
 * `inline` forces the banner for an error that has to stay in front of the person (a refusal they must act on in place).
 */
const props = defineProps<{ error: ApiError | null; inline?: boolean }>()

const inline = computed(() => !!props.error && (props.inline || props.error.fieldErrors.length > 0))

watch(() => props.error, (e) => {
  if (e && !inline.value) toast.fromError(e)
}, { immediate: true })
</script>

<template>
  <p v-if="error && inline" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
</template>
