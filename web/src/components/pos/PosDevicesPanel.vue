<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosDevice, PosDeviceList, PosEnrollment } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { confirm } from '@/composables/useConfirm'
import { t } from '@/i18n'
import { formatDateTime } from '@/utils/format'

/**
 * The print agents of the property (docs/architecture/34-pos.md 10.3 and 20): the one-time code that enrols an agent (shown once, 15 minutes, with the command to copy), the agents with their state, and
 * revoking or replacing the token of one. The token of a device is shown once, when it is replaced.
 */
const props = defineProps<{ propertyId: number }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const list = ref<PosDeviceList | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const notice = ref('')

async function load(): Promise<void> {
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/devices', { params: { path: { propertyId: props.propertyId } } })
    list.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(() => props.propertyId, () => void load(), { immediate: true })
const refresh = debounce(() => void load(), 250)
usePosEvents({ propertyId: () => props.propertyId, channels: () => ['printers'], onEvent: () => refresh(), onReload: () => void load() })

// ---- the code of an enrolment: shown once, with a countdown and the command an agent is started with
const enrollment = ref<PosEnrollment | null>(null)
const enrollmentOpen = ref(false)
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null
function startClock(): void {
  stopClock()
  now.value = Date.now()
  timer = setInterval(() => (now.value = Date.now()), 1000)
}
function stopClock(): void {
  if (timer !== null) clearInterval(timer)
  timer = null
}
onBeforeUnmount(stopClock)
const secondsLeft = computed(() => (enrollment.value ? Math.max(0, Math.floor((new Date(enrollment.value.expires_at).getTime() - now.value) / 1000)) : 0))
const countdown = computed(() => `${String(Math.floor(secondsLeft.value / 60)).padStart(2, '0')}:${String(secondsLeft.value % 60).padStart(2, '0')}`)
const command = computed(() => `pos-agent enroll -server ${window.location.origin} -code ${enrollment.value?.code ?? ''} -name "Kasir Lobby"`)

async function createEnrollment(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/devices/enrollments', { params: { path: { propertyId: props.propertyId } } })
    enrollment.value = data ?? null
    enrollmentOpen.value = true
    startClock()
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
function closeEnrollment(): void {
  enrollmentOpen.value = false
  enrollment.value = null // it is shown once
  stopClock()
}

const copied = ref('')
async function copy(text: string, what: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = what
  } catch {
    copied.value = ''
  }
}

// ---- revoke and replace the token
const newToken = ref<{ name: string; token: string; command: string } | null>(null)
async function revoke(d: PosDevice): Promise<void> {
  if (!(await confirm({ title: t('posDevices.revokeTitle', { name: d.name }), description: t('posDevices.revokeHint'), confirmLabel: t('posDevices.revoke'), destructive: true }))) return
  await act(async () => {
    await api.POST('/api/v1/properties/{propertyId}/pos/devices/{id}/revoke', { params: { path: { propertyId: props.propertyId, id: d.id } } })
    notice.value = t('posDevices.revoked', { name: d.name })
  })
}
async function rotate(d: PosDevice): Promise<void> {
  if (!(await confirm({ title: t('posDevices.rotateTitle', { name: d.name }), description: t('posDevices.rotateHint'), confirmLabel: t('posDevices.rotate') }))) return
  await act(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/devices/{id}/rotate-token', { params: { path: { propertyId: props.propertyId, id: d.id } } })
    if (data) newToken.value = { name: d.name, token: data.token, command: '' }
  })
}
async function act(fn: () => Promise<void>): Promise<void> {
  busy.value = true
  error.value = null
  try {
    await fn()
    await load()
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const state = (d: PosDevice): 'online' | 'offline' | 'revoked' | 'never' => (d.status === 'REVOKED' ? 'revoked' : d.online ? 'online' : d.last_seen_at ? 'offline' : 'never')
</script>

<template>
  <section class="rounded-xl border border-border bg-card p-4" data-testid="devices">
    <header class="mb-1 flex flex-wrap items-center justify-between gap-2">
      <h2 class="m-0 text-base font-semibold">{{ t('posDevices.title') }}</h2>
      <Button size="sm" :disabled="busy" data-testid="enrollment-new" @click="createEnrollment">{{ t('posDevices.newCode') }}</Button>
    </header>
    <p class="m-0 mb-3 text-xs text-muted-foreground">{{ t('posDevices.intro') }}</p>
    <ErrorNotice v-if="error" :error="error" inline />
    <p v-if="notice" class="notice" role="status" data-testid="devices-notice">{{ notice }}</p>
    <p v-if="list && list.pending_enrollments > 0" class="m-0 mb-2 text-xs text-muted-foreground" data-testid="pending-codes">{{ t('posDevices.pending', { n: list.pending_enrollments }) }}</p>

    <div v-if="!list || list.data.length === 0" class="rounded-lg border border-dashed border-border p-5 text-center" data-testid="devices-empty">
      <p class="m-0 text-sm font-medium">{{ t('posDevices.empty') }}</p>
      <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('posDevices.emptyHint') }}</p>
    </div>
    <ul v-else class="m-0 list-none divide-y divide-border p-0">
      <li v-for="d in list.data" :key="d.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2" :data-testid="`device-${d.id}`" :data-state="state(d)">
        <div class="min-w-0 flex-1">
          <strong class="block truncate text-sm">{{ d.name }}</strong>
          <span class="text-xs text-muted-foreground">
            {{ d.last_seen_at ? t('posDevices.lastSeen', { at: formatDateTime(d.last_seen_at) }) : t('posDevices.neverSeen') }}
            · {{ d.agent_version ? t('posDevices.version', { v: d.agent_version }) : t('posDevices.noVersion') }}
            · {{ t('posDevices.printers', { n: d.printers }) }}
          </span>
        </div>
        <Badge :variant="state(d) === 'online' ? 'success' : state(d) === 'revoked' ? 'destructive' : 'outline'" data-testid="device-state">{{ t(`posDevices.state_${state(d)}`) }}</Badge>
        <template v-if="d.status === 'ACTIVE'">
          <Button variant="outline" size="sm" :disabled="busy" :data-testid="`device-rotate-${d.id}`" @click="rotate(d)">{{ t('posDevices.rotate') }}</Button>
          <Button variant="outline" size="sm" :disabled="busy" :data-testid="`device-revoke-${d.id}`" @click="revoke(d)">{{ t('posDevices.revoke') }}</Button>
        </template>
      </li>
    </ul>
  </section>

  <Dialog :open="enrollmentOpen" @update:open="(v) => { if (!v) closeEnrollment() }">
    <DialogContent class="max-w-lg p-5" data-testid="enrollment-dialog" @interact-outside.prevent>
      <DialogTitle>{{ t('posDevices.codeTitle') }}</DialogTitle>
      <DialogDescription class="mt-1">{{ t('posDevices.codeHint') }}</DialogDescription>
      <div v-if="enrollment" class="mt-3 flex flex-col gap-3">
        <div class="rounded-lg border border-border bg-muted/40 p-4 text-center">
          <div class="font-mono text-3xl font-bold tracking-widest" data-testid="enrollment-code">{{ enrollment.code }}</div>
          <div class="mt-1 text-sm tabular-nums" :class="secondsLeft === 0 ? 'text-destructive' : 'text-muted-foreground'" data-testid="enrollment-countdown">
            {{ secondsLeft === 0 ? t('posDevices.expired') : t('posDevices.expiresIn', { time: countdown }) }}
          </div>
        </div>
        <div>
          <div class="mb-1 text-xs font-medium text-muted-foreground">{{ t('posDevices.command') }}</div>
          <pre class="m-0 overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 text-xs" data-testid="enrollment-command">{{ command }}</pre>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('posDevices.commandHint') }}</p>
        </div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="enrollment-copy" @click="copy(command, 'command')">{{ copied === 'command' ? t('posDevices.copied') : t('posDevices.copy') }}</Button>
          <Button type="button" data-testid="enrollment-close" @click="closeEnrollment">{{ t('common.close') }}</Button>
        </div>
      </div>
    </DialogContent>
  </Dialog>

  <Dialog :open="newToken !== null" @update:open="(v) => { if (!v) newToken = null }">
    <DialogContent class="max-w-lg p-5" data-testid="token-dialog" @interact-outside.prevent>
      <DialogTitle>{{ t('posDevices.tokenTitle', { name: newToken?.name ?? '' }) }}</DialogTitle>
      <DialogDescription class="mt-1">{{ t('posDevices.tokenHint') }}</DialogDescription>
      <pre v-if="newToken" class="m-0 mt-3 overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 text-xs" data-testid="token-value">{{ newToken.token }}</pre>
      <div v-if="newToken" class="mt-3 flex justify-end gap-2">
        <Button type="button" variant="outline" data-testid="token-copy" @click="copy(newToken.token, 'token')">{{ copied === 'token' ? t('posDevices.copied') : t('posDevices.copy') }}</Button>
        <Button type="button" @click="newToken = null">{{ t('common.close') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
