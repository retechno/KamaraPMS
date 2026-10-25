<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetDocument, BanquetEmails, BanquetEvent } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { currentLocale, t } from '@/i18n'
import { documentPath, openPdf } from '@/utils/documents'
import { formatDate } from '@/utils/format'

/**
 * The tab "Documents" (docs/architecture/36-banquet.md 4.6, mockup 42): the proposal, the contract and the BEO as PDF, the contract numbered once for the revision in force, and the e-mails that carried a
 * proposal or a contract with the state of each (queued, sent, failed, skipped). Sending records the document first, so what is sent is what was recorded; the e-mail goes through the same outbox as the
 * confirmation of a reservation.
 */
const props = defineProps<{ propertyId: number; event: BanquetEvent; canManage: boolean; canConfirm: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const documents = ref<BanquetDocument[]>([])
const emails = ref<BanquetEmails>({ enabled: false, data: [] })
const error = ref<ApiError | null>(null)
const busy = ref(false)

const definite = computed(() => props.event.status === 'DEFINITE' || props.event.status === 'COMPLETED')
const open = computed(() => ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(props.event.status))
const contracts = computed(() => documents.value.filter((d) => d.kind === 'CONTRACT'))

async function load(): Promise<void> {
  error.value = null
  const path = { params: { path: { propertyId: props.propertyId, id: props.event.id } } }
  try {
    const [d, e] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/documents', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/emails', path),
    ])
    documents.value = d.data?.data ?? []
    emails.value = e.data ?? { enabled: false, data: [] }
  } catch (err) {
    error.value = err instanceof ApiError ? err : null
  }
}
watch(() => [props.propertyId, props.event.id, props.event.status], load, { immediate: true })

