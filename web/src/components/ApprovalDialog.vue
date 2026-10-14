<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, onMounted, ref, watch } from 'vue'
import type { ApiError } from '@/api/problem'
import type { Approval, ApprovalKind } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useApprovalRulesStore } from '@/stores/approvalRules'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { toMinor } from '@/utils/checkin'
import { formatMoney } from '@/utils/format'

/**
 * The shared "Approval" dialog: every correction (adjustment, reversal, void, refund) needs the approver's own
 * credentials. The approver may be the person who is signed in (their email is prefilled) or someone else.
 * It is a Dialog (focus stays inside, Escape cancels); a click outside does not close it, so a typed password is not lost by a slip.
 * The password lives only in this component's state: it is handed to `approve` once and cleared, never stored.
 *
 * It says the rule of the property for the correction it asks for (docs/architecture/32-distinct-approver.md): `kind` names the kind of approval and `amount`, when the screen knows it, the amount of the
 * correction. When the rule asks for someone other than the person who is signed in, their email is not prefilled; a tenant administrator is the exception and the line says so.
 */
const props = defineProps<{
  title: string
  message?: string
  busy?: boolean
  error?: ApiError | null
  kind?: ApprovalKind
  amount?: string
}>()
const emit = defineEmits<{ approve: [approval: Approval]; cancel: [] }>()

const auth = useAuthStore()
const properties = usePropertyStore()
const rules = useApprovalRulesStore()
const ownEmail = auth.me?.user.email ?? ''
const email = ref(ownEmail)
const touched = ref(false)
const isAdmin = computed(() => auth.me?.user.is_tenant_admin === true)
const password = ref('')
const passwordBox = ref<{ $el: HTMLInputElement } | null>(null)

// What the rule says for this correction: `none` (free), `distinct` (always someone else), `above` (someone else, the amount is above the limit), `within` (the person may approve their own, the amount
// is not above the limit) or `limit` (the rule has a limit and the screen does not know the amount).
const rule = computed(() => rules.ruleOf(props.kind))
const line = computed<{ mode: 'none' | 'distinct' | 'above' | 'within' | 'limit'; limit: string }>(() => {
  const r = rule.value
  if (!r || r.rule === 'FREE') return { mode: 'none', limit: '' }
  if (r.rule === 'DISTINCT') return { mode: 'distinct', limit: '' }
  const limit = formatMoney(r.threshold)
  if (props.amount === undefined) return { mode: 'limit', limit }
  return { mode: toMinor(props.amount, 3) > toMinor(r.threshold, 3) ? 'above' : 'within', limit }
})
const asksOther = computed(() => line.value.mode === 'distinct' || line.value.mode === 'above')

// The email is the person's own unless the rule asks for someone else (an administrator keeps theirs: they may approve their own, and it is recorded).
function settleEmail(): void {
  if (!touched.value && asksOther.value && !isAdmin.value) email.value = ''
}
watch(asksOther, settleEmail)
onMounted(() => {
  if (properties.currentId !== null) void rules.load(properties.currentId).then(settleEmail)
  settleEmail()
})

// The password is where the approver starts typing (the e-mail is usually prefilled).
function focusPassword(event: Event): void {
  event.preventDefault()
  passwordBox.value?.$el.focus()
}

function submit(): void {
  const approval = { email: email.value.trim(), password: password.value }
  password.value = ''
  emit('approve', approval)
}

function cancel(): void {
  password.value = ''
  emit('cancel')
}
</script>

<template>
  <Dialog :open="true" @update:open="(value) => { if (!value) cancel() }">
    <DialogContent class="max-w-md p-5" @open-auto-focus="focusPassword" @interact-outside.prevent>
      <form novalidate data-testid="approval-dialog" @submit.prevent="submit">
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ message ?? t('approval.message') }}</DialogDescription>
        <p v-if="line.mode !== 'none'" class="mb-3 rounded-md border border-border bg-muted px-3 py-2 text-sm" data-testid="approval-rule" :data-rule="line.mode">
          <template v-if="line.mode === 'within'">{{ t('approval.rule.within', { limit: line.limit }) }}</template>
          <template v-else-if="line.mode === 'limit'">{{ t('approval.rule.limit', { limit: line.limit }) }}</template>
          <template v-else-if="isAdmin">{{ line.mode === 'above' ? t('approval.rule.aboveAdmin', { limit: line.limit }) : t('approval.rule.distinctAdmin') }}</template>
          <template v-else>{{ line.mode === 'above' ? t('approval.rule.above', { limit: line.limit }) : t('approval.rule.distinct') }}</template>
        </p>
        <ErrorNotice v-if="error" :error="error" inline data-testid="approval-error">
          <p v-if="error.code === 'APPROVAL_SAME_USER'" class="m-0 mt-1 text-sm" data-testid="approval-same-user">
            {{ error.context.threshold ? t('approval.rule.above', { limit: formatMoney(String(error.context.threshold)) }) : t('approval.rule.distinct') }}
          </p>
        </ErrorNotice>
        <div class="flex flex-col gap-3">
          <FormField :label="t('approval.email')">
            <template #default="{ id }"><Input :id="id" v-model="email" name="approval_email" type="email" autocomplete="off" @input="touched = true" /></template>
          </FormField>
          <FormField :label="t('approval.password')">
            <template #default="{ id }"><Input :id="id" ref="passwordBox" v-model="password" name="approval_password" type="password" autocomplete="off" /></template>
          </FormField>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="approval-cancel" @click="cancel">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !email || !password" data-testid="approval-submit">{{ t('approval.approve') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
