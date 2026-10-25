<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEventType, BanquetPolicyTemplate, BanquetReason, BanquetSettings } from '@/api/types'
import BanquetRecipientsCard from '@/components/banquet/BanquetRecipientsCard.vue'
import BanquetTemplateDialog from '@/components/banquet/BanquetTemplateDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The banquet settings (docs/architecture/36-banquet.md 3.2, 6.10): what happens when a tentative booking asks for a room that another tentative booking holds (refuse, or queue behind it), and the
 * kinds of event and the reasons for losing or cancelling one. A change of the conflict mode applies to what is booked or moved after it; the queues that exist stay as they are.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const settings = reactive<{ mode: 'REJECT' | 'QUEUE'; from: string | null; to: string | null; posting: 'AUTO' | 'MANUAL'; priceMode: 'EXCLUSIVE' | 'INCLUSIVE'; maxDiscount: string }>({ mode: 'REJECT', from: null, to: null, posting: 'AUTO', priceMode: 'EXCLUSIVE', maxDiscount: '0' })
const templates = ref<BanquetPolicyTemplate[]>([])
const templateOpen = ref(false)
const editingTemplate = ref<BanquetPolicyTemplate | null>(null)
function editTemplate(x: BanquetPolicyTemplate | null): void {
  editingTemplate.value = x
  templateOpen.value = true
}
const types = ref<BanquetEventType[]>([])
const reasons = ref<BanquetReason[]>([])
const error = ref<ApiError | null>(null)
const saving = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [s, ty, re, tp] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/settings', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/event-types', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/reasons', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/policy-templates', path),
    ])
    const d: BanquetSettings | undefined = s.data
    if (d) Object.assign(settings, { mode: d.tentative_conflict_mode, from: d.operating_from ?? null, to: d.operating_to ?? null, posting: d.posting_mode, priceMode: d.default_price_mode, maxDiscount: d.max_discount_pct_no_approval })
    types.value = ty.data?.data ?? []
    reasons.value = re.data?.data ?? []
    templates.value = tp.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, load, { immediate: true })

