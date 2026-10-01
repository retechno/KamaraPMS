<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ReservationEmails } from '@/api/types'
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
const label: Record<string, string> = { QUEUED: 'Waiting to be sent', SENT: 'Sent', FAILED: 'Not delivered', SKIPPED: 'Not sent' }

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
    notice.value = 'The confirmation is queued and will be sent in a moment.'
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
  <section v-if="state" class="card" data-testid="emails">
    <div class="head">
      <h2>E-mail</h2>
      <button type="button" data-testid="emails-refresh" @click="load">Refresh</button>
    </div>
    <p v-if="error" class="alert" role="alert" data-testid="emails-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <p v-if="notice" class="alert warning" role="status" data-testid="emails-notice">{{ notice }}</p>
    <p v-if="!state.enabled" class="muted" data-testid="emails-off">E-mail is not set up on this server (an administrator sets PMS_SMTP_HOST), so no confirmation is sent.</p>
    <p v-else-if="!state.data.length" class="muted" data-testid="emails-none">No e-mail was sent for this reservation. A confirmation goes out when the reservation is confirmed and the booker has an e-mail address.</p>
    <ul v-else class="list">
      <li v-for="e in state.data" :key="e.id" :data-testid="`email-${e.id}`">
        <span class="badge" :class="e.status.toLowerCase()">{{ label[e.status] ?? e.status }}</span>
        {{ e.to }}
        <span class="muted"> · {{ e.sent_at ?? e.created_at }}<template v-if="e.attempts > 1"> · {{ e.attempts }} attempts</template></span>
        <small v-if="e.last_error" class="error-text"> {{ e.last_error }}</small>
      </li>
    </ul>
    <div v-if="canResend" class="form-actions">
      <button type="button" :disabled="busy || waiting" data-testid="resend" @click="resend">{{ state.data.length ? 'Send again' : 'Send confirmation e-mail' }}</button>
    </div>
  </section>
</template>

<style scoped>
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.list {
  list-style: none;
  padding: 0;
  display: grid;
  gap: 6px;
}
.badge {
  border-radius: 999px;
  padding: 1px 8px;
  font-size: 12px;
  background: var(--accent-soft);
}
</style>
