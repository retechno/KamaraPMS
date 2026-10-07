<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ApiError } from '@/api/problem'
import type { Approval, Violation } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import type { RestrictionOverrideInput } from '@/utils/restrictions'

/**
 * A sale the sales restrictions refused (409 STAY_RESTRICTED), shown with the rules it breaks. A person who may override gives a reason and, unless they are
 * the one who approves, an approver's credentials, and the same request is sent again with the override. A booking from the web or an OTA cannot be overridden.
 */
const props = defineProps<{ violations: Violation[]; overridable: boolean; busy?: boolean; error?: ApiError | null }>()
const emit = defineEmits<{ override: [input: RestrictionOverrideInput]; cancel: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const reason = ref('')
const approving = ref(false)

const may = computed(() => auth.can('reservation.override_restriction', property.currentId))
const selfApproves = computed(() => auth.can('reservation.restriction_approve', property.currentId))
const violationText = (v: Violation): string => t(`restrictions.violation_${v.type}`, { date: v.date, value: v.value ?? '', nights: v.nights ?? '' })
const reasonError = computed(() => props.error?.fieldMessage('restriction_override.reason'))

function submit(): void {
  if (!selfApproves.value) {
    approving.value = true
    return
  }
  emit('override', { reason: reason.value.trim() })
}

function approve(approval: Approval): void {
  approving.value = false
  emit('override', { reason: reason.value.trim(), approval })
}
</script>

<template>
  <div class="mb-4 rounded-md border border-warning/60 bg-warning/10 p-3 text-sm" role="alert" data-testid="restriction-refusal">
    <p class="m-0 font-medium">{{ t('restrictions.refusedTitle') }}</p>
    <ul class="m-0 mt-1 list-disc pl-5"><li v-for="(v, i) in violations" :key="i">{{ violationText(v) }}</li></ul>
    <p v-if="!overridable" class="m-0 mt-2" data-testid="restriction-final">{{ t('restrictions.cannotOverrideSource') }}</p>
    <p v-else-if="!may" class="m-0 mt-2" data-testid="restriction-not-allowed">{{ t('restrictions.whyRestricted') }}</p>
    <form v-else class="mt-2 flex flex-wrap items-end gap-3" novalidate data-testid="restriction-form" @submit.prevent="submit">
      <FormField class="min-w-64 flex-1" :label="t('restrictions.overrideReason')" :hint="t('restrictions.overrideHint')" :error="reasonError">
        <template #default="{ id, invalid }"><Input :id="id" v-model="reason" name="restriction_reason" maxlength="500" :aria-invalid="invalid" /></template>
      </FormField>
      <Button type="submit" size="sm" :disabled="busy || !reason.trim()" data-testid="override-restriction">{{ t('restrictions.overrideAndContinue') }}</Button>
      <Button type="button" variant="outline" size="sm" data-testid="cancel-override" @click="emit('cancel')">{{ t('common.cancel') }}</Button>
    </form>
    <Button v-if="!overridable || !may" type="button" variant="outline" size="sm" class="mt-2" data-testid="cancel-override" @click="emit('cancel')">{{ t('common.close') }}</Button>
  </div>
  <ApprovalDialog v-if="approving" :title="t('restrictions.approveTitle')" :message="t('restrictions.approveMessage')" :busy="busy" :error="error" @approve="approve" @cancel="approving = false" />
</template>
