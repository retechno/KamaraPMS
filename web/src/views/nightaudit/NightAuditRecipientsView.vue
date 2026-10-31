<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightAuditPackRecipient } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { confirm } from '@/composables/useConfirm'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The recipients of the night audit pack (docs/architecture/38-reports-night-audit-pack.md 6 and 8.1): who gets the pack by e-mail after each night audit, and which sections. */
const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('nightaudit.pack_manage', pid.value))

/** The sections in the order of the pack; the marked ones hold personal data of guests or money and leave the system. */
const SECTIONS = [
  { code: 'SUMMARY' }, { code: 'FLASH' }, { code: 'REVENUE' }, { code: 'TAX' }, { code: 'PAYMENTS' }, { code: 'Z_REPORTS', mark: 'cash' }, { code: 'GUEST_LEDGER', mark: 'guestNames' },
  { code: 'DEPOSIT_LEDGER' }, { code: 'CORRECTIONS', mark: 'guestNames' }, { code: 'CANCELLATIONS', mark: 'guestNames' }, { code: 'OUTLETS' }, { code: 'OUT_OF_ORDER' },
  { code: 'IN_HOUSE', mark: 'guestNames' }, { code: 'NEXT_DAY', mark: 'guestNames' }, { code: 'AUDIT_NOTES' },
] as const
const DEFAULT_SECTIONS = ['SUMMARY', 'FLASH']

const rows = ref<NightAuditPackRecipient[]>([])
const emailEnabled = ref(true)
const loading = ref(false)
const error = ref<ApiError | null>(null)
const editing = ref<{ id: number | null; email: string; name: string; sections: string[]; is_active: boolean } | null>(null)
const busy = ref(false)
const formError = ref<ApiError | null>(null)

