<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'

/**
 * Suspend a tenant (an `alertdialog`: it signs every user out) or reactivate it (a plain dialog). Both ask for a reason; suspending also asks for the code of the tenant typed again.
 * The browser compares the code case-insensitively to enable the button, the server checks it again (`CONFIRMATION_MISMATCH`) and gets the text as it was typed.
 */
const props = defineProps<{ open: boolean; tenantCode: string; tenantName: string; mode: 'suspend' | 'reactivate' }>()
const emit = defineEmits<{ 'update:open': [boolean]; done: [] }>()

const reason = ref('')
const confirm = ref('')
const busy = ref(false)
const error = ref<ApiError | null>(null)
const reasonMissing = ref(false)

watch(
  () => props.open,
  (now) => {
    if (!now) return
    reason.value = ''
    confirm.value = ''
    error.value = null
    reasonMissing.value = false
  },
  { immediate: true },
)

const suspending = computed(() => props.mode === 'suspend')
const matches = computed(() => confirm.value.trim().toUpperCase() === props.tenantCode.toUpperCase())
const canSubmit = computed(() => !busy.value && (!suspending.value || matches.value))
const reasonError = computed(() => (reasonMissing.value ? t(suspending.value ? 'platform.tenant.suspend.reasonRequired' : 'platform.tenant.reactivate.reasonRequired') : error.value?.fieldMessage('reason')))
const confirmError = computed(() => error.value?.fieldMessage('confirm_code'))

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  reasonMissing.value = reason.value.trim() === ''
  if (reasonMissing.value) return
  busy.value = true
  error.value = null
  try {
    const path = { code: props.tenantCode }
    if (suspending.value) await platformApi.POST('/api/v1/platform/tenants/{code}/suspend', { params: { path }, body: { reason: reason.value.trim(), confirm_code: confirm.value.trim() } })
    else await platformApi.POST('/api/v1/platform/tenants/{code}/reactivate', { params: { path }, body: { reason: reason.value.trim() } })
    emit('update:open', false)
    emit('done')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (!(e instanceof ApiError)) throw e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent :role="suspending ? 'alertdialog' : 'dialog'" class="max-w-md p-5" :data-testid="suspending ? 'suspend-dialog' : 'reactivate-dialog'" @interact-outside.prevent>
      <form novalidate @submit.prevent="submit">
        <div class="mb-3 flex items-start justify-between gap-3">
          <DialogTitle>{{ suspending ? t('platform.tenant.suspend.title', { name: tenantName }) : t('platform.tenant.reactivate.title', { name: tenantName }) }}</DialogTitle>
          <Button type="button" variant="ghost" size="icon" :aria-label="t('common.close')" data-testid="dialog-close" @click="emit('update:open', false)"><X /></Button>
        </div>
        <ErrorNotice v-if="error" :error="error" inline class="mb-3" />
        <ul v-if="suspending" class="mb-3 mt-0 grid list-disc gap-1.5 pl-5 text-sm" data-testid="suspend-consequences">
          <li>{{ t('platform.tenant.suspend.signOut') }}</li>
          <li>{{ t('platform.tenant.suspend.dataKept') }}</li>
          <li>{{ t('platform.tenant.suspend.late') }}</li>
        </ul>
        <p v-else class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('platform.tenant.reactivate.body') }}</p>
        <div class="flex flex-col gap-3">
          <FormField :label="suspending ? t('platform.tenant.suspend.reason') : t('platform.tenant.reactivate.reason')" :error="reasonError" required>
            <template #default="{ id, describedBy, invalid }">
              <textarea
                :id="id"
                v-model="reason"
                name="reason"
                rows="2"
                maxlength="500"
                data-autofocus
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                class="w-full rounded-md border border-border bg-card p-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring aria-[invalid=true]:border-destructive"
                @input="reasonMissing = false"
              />
            </template>
          </FormField>
          <FormField v-if="suspending" :label="t('platform.tenant.suspend.confirm', { code: tenantCode })" :error="confirmError">
            <template #default="{ id, describedBy, invalid }">
              <Input :id="id" v-model="confirm" name="confirm_code" autocomplete="off" autocapitalize="characters" :aria-describedby="describedBy" :aria-invalid="invalid || undefined" />
            </template>
          </FormField>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="dialog-cancel" @click="emit('update:open', false)">{{ suspending ? t('platform.tenant.suspend.cancel') : t('platform.tenant.reactivate.cancel') }}</Button>
          <Button type="submit" :variant="suspending ? 'destructive' : 'default'" :disabled="!canSubmit" data-testid="dialog-submit">{{ suspending ? t('platform.tenant.suspend.submit') : t('platform.tenant.reactivate.submit') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