async function pdf(path: string): Promise<void> {
  error.value = null
  try {
    await openPdf(path)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function issue(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/documents/contract', { params: { path: { propertyId: props.propertyId, id: props.event.id } } })
    toast.success(t('banquet.docs.contractIssued'))
    await load()
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- sending
const sendOpen = ref(false)
const sendKind = ref<'proposal' | 'contract'>('proposal')
const form = ref({ to: '', language: 'en' as 'en' | 'id' })
const sendError = ref<ApiError | null>(null)
function startSend(kind: 'proposal' | 'contract'): void {
  sendKind.value = kind
  form.value = { to: props.event.contact_email ?? '', language: currentLocale() === 'id' ? 'id' : 'en' }
  sendError.value = null
  sendOpen.value = true
}
async function send(): Promise<void> {
  busy.value = true
  sendError.value = null
  try {
    const path = { params: { path: { propertyId: props.propertyId, id: props.event.id } }, body: { to: form.value.to.trim(), language: form.value.language } }
    if (sendKind.value === 'contract') await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/emails/contract', path)
    else await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/emails/proposal', path)
    sendOpen.value = false
    toast.success(t('banquet.docs.queued'))
    await load()
    emit('changed')
  } catch (e) {
    sendError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const statusVariant = (s: string): 'inhouse' | 'booked' | 'destructive' | 'cancelled' => (s === 'SENT' ? 'inhouse' : s === 'QUEUED' ? 'booked' : s === 'FAILED' ? 'destructive' : 'cancelled')
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="docs-tab">
    <ErrorNotice v-if="error" :error="error" inline />

    <Card class="p-4" data-testid="docs-list">
      <h3 class="m-0 mb-2 text-base font-semibold">{{ t('banquet.docs.title') }}</h3>
      <ul class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
        <li class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" data-testid="doc-proposal">
          <div><strong>{{ t('banquet.docs.proposal') }}</strong><span class="block text-muted-foreground">{{ t('banquet.docs.proposalHint') }}</span></div>
          <div class="flex gap-2">
            <Button type="button" variant="outline" size="sm" data-testid="proposal-pdf" @click="pdf(documentPath.banquetProposal(propertyId, event.id))">{{ t('banquet.docs.open') }}</Button>
            <Button v-if="canManage && open && emails.enabled" type="button" variant="outline" size="sm" data-testid="proposal-send" @click="startSend('proposal')">{{ t('banquet.docs.send') }}</Button>
          </div>
        </li>
        <li class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" data-testid="doc-contract">
          <div>
            <strong>{{ t('banquet.docs.contract') }}</strong>
            <span class="block text-muted-foreground">{{ definite ? t('banquet.docs.contractHint') : t('banquet.docs.contractNeedsDefinite') }}</span>
          </div>
          <div v-if="definite" class="flex gap-2">
            <Button v-if="canConfirm && open" type="button" variant="outline" size="sm" :disabled="busy" data-testid="contract-issue" @click="issue">{{ t('banquet.docs.issue') }}</Button>
            <Button v-if="contracts.length" type="button" variant="outline" size="sm" data-testid="contract-pdf" @click="pdf(documentPath.banquetContract(propertyId, event.id))">{{ t('banquet.docs.open') }}</Button>
            <Button v-if="canConfirm && open && emails.enabled" type="button" variant="outline" size="sm" data-testid="contract-send" @click="startSend('contract')">{{ t('banquet.docs.send') }}</Button>
          </div>
        </li>
        <li class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" data-testid="doc-beo">
          <div><strong>{{ t('banquet.docs.beo') }}</strong><span class="block text-muted-foreground">{{ t('banquet.docs.beoHint') }}</span></div>
          <Button type="button" variant="outline" size="sm" data-testid="beo-open" @click="pdf(documentPath.banquetBeo(propertyId, event.id))">{{ t('banquet.docs.open') }}</Button>
        </li>
      </ul>
      <p v-if="!emails.enabled" class="m-0 mt-2 text-xs text-muted-foreground" data-testid="docs-no-mail">{{ t('banquet.docs.noMail') }}</p>
    </Card>

    <Card v-if="contracts.length" class="p-4" data-testid="contracts">
      <h3 class="m-0 mb-2 text-base font-semibold">{{ t('banquet.docs.contracts') }}</h3>
      <ul class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
        <li v-for="d in contracts" :key="d.id" class="flex flex-wrap items-center justify-between gap-2" :data-testid="`contract-${d.id}`">
          <span><strong>{{ d.number }}</strong> · {{ t('banquet.beo.revision', { n: d.revision_no ?? 0 }) }} · {{ formatDate(d.created_at.slice(0, 10)) }} · {{ d.created_by_name }}</span>
          <Button type="button" variant="outline" size="sm" @click="pdf(documentPath.banquetDocument(propertyId, d.id))">{{ t('banquet.docs.open') }}</Button>
        </li>
      </ul>
    </Card>

    <Card class="p-4" data-testid="email-history">
      <h3 class="m-0 mb-2 text-base font-semibold">{{ t('banquet.docs.emails') }}</h3>
      <p v-if="!emails.data.length" class="m-0 text-sm text-muted-foreground">{{ t('banquet.docs.emailsEmpty') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
        <li v-for="m in emails.data" :key="m.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3" :data-testid="`email-${m.id}`">
          <div>
            <strong>{{ t(`banquet.docs.kind.${m.kind}`) }}</strong><span v-if="m.document_number">{{ ' ' + m.document_number }}</span> → {{ m.to }}
            <span class="block text-xs text-muted-foreground">{{ formatDate(m.created_at.slice(0, 10)) }} · {{ m.language.toUpperCase() }}<template v-if="m.last_error"> · {{ m.last_error }}</template></span>
          </div>
          <Badge :variant="statusVariant(m.status)" :data-testid="`email-status-${m.id}`">{{ t(`banquet.docs.status.${m.status}`) }}</Badge>
        </li>
      </ul>
    </Card>

    <Dialog :open="sendOpen" @update:open="(v) => (sendOpen = v)">
      <DialogContent class="max-w-md p-5" data-testid="send-dialog" @interact-outside.prevent>
        <form novalidate class="flex flex-col gap-3" @submit.prevent="send">
          <DialogTitle>{{ sendKind === 'contract' ? t('banquet.docs.sendContract') : t('banquet.docs.sendProposal') }}</DialogTitle>
          <DialogDescription>{{ sendKind === 'contract' ? t('banquet.docs.sendContractHint') : t('banquet.docs.sendProposalHint') }}</DialogDescription>
          <ErrorNotice v-if="sendError" :error="sendError" inline />
          <FormField :label="t('banquet.docs.to')" :error="sendError?.fieldMessage('to')">
            <template #default="{ id }"><Input :id="id" v-model="form.to" name="to" type="email" /></template>
          </FormField>
          <FormField :label="t('banquet.docs.language')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.language" name="language">
                <option value="en">English</option>
                <option value="id">Bahasa Indonesia</option>
              </NativeSelect>
            </template>
          </FormField>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="sendOpen = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy" data-testid="send-submit">{{ t('banquet.docs.send') }}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  </div>
</template>