const columns = computed<Column<NightAuditPackRecipient>[]>(() => [
  { key: 'email', label: t('nightAuditRecipients.address') },
  { key: 'sections', label: t('nightAuditRecipients.sections') },
  { key: 'is_active', label: t('nightAuditRecipients.status') },
  { key: 'last', label: t('nightAuditRecipients.last') },
  { key: 'actions', label: '', align: 'right' },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canManage.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/pack-recipients', { params: { path: { propertyId } } })
    rows.value = data?.items ?? []
    emailEnabled.value = data?.email_enabled ?? true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

function startAdd(): void {
  formError.value = null
  editing.value = { id: null, email: '', name: '', sections: [...DEFAULT_SECTIONS], is_active: true }
}

function startEdit(r: NightAuditPackRecipient): void {
  formError.value = null
  editing.value = { id: r.id, email: r.email, name: r.name, sections: [...r.sections], is_active: r.is_active }
}

/** The sections of a row in a short line: the names when there are few, a count and the marks when there are many. */
function sectionsLine(r: NightAuditPackRecipient): string {
  if (r.sections.length <= 2) return r.sections.map((c) => t(`nightAuditArchive.section.${c}` as never)).join(', ')
  return t('nightAuditRecipients.sectionsCount', { n: r.sections.length })
}

function marks(r: NightAuditPackRecipient): string[] {
  const kinds = new Set(SECTIONS.filter((s) => 'mark' in s && r.sections.includes(s.code)).map((s) => ('mark' in s ? s.mark : '')))
  return [...kinds].map((k) => t(`nightAuditRecipients.${k}` as never))
}

async function save(): Promise<void> {
  const propertyId = pid.value
  const f = editing.value
  if (propertyId === null || !f) return
  busy.value = true
  formError.value = null
  try {
    const body = { email: f.email.trim(), name: f.name.trim(), sections: SECTIONS.map((s) => s.code).filter((c) => f.sections.includes(c)), is_active: f.is_active }
    if (f.id === null) await api.POST('/api/v1/properties/{propertyId}/night-audit/pack-recipients', { params: { path: { propertyId } }, body })
    else await api.PATCH('/api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}', { params: { path: { propertyId, id: f.id } }, body })
    toast.success(t('nightAuditRecipients.saved'))
    editing.value = null
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function remove(): Promise<void> {
  const propertyId = pid.value
  const f = editing.value
  if (propertyId === null || !f || f.id === null) return
  if (!(await confirm({ title: t('nightAuditRecipients.removeTitle', { email: f.email }), description: t('nightAuditRecipients.removeHint'), destructive: true }))) return
  busy.value = true
  formError.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}', { params: { path: { propertyId, id: f.id } } })
    toast.success(t('nightAuditRecipients.removed'))
    editing.value = null
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(pid, () => { void load() }, { immediate: true })
</script>

<template>
  <PageHeader :title="t('nightAuditRecipients.title')" :description="t('nightAuditRecipients.description')">
    <template #actions>
      <Button v-if="canManage && pid !== null" size="sm" data-testid="add-recipient" @click="startAdd">{{ t('nightAuditRecipients.add') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice :error="error" />
  <p v-if="pid === null" class="muted">{{ t('nightAuditRecipients.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="no-access">{{ t('nightAuditRecipients.noAccess') }}</p>

  <template v-else>
    <p class="mb-4 mt-0 text-sm" :class="emailEnabled ? 'text-muted-foreground' : 'text-warning'" role="status" data-testid="email-state">
      {{ emailEnabled ? t('nightAuditRecipients.emailOn') : t('nightAuditRecipients.emailOff') }}
    </p>

    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="loading"
      :empty-title="t('nightAuditRecipients.empty')"
      :empty-description="t('nightAuditRecipients.emptyHint')"
      :row-test-id="(r) => `recipient-${r.id}`"
      data-testid="recipients"
    >
      <template #cell-email="{ row }">
        <div class="font-medium">{{ row.email }}</div>
        <small v-if="row.name" class="text-muted-foreground">{{ row.name }}</small>
      </template>
      <template #cell-sections="{ row }">
        {{ sectionsLine(row) }}
        <Badge v-for="m in marks(row)" :key="m" variant="warning" class="ml-1">{{ m }}</Badge>
      </template>
      <template #cell-is_active="{ row }">
        <Badge :variant="row.is_active ? 'success' : 'secondary'">{{ row.is_active ? t('nightAuditRecipients.active') : t('nightAuditRecipients.inactive') }}</Badge>
      </template>
      <template #cell-last="{ row }">
        <span v-if="!row.last_status" class="text-muted-foreground" data-testid="last-none">{{ t('nightAuditRecipients.lastNone') }}</span>
        <template v-else>
          {{ $date(row.last_pack_date) }} ·
          <Badge :variant="row.last_status === 'FAILED' ? 'destructive' : row.last_status === 'SENT' ? 'success' : 'secondary'" :data-testid="`last-${row.id}`">{{ t(`nightAuditRecipients.lastState.${row.last_status}` as never) }}</Badge>
        </template>
      </template>
      <template #cell-actions="{ row }">
        <Button variant="outline" size="sm" :data-testid="`edit-${row.id}`" @click="startEdit(row)">{{ t('nightAuditRecipients.edit') }}</Button>
      </template>
    </DataTable>

    <p class="mb-0 mt-4 text-xs text-muted-foreground">{{ t('nightAuditRecipients.auditNote') }}</p>
  </template>

  <Dialog :open="!!editing" @update:open="(v) => { if (!v) editing = null }">
    <DialogContent v-if="editing" class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="recipient-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ editing.id === null ? t('nightAuditRecipients.add') : t('nightAuditRecipients.edit') }}</DialogTitle>
        <DialogDescription>{{ t('nightAuditRecipients.sensitiveNote') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('nightAuditRecipients.address')" required :error="formError?.fieldMessage('email')">
          <template #default="{ id }"><Input :id="id" v-model="editing.email" name="email" type="email" maxlength="254" autocomplete="off" /></template>
        </FormField>
        <FormField :label="t('nightAuditRecipients.name')" :error="formError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="editing.name" name="name" maxlength="100" autocomplete="off" /></template>
        </FormField>
        <label class="flex cursor-pointer items-center gap-2 text-sm font-medium"><input v-model="editing.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" /> {{ t('nightAuditRecipients.activeHint') }}</label>
        <fieldset class="m-0 grid gap-1 border-0 p-0" data-testid="recipient-sections">
          <legend class="mb-1 p-0 text-sm font-medium">{{ t('nightAuditRecipients.sectionsSent') }}</legend>
          <label v-for="s in SECTIONS" :key="s.code" class="flex cursor-pointer items-center gap-2 text-sm">
            <input v-model="editing.sections" type="checkbox" :value="s.code" :name="`section_${s.code}`" class="size-4 accent-primary" />
            {{ t(`nightAuditArchive.section.${s.code}` as never) }}
            <Badge v-if="'mark' in s" variant="warning">{{ t(`nightAuditRecipients.${s.mark}` as never) }}</Badge>
          </label>
          <p v-if="formError?.fieldMessage('sections')" class="m-0 text-xs text-destructive" role="alert">{{ formError.fieldMessage('sections') }}</p>
        </fieldset>
        <div class="flex justify-between gap-2">
          <Button v-if="editing.id !== null" type="button" variant="outline" class="text-destructive" :disabled="busy" data-testid="remove-recipient" @click="remove">{{ t('nightAuditRecipients.remove') }}</Button>
          <span v-else />
          <div class="flex gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !editing.email.trim() || !editing.sections.length" data-testid="save-recipient">{{ t('nightAuditRecipients.save') }}</Button>
          </div>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
