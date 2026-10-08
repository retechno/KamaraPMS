<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, InHouseRow, StayDetail } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import FormField from '@/components/app/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * Edit the rate of an in-house stay: the nights with their status, the new rate, and what it applies to (the selected night, or the remaining nights that are not charged). The server decides:
 * a night that is not charged changes in the snapshot of the booking; a charged night (also on a closed day) keeps its charge and gets an adjustment for the difference, which needs an
 * approval. The rate plan is never changed from here.
 */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ row: InHouseRow | null }>()
const emit = defineEmits<{ saved: [] }>()

type Night = StayDetail['nightly_rates'][number]
const auth = useAuthStore()
const property = usePropertyStore()

const detail = ref<StayDetail | null>(null)
const selected = ref('')
const applyTo = ref<'NIGHT' | 'REMAINING'>('NIGHT')
const amount = ref('')
const reason = ref('')
const error = ref<ApiError | null>(null)
const approvalError = ref<ApiError | null>(null)
const approving = ref(false)
const busy = ref(false)

const nights = computed<Night[]>(() => detail.value?.nightly_rates ?? [])
const canAdjust = computed(() => auth.can('folio.adjust', property.currentId))
const isOpen = (n: Night) => n.status === 'OPEN'
const selectable = (n: Night) => isOpen(n) || canAdjust.value
const fieldError = (field: string) => error.value?.fieldMessage(field)
const variants = { OPEN: 'outline', POSTED: 'warning', CLOSED: 'secondary' } as const

/** The nights the change would touch: the selected one, or it and every later night that is not charged. */
const targets = computed(() => {
  const from = selected.value
  if (!from) return []
  return applyTo.value === 'NIGHT' ? nights.value.filter((n) => n.date === from) : nights.value.filter((n) => n.date >= from && isOpen(n))
})
const validAmount = computed(() => /^\d+(\.\d+)?$/.test(amount.value.trim()))
const changing = computed(() => validAmount.value && targets.value.some((n) => Number(n.amount) !== Number(amount.value)))
const needsApproval = computed(() => targets.value.some((n) => !isOpen(n)))
const isTarget = (n: Night) => targets.value.some((x) => x.date === n.date)
const canSubmit = computed(() => !!detail.value && !busy.value && changing.value && reason.value.trim() !== '')

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !props.row) return
  error.value = null
  detail.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: props.row.id } } })
    detail.value = data ?? null
    selected.value = (nights.value.find(isOpen) ?? nights.value[0])?.date ?? ''
    applyTo.value = 'NIGHT'
    amount.value = ''
    reason.value = ''
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function submit(): void {
  if (!canSubmit.value) return
  if (needsApproval.value) {
    approvalError.value = null
    approving.value = true
    return
  }
  void send()
}

async function send(approval?: Approval): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !detail.value) return
  busy.value = true
  error.value = null
  approvalError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/rates', {
      params: { path: { propertyId, id: detail.value.stay.id } },
      body: { version: detail.value.stay.version, apply_to: applyTo.value, date: selected.value, amount: amount.value.trim(), reason: reason.value.trim(), approval },
    })
    approving.value = false
    open.value = false
    emit('saved')
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    if (approval) approvalError.value = err
    else error.value = err
    if (err?.code === 'VERSION_CONFLICT') {
      approving.value = false
      await load()
      error.value = err // load() clears the error; the person is told why the screen was refreshed
    }
  } finally {
    busy.value = false
  }
}

