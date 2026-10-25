<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetPolicyTemplate } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { formatDateShort } from '@/utils/format'

/**
 * Turns a tentative event into a definite one (docs/architecture/36-banquet.md 6.7): its holds become exclusive. Only the first option of each room can be confirmed; an event behind another says
 * who is in front. The options queued behind are told that they lost their room. The policy template chosen (the default one is offered) is copied to the event with its deposit schedule; a change of the
 * template afterwards changes no event. The EVENT folio of a down payment is kept: confirming does not open another.
 */
const props = defineProps<{ open: boolean; propertyId: number; event: BanquetEvent; templates?: BanquetPolicyTemplate[] }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [event: BanquetEvent] }>()

const error = ref<ApiError | null>(null)
const busy = ref(false)
const templateId = ref(0)
const chosen = computed(() => props.templates?.find((x) => x.id === templateId.value))
const live = computed(() => props.event.sessions.filter((s) => s.status === 'ACTIVE'))
const behind = computed(() => live.value.filter((s) => (s.rank ?? 1) > 1))
watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  templateId.value = props.templates?.find((x) => x.is_default)?.id ?? 0
})

async function confirmEvent(): Promise<void> {
  busy.value = true
  error.value = null
  const propertyId = props.propertyId
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/confirm', { params: { path: { propertyId, id: props.event.id } }, body: { policy_template_id: templateId.value || null } })
    if (data) emit('saved', data)
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="banquet-confirm-dialog" @interact-outside.prevent>
      <div class="flex flex-col gap-3">
        <DialogTitle>{{ t('banquet.confirm.title', { name: event.name }) }}</DialogTitle>
        <DialogDescription>{{ t('banquet.confirm.intro') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm" data-testid="confirm-sessions">
          <li v-for="s in live" :key="s.id" class="flex flex-wrap justify-between gap-2 rounded-md border border-border px-3 py-2" :data-testid="`confirm-session-${s.id}`">
            <span>{{ s.name }} · {{ formatDateShort(s.session_date) }} {{ (s.start_time ?? '').slice(0, 5) }}–{{ (s.end_time ?? '').slice(0, 5) }} · {{ s.room_name }}</span>
            <span :class="(s.rank ?? 1) > 1 ? 'text-destructive' : 'text-success-text'">{{ (s.rank ?? 1) > 1 ? t('banquet.confirm.behind', { n: s.rank ?? 1 }) : t('banquet.confirm.first') }}</span>
          </li>
        </ul>
        <FormField v-if="templates?.length" :label="t('banquet.confirm.policy')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="templateId" name="policy_template_id" data-testid="confirm-template">
              <option :value="0">{{ t('banquet.confirm.noPolicy') }}</option>
              <option v-for="x in templates" :key="x.id" :value="x.id">{{ x.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <p v-if="chosen" class="m-0 text-sm text-muted-foreground" data-testid="confirm-policy">{{ t('banquet.confirm.policyCopied', { base: t(`banquet.policy.base${chosen.base}` as never), deposits: chosen.deposits.length, tiers: chosen.tiers.length }) }}</p>
        <p v-if="behind.length" class="m-0 text-sm text-destructive" data-testid="confirm-blocked">{{ t('banquet.confirm.blocked') }}</p>
        <p v-else class="m-0 text-sm text-muted-foreground" data-testid="confirm-effect">{{ t('banquet.confirm.effect') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="button" :disabled="busy || behind.length > 0" data-testid="confirm-event" @click="confirmEvent">{{ t('banquet.confirm.action') }}</Button>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
