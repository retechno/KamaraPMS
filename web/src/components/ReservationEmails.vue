<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ReservationEmails } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const props = defineProps<{ reservationId: number; confirmed: boolean }>()
const auth = useAuthStore()
const property = usePropertyStore()

const state = ref<ReservationEmails | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)

const pid = computed(() => property.currentId)
const canResend = computed(() => props.confirmed && !!state.value?.enabled && auth.can('reservation.update', pid.value))
const waiting = computed(() => state.value?.data.some((e) => e.status === 'QUEUED') ?? false)
const label = (s: string): string => (['QUEUED', 'SENT', 'FAILED', 'SKIPPED'].includes(s) ? t(`emails.${s}` as 'emails.SENT') : s)
const variant = (s: string) => ({ QUEUED: 'warning', SENT: 'success', FAILED: 'destructive', SKIPPED: 'outline' })[s] as 'warning' | 'success' | 'destructive' | 'outline'

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId, id: props.reservationId } } })
    state.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function resend(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId, id: props.reservationId } } })
    notice.value = t('emails.queuedNotice')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  await load()
}

watch(() => [pid.value, props.reservationId], () => void load(), { immediate: true })
</script>

<template>
  <Card v-if="state" class="mb-4" data-testid="emails">
    <CardHeader class="flex-row items-center justify-between">
      <CardTitle>{{ t('emails.title') }}</CardTitle>
      <Button type="button" variant="outline" size="sm" data-testid="emails-refresh" @click="load">{{ t('emails.refresh') }}</Button>
    </CardHeader>
    <CardContent>
      <p v-if="error" class="alert" role="alert" data-testid="emails-error">{{ error.message }} <code>{{ error.code }}</code></p>
      <p v-if="notice" class="alert warning" role="status" data-testid="emails-notice">{{ notice }}</p>
      <p v-if="!state.enabled" class="m-0 text-sm text-muted-foreground" data-testid="emails-off">{{ t('emails.off') }}</p>
      <p v-else-if="!state.data.length" class="m-0 text-sm text-muted-foreground" data-testid="emails-none">{{ t('emails.none') }}</p>
      <ul v-else class="m-0 grid list-none gap-1.5 p-0 text-sm">
        <li v-for="e in state.data" :key="e.id" :data-testid="`email-${e.id}`">
          <Badge :variant="variant(e.status)">{{ label(e.status) }}</Badge>
          {{ $date(e.to) }}
          <span class="text-muted-foreground"> · {{ e.sent_at ?? e.created_at }}<template v-if="e.attempts > 1"> · {{ t('emails.attempts', { n: e.attempts }) }}</template></span>
          <small v-if="e.last_error" class="text-destructive"> {{ e.last_error }}</small>
        </li>
      </ul>
      <div v-if="canResend" class="mt-3 flex justify-end">
        <Button type="button" variant="outline" :disabled="busy || waiting" data-testid="resend" @click="resend">{{ state.data.length ? t('emails.sendAgain') : t('emails.send') }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
