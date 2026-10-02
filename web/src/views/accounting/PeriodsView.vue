<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlPeriod } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const periods = ref<GlPeriod[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const reopening = ref<{ start: string; reason: string } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const monthLabel = (start: string): string => new Date(`${start}T00:00:00Z`).toLocaleDateString('en-GB', { month: 'long', year: 'numeric', timeZone: 'UTC' })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/periods', { params: { path: { propertyId } } })
    periods.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function close(p: GlPeriod): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !(await confirm({ title: `Close ${monthLabel(p.period_start)}?`, description: 'No journal can be posted into it afterwards.', destructive: true }))) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/accounting/periods/{start}/close', { params: { path: { propertyId, start: p.period_start } } })
    notice.value = `${monthLabel(p.period_start)} is closed.`
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reopen(): Promise<void> {
  const propertyId = pid.value
  const r = reopening.value
  if (propertyId === null || r === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/accounting/periods/{start}/reopen', { params: { path: { propertyId, start: r.start } }, body: { reason: r.reason.trim() } })
    notice.value = `${monthLabel(r.start)} is open again.`
    reopening.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  periods.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Accounting periods</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="period-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see accounting periods: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <p class="muted">A month can be closed once it has ended and every business day of it is closed with its journal. A closed month takes no journals; the latest closed month can be reopened.</p>
    <form v-if="reopening" class="card" novalidate data-testid="reopen-form" @submit.prevent="reopen">
      <h2>Reopen {{ monthLabel(reopening.start) }}</h2>
      <label class="field">
        <span>Reason</span>
        <input v-model="reopening.reason" name="reason" maxlength="500" />
      </label>
      <div class="form-actions">
        <button type="button" @click="reopening = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !reopening.reason.trim()" data-testid="reopen-run">Reopen</button>
      </div>
    </form>
    <section class="card">
      <p v-if="loaded && !periods.length" class="muted" data-testid="empty">No period yet.</p>
      <table v-else class="list" data-testid="periods">
        <thead><tr><th>Month</th><th>Status</th><th>Days journaled</th><th /></tr></thead>
        <tbody>
          <tr v-for="p in periods" :key="p.period_start" :data-testid="`period-${p.period_start}`">
            <td>{{ monthLabel(p.period_start) }}</td>
            <td>{{ p.status === 'CLOSED' ? 'Closed' : 'Open' }}<small v-if="p.reopen_reason && p.status === 'OPEN'" class="muted"> · reopened: {{ p.reopen_reason }}</small></td>
            <td>{{ p.posted_days }} of {{ p.days }}</td>
            <td class="row-actions">
              <button v-if="can('accounting.close') && p.closable" type="button" class="btn-primary" :disabled="busy" :data-testid="`close-${p.period_start}`" @click="close(p)">Close</button>
              <button v-if="can('accounting.close') && p.reopenable" type="button" :data-testid="`reopen-${p.period_start}`" @click="reopening = { start: p.period_start, reason: '' }">Reopen</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>
