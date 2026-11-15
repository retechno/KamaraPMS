<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { timeZones } from '@/utils/dates'

/**
 * P4 "New tenant" (docs/architecture/39-platform-console.md 11): code, name, time zone and, when wanted, a first administrator. The answer of the server carries the administrator's generated
 * password once; it is shown here with a Copy button and lives only in this component's state (never in a store, in storage or in the address), and it is dropped when the dialog closes.
 * Closing after a creation emits `created` with the code, so that the page can open the new tenant.
 */
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [boolean]; created: [code: string] }>()

const DEFAULT_ZONE = 'Asia/Jakarta'
const zones = computed(() => {
  const all = timeZones()
  return all.includes(DEFAULT_ZONE) ? all : [DEFAULT_ZONE, ...all]
})

const code = ref('')
const name = ref('')
const timezone = ref(DEFAULT_ZONE)
const adminEmail = ref('')
const adminName = ref('')
const error = ref<ApiError | null>(null)
const busy = ref(false)
const created = ref<{ code: string; name: string; password: string | null } | null>(null)
const copied = ref(false)

function reset(): void {
  code.value = ''
  name.value = ''
  timezone.value = DEFAULT_ZONE
  adminEmail.value = ''
  adminName.value = ''
  error.value = null
  busy.value = false
  created.value = null // the password goes with it
  copied.value = false
}
watch(() => props.open, (now) => { if (now) reset() }, { immediate: true })

const fieldError = (field: string) => error.value?.fieldMessage(field)
const canSubmit = computed(() => !busy.value && code.value.trim() !== '' && name.value.trim() !== '')

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  busy.value = true
  error.value = null
  const email = adminEmail.value.trim()
  const fullName = adminName.value.trim()
  try {
    const { data } = await platformApi.POST('/api/v1/platform/tenants', {
      body: {
        code: code.value.trim().toUpperCase(),
        name: name.value.trim(),
        timezone: timezone.value,
        ...(email || fullName ? { admin: { email, full_name: fullName } } : {}),
      },
    })
    if (data) created.value = { code: data.tenant.code, name: data.tenant.name, password: data.admin_password ?? null }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function copy(): Promise<void> {
  if (!created.value?.password) return
  try {
    await navigator.clipboard.writeText(created.value.password)
    copied.value = true
  } catch {
    copied.value = false // the clipboard is refused: the password stays on the screen to be selected
  }
}

/** Closing after a creation goes to the new tenant; the password is dropped either way. */
function close(): void {
  const done = created.value
  created.value = null
  emit('update:open', false)
  if (done) emit('created', done.code)
}
function onOpenChange(value: boolean): void {
  if (!value) close()
  else emit('update:open', true)
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpenChange">
    <DialogContent class="max-h-[90vh] max-w-lg overflow-y-auto p-5" data-testid="new-tenant-dialog">
      <template v-if="created">
        <DialogTitle>{{ t('platform.tenants.new.createdTitle') }}</DialogTitle>
        <DialogDescription class="mt-1">{{ t('platform.tenants.new.createdBody', { name: created.name, code: created.code }) }}</DialogDescription>
        <div v-if="created.password" class="mt-4 rounded-md border border-border bg-muted p-3" data-testid="admin-password-box" role="status">
          <p class="m-0 text-sm font-medium">{{ t('platform.tenants.new.passwordTitle') }}</p>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('platform.tenants.new.passwordNote') }}</p>
          <div class="mt-2 flex items-center gap-2">
            <code class="min-w-0 flex-1 select-all break-all rounded bg-card px-2 py-1.5 text-sm" :aria-label="t('platform.tenants.new.password')" data-testid="admin-password">{{ created.password }}</code>
            <Button type="button" variant="outline" size="sm" data-testid="copy-password" @click="copy">{{ copied ? t('platform.tenants.new.copied') : t('platform.tenants.new.copy') }}</Button>
          </div>
        </div>
        <div class="mt-5 flex justify-end">
          <Button type="button" data-testid="open-new-tenant" @click="close">{{ t('platform.tenants.new.openTenant') }}</Button>
        </div>
      </template>

      <template v-else>
        <DialogTitle>{{ t('platform.tenants.new.title') }}</DialogTitle>
        <DialogDescription class="mt-1">{{ t('platform.tenants.new.description') }}</DialogDescription>
        <form class="mt-4 grid gap-4" novalidate data-testid="new-tenant-form" @submit.prevent="submit">
          <ErrorNotice :error="error" inline data-testid="new-tenant-error" />
          <div class="grid gap-4 sm:grid-cols-[10rem_1fr]">
            <FormField :label="t('platform.tenants.new.code')" :hint="t('platform.tenants.new.codeHint')" required :error="fieldError('code')">
              <template #default="{ id, describedBy, invalid }">
                <Input :id="id" v-model="code" v-autofocus name="code" maxlength="20" autocomplete="off" class="uppercase" :aria-describedby="describedBy" :aria-invalid="invalid" />
              </template>
            </FormField>
            <FormField :label="t('platform.tenants.new.name')" required :error="fieldError('name')">
              <template #default="{ id, describedBy, invalid }">
                <Input :id="id" v-model="name" name="name" maxlength="200" autocomplete="off" :aria-describedby="describedBy" :aria-invalid="invalid" />
              </template>
            </FormField>
          </div>
          <FormField :label="t('platform.tenants.new.timezone')" :error="fieldError('timezone')">
            <template #default="{ id, describedBy, invalid }">
              <NativeSelect :id="id" v-model="timezone" name="timezone" :aria-describedby="describedBy" :aria-invalid="invalid">
                <option v-for="z in zones" :key="z" :value="z">{{ z }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <fieldset class="m-0 grid gap-3 rounded-md border border-border p-3">
            <legend class="px-1 text-sm font-medium">{{ t('platform.tenants.new.admin') }}</legend>
            <p class="m-0 text-xs text-muted-foreground">{{ t('platform.tenants.new.adminHint') }}</p>
            <div class="grid gap-3 sm:grid-cols-2">
              <FormField :label="t('platform.tenants.new.adminEmail')" :error="fieldError('admin.email') ?? fieldError('email')">
                <template #default="{ id, describedBy, invalid }">
                  <Input :id="id" v-model="adminEmail" name="admin_email" type="email" autocomplete="off" :aria-describedby="describedBy" :aria-invalid="invalid" />
                </template>
              </FormField>
              <FormField :label="t('platform.tenants.new.adminName')" :error="fieldError('admin.full_name') ?? fieldError('full_name')">
                <template #default="{ id, describedBy, invalid }">
                  <Input :id="id" v-model="adminName" name="admin_name" autocomplete="off" :aria-describedby="describedBy" :aria-invalid="invalid" />
                </template>
              </FormField>
            </div>
          </fieldset>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="close">{{ t('platform.tenants.new.cancel') }}</Button>
            <Button type="submit" :disabled="!canSubmit" data-testid="new-tenant-submit">{{ t('platform.tenants.new.create') }}</Button>
          </div>
        </form>
      </template>
    </DialogContent>
  </Dialog>
</template>