watch(() => [open.value, props.row?.id], () => { if (open.value) void load() }, { immediate: true })
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent v-if="row" class="top-[6vh] max-h-[88vh] max-w-3xl overflow-y-auto p-5" data-testid="edit-rate-dialog">
      <DialogTitle>{{ t('frontDesk.rateEdit.title') }}</DialogTitle>
      <DialogDescription class="mt-1">{{ t('frontDesk.rateEdit.description') }}</DialogDescription>

      <dl class="mt-3 grid grid-cols-3 gap-3 text-sm">
        <div><dt class="text-xs uppercase text-muted-foreground">{{ t('frontDesk.rateEdit.room') }}</dt><dd class="m-0" data-testid="rate-room">{{ row.room.number }} · {{ row.room.room_type_code }}</dd></div>
        <div><dt class="text-xs uppercase text-muted-foreground">{{ t('frontDesk.rateEdit.guest') }}</dt><dd class="m-0" data-testid="rate-guest">{{ row.guest.name }}</dd></div>
        <div><dt class="text-xs uppercase text-muted-foreground">{{ t('frontDesk.rateEdit.ratePlan') }}</dt><dd class="m-0" data-testid="rate-plan">{{ row.rate.rate_plan_code }}</dd></div>
      </dl>

      <p v-if="error" class="alert mt-3" role="alert" data-testid="rate-error">{{ error.message }} <code>{{ error.code }}</code></p>
      <p v-if="!detail && !error" class="muted mt-3">{{ t('frontDesk.rateEdit.loading') }}</p>

      <form v-if="detail" class="mt-4" novalidate data-testid="rate-form" @submit.prevent="submit">
        <table class="w-full border-collapse text-sm" data-testid="rate-nights">
          <thead>
            <tr class="text-left text-xs uppercase text-muted-foreground">
              <th class="py-1 pr-2" />
              <th class="py-1 pr-2">{{ t('frontDesk.rateEdit.night') }}</th>
              <th class="py-1 pr-2">{{ t('frontDesk.rateEdit.status') }}</th>
              <th class="py-1 pr-2 text-right">{{ t('frontDesk.rateEdit.current') }}</th>
              <th class="py-1 text-right">{{ t('frontDesk.rateEdit.next') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="n in nights" :key="n.date" class="border-t border-border" :data-testid="`night-${n.date}`">
              <td class="py-1.5 pr-2"><input v-model="selected" type="radio" name="night" :value="n.date" :disabled="!selectable(n)" :aria-label="n.date" /></td>
              <td class="py-1.5 pr-2">{{ $date(n.date) }}</td>
              <td class="py-1.5 pr-2"><Badge :variant="variants[n.status]" :data-status="n.status">{{ t(`frontDesk.rateEdit.nightStatus.${n.status}`) }}</Badge></td>
              <td class="py-1.5 pr-2 text-right tabular-nums" :data-testid="`current-${n.date}`">{{ $money(n.amount) }}</td>
              <td class="py-1.5 text-right tabular-nums" :data-testid="`next-${n.date}`">{{ isTarget(n) && validAmount ? $money(amount.trim()) : '—' }}</td>
            </tr>
          </tbody>
        </table>

        <div class="mt-4 grid gap-4 sm:grid-cols-2">
          <FormField :label="t('frontDesk.rateEdit.applyTo')">
            <template #default>
              <label class="flex items-center gap-2 text-sm"><input v-model="applyTo" type="radio" name="apply_to" value="NIGHT" />{{ t('frontDesk.rateEdit.selected') }}</label>
              <label class="flex items-center gap-2 text-sm"><input v-model="applyTo" type="radio" name="apply_to" value="REMAINING" />{{ t('frontDesk.rateEdit.remaining') }}</label>
              <small v-if="applyTo === 'REMAINING'" class="text-xs text-muted-foreground">{{ t('frontDesk.rateEdit.remainingHint') }}</small>
            </template>
          </FormField>
          <FormField :label="t('frontDesk.rateEdit.newRate')" :error="fieldError('amount')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField class="sm:col-span-2" :label="t('frontDesk.rateEdit.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
        </div>

        <p v-if="needsApproval" class="alert warning mt-3" data-testid="charged-note">{{ t('frontDesk.rateEdit.chargedNote') }}</p>
        <p v-if="!canAdjust && nights.some((n) => !isOpen(n))" class="mb-0 mt-2 text-xs text-muted-foreground">{{ t('frontDesk.rateEdit.noAdjustRight') }}</p>
        <p v-if="validAmount && !changing && targets.length" class="mb-0 mt-2 text-xs text-muted-foreground" data-testid="nothing">{{ t('frontDesk.rateEdit.nothingToChange') }}</p>

        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="open = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!canSubmit" data-testid="rate-apply">{{ t('frontDesk.rateEdit.apply') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog v-if="approving" :title="t('frontDesk.rateEdit.approvalTitle')" :message="t('frontDesk.rateEdit.approvalMessage')" :busy="busy" :error="approvalError" @approve="(a) => send(a)" @cancel="approving = false" />
</template>