async function saveSettings(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/banquet/settings', { params: { path: { propertyId } }, body: { tentative_conflict_mode: settings.mode, operating_from: settings.from || null, operating_to: settings.to || null, posting_mode: settings.posting, default_price_mode: settings.priceMode, max_discount_pct_no_approval: settings.maxDiscount.trim() || '0' } })
    toast.success(t('banquet.settings.saved'))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

// ---- kinds of event and reasons share one small dialog
type Kind = 'type' | 'reason'
const dialog = ref(false)
const what = ref<Kind>('type')
const dError = ref<ApiError | null>(null)
const form = reactive({ id: null as number | null, code: '', name: '', reasonKind: 'LOST' as 'LOST' | 'CANCEL', active: true })
function edit(kind: Kind, x?: BanquetEventType | BanquetReason): void {
  what.value = kind
  dError.value = null
  Object.assign(form, x ? { id: x.id, code: x.code, name: x.name, reasonKind: 'kind' in x ? x.kind : 'LOST', active: x.is_active } : { id: null, code: '', name: '', reasonKind: 'LOST', active: true })
  dialog.value = true
}
async function saveSmall(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  dError.value = null
  const common = { code: form.code.trim().toUpperCase(), name: form.name.trim(), is_active: form.active }
  try {
    if (what.value === 'type') {
      if (form.id === null) await api.POST('/api/v1/properties/{propertyId}/banquet/event-types', { params: { path: { propertyId } }, body: common })
      else await api.PATCH('/api/v1/properties/{propertyId}/banquet/event-types/{id}', { params: { path: { propertyId, id: form.id } }, body: common })
    } else {
      const body = { ...common, kind: form.reasonKind }
      if (form.id === null) await api.POST('/api/v1/properties/{propertyId}/banquet/reasons', { params: { path: { propertyId } }, body })
      else await api.PATCH('/api/v1/properties/{propertyId}/banquet/reasons/{id}', { params: { path: { propertyId, id: form.id } }, body })
    }
    dialog.value = false
    toast.success(t('banquet.settings.saved'))
    await load()
  } catch (e) {
    dError.value = e instanceof ApiError ? e : null
  }
}
</script>

<template>
  <PageHeader :title="t('banquet.settings.title')" :description="t('banquet.settings.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <div v-else class="flex flex-col gap-4">
    <Card class="p-4" data-testid="conflict-section">
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveSettings">
        <h2 class="m-0 text-base font-semibold">{{ t('banquet.settings.conflict') }}</h2>
        <p class="m-0 text-sm text-muted-foreground">{{ t('banquet.settings.conflictHint') }}</p>
        <label v-for="m in (['REJECT', 'QUEUE'] as const)" :key="m" class="flex cursor-pointer items-start gap-2 rounded-md border border-border p-3 text-sm" :class="settings.mode === m ? 'border-primary bg-primary/5' : ''">
          <input v-model="settings.mode" type="radio" name="tentative_conflict_mode" :value="m" class="mt-0.5 size-4 accent-primary" :data-testid="`mode-${m}`" />
          <span><span class="block font-medium">{{ t(`banquet.settings.mode_${m}`) }}</span><span class="text-muted-foreground">{{ t(`banquet.settings.mode_${m}_hint`) }}</span></span>
        </label>
        <div><Button type="submit" :disabled="saving" data-testid="settings-save">{{ t('common.save') }}</Button></div>
      </form>
    </Card>
    <BanquetRecipientsCard :property-id="pid" />

    <Card class="p-4" data-testid="money-section">
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveSettings">
        <h2 class="m-0 text-base font-semibold">{{ t('banquet.settings.money') }}</h2>
        <FormField :label="t('banquet.settings.priceMode')">
          <template #default="{ id }"><NativeSelect :id="id" v-model="settings.priceMode" name="default_price_mode"><option value="INCLUSIVE">{{ t('banquet.settings.price_INCLUSIVE') }}</option><option value="EXCLUSIVE">{{ t('banquet.settings.price_EXCLUSIVE') }}</option></NativeSelect></template>
        </FormField>
        <FormField :label="t('banquet.settings.postingMode')">
          <template #default="{ id }"><NativeSelect :id="id" v-model="settings.posting" name="posting_mode"><option value="AUTO">{{ t('banquet.settings.posting_AUTO_hint') }}</option><option value="MANUAL">{{ t('banquet.settings.posting_MANUAL_hint') }}</option></NativeSelect></template>
        </FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ t('banquet.settings.postingHint') }}</p>
        <FormField :label="t('banquet.settings.maxDiscount')">
          <template #default="{ id }"><Input :id="id" v-model="settings.maxDiscount" name="max_discount_pct_no_approval" inputmode="decimal" class="max-w-32" /></template>
        </FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ t('banquet.settings.maxDiscountHint') }}</p>
        <div><Button type="submit" :disabled="saving" data-testid="money-save">{{ t('common.save') }}</Button></div>
      </form>
    </Card>

    <Card class="p-4" data-testid="templates-section">
      <div class="mb-2 flex items-center justify-between gap-2"><h2 class="m-0 text-base font-semibold">{{ t('banquet.settings.templates') }}</h2><Button type="button" variant="outline" size="sm" data-testid="template-new" @click="editTemplate(null)">{{ t('banquet.template.new') }}</Button></div>
      <p class="m-0 mb-2 text-sm text-muted-foreground">{{ t('banquet.settings.templatesHint') }}</p>
      <ul class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="x in templates" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm" :data-testid="`template-${x.code}`">
          <span>
            <span class="font-medium">{{ x.name }}</span> <span class="text-muted-foreground">{{ x.code }} · {{ t(`banquet.policy.base${x.base}` as never) }}</span>
            <span class="block text-xs text-muted-foreground">{{ t('banquet.template.summary', { deposits: x.deposits.map((d) => `${d.percent}%`).join(' / '), tiers: x.tiers.length }) }}</span>
          </span>
          <span class="flex items-center gap-2">
            <Badge v-if="x.is_default" variant="booked">{{ t('banquet.template.isDefault') }}</Badge>
            <Badge :variant="x.is_active ? 'success' : 'outline'">{{ x.is_active ? t('common.active') : t('common.inactive') }}</Badge>
            <Button type="button" variant="outline" size="sm" :data-testid="`template-edit-${x.code}`" @click="editTemplate(x)">{{ t('common.edit') }}</Button>
          </span>
        </li>
      </ul>
    </Card>

    <Card class="p-4" data-testid="types-section">
      <div class="mb-2 flex items-center justify-between gap-2"><h2 class="m-0 text-base font-semibold">{{ t('banquet.settings.types') }}</h2><Button type="button" variant="outline" size="sm" data-testid="type-new" @click="edit('type')">{{ t('common.add') }}</Button></div>
      <ul class="m-0 flex list-none flex-col gap-1 p-0">
        <li v-for="x in types" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 text-sm" :data-testid="`type-${x.code}`">
          <span>{{ x.name }} <span class="text-muted-foreground">{{ x.code }}</span></span>
          <span class="flex items-center gap-2"><Badge :variant="x.is_active ? 'success' : 'outline'">{{ x.is_active ? t('common.active') : t('common.inactive') }}</Badge><Button type="button" variant="outline" size="sm" @click="edit('type', x)">{{ t('common.edit') }}</Button></span>
        </li>
      </ul>
    </Card>

    <Card class="p-4" data-testid="reasons-section">
      <div class="mb-2 flex items-center justify-between gap-2"><h2 class="m-0 text-base font-semibold">{{ t('banquet.settings.reasons') }}</h2><Button type="button" variant="outline" size="sm" data-testid="reason-new" @click="edit('reason')">{{ t('common.add') }}</Button></div>
      <ul class="m-0 flex list-none flex-col gap-1 p-0">
        <li v-for="x in reasons" :key="x.id" class="flex flex-wrap items-center justify-between gap-2 text-sm" :data-testid="`reason-${x.code}`">
          <span>{{ x.name }} <span class="text-muted-foreground">{{ x.code }} · {{ t(`banquet.settings.reason_${x.kind}`) }}</span></span>
          <span class="flex items-center gap-2"><Badge :variant="x.is_active ? 'success' : 'outline'">{{ x.is_active ? t('common.active') : t('common.inactive') }}</Badge><Button type="button" variant="outline" size="sm" @click="edit('reason', x)">{{ t('common.edit') }}</Button></span>
        </li>
      </ul>
    </Card>
  </div>

  <BanquetTemplateDialog v-if="pid !== null" v-model:open="templateOpen" :property-id="pid" :template="editingTemplate" @saved="load" />
  <Dialog :open="dialog" @update:open="(v) => (dialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="small-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveSmall">
        <DialogTitle>{{ what === 'type' ? t('banquet.settings.type') : t('banquet.settings.reason') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.settings.smallHint') }}</DialogDescription>
        <ErrorNotice v-if="dError" :error="dError" inline />
        <FormField :label="t('banquet.rooms.code')" required :error="dError?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="form.id !== null" class="uppercase" /></template></FormField>
        <FormField :label="t('banquet.rooms.name')" required :error="dError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template></FormField>
        <FormField v-if="what === 'reason'" :label="t('banquet.settings.reasonFor')">
          <template #default="{ id }"><NativeSelect :id="id" v-model="form.reasonKind" name="kind" :disabled="form.id !== null"><option value="LOST">{{ t('banquet.settings.reason_LOST') }}</option><option value="CANCEL">{{ t('banquet.settings.reason_CANCEL') }}</option></NativeSelect></template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="dialog = false">{{ t('common.cancel') }}</Button><Button type="submit" data-testid="small-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
