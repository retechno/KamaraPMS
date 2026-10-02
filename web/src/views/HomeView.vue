<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { usePropertyStore } from '@/stores/property'
import { useSystemStore, type ComponentStatus } from '@/stores/system'
import { formatBusinessDate, wallClock } from '@/utils/dates'
import ManagerDashboard from '@/views/dashboard/ManagerDashboard.vue'

const system = useSystemStore()
const property = usePropertyStore()
onMounted(() => system.check())

const local = computed(() => (property.clock ? wallClock(property.clock.property_local_time) : null))
const auditFrom = computed(() => {
  const c = property.clock
  if (!c) return ''
  // Shown in the property's zone: the rule is "from HH:MM local on the business date".
  return property.current ? `${property.current.night_audit_earliest_time} on ${formatBusinessDate(c.business_date)}` : ''
})

const label: Record<ComponentStatus, string> = { unknown: 'Unknown', up: 'Operational', down: 'Unavailable' }
</script>

<template>
  <h1>Dashboard</h1>

  <ManagerDashboard />

  <section v-if="property.clock && property.current" class="card" data-testid="business-day-card">
    <h2>Business day · {{ property.current.name }}</h2>
    <p class="bd">{{ formatBusinessDate(property.clock.business_date) }}</p>
    <dl class="status-list">
      <div class="status-row">
        <dt>Property local time</dt>
        <dd>{{ local ? `${formatBusinessDate(local.date)} ${local.time}` : '—' }} ({{ property.clock.timezone }})</dd>
      </div>
      <div class="status-row">
        <dt>Night audit</dt>
        <dd>{{ property.clock.night_audit_allowed ? 'Can run now' : `Allowed from ${auditFrom}` }}</dd>
      </div>
    </dl>
  </section>
  <section v-else-if="property.loaded && !property.hasProperties" class="card">
    <h2>Set up your first property</h2>
    <p class="muted">A property holds its own time zone, currency and business date.</p>
    <RouterLink to="/setup/properties/new">Create a property</RouterLink>
  </section>

  <section class="card" aria-labelledby="status-title">
    <div class="card-head">
      <h2 id="status-title">System status</h2>
      <button type="button" :disabled="system.checking" @click="system.check()">
        {{ system.checking ? 'Checking…' : 'Refresh' }}
      </button>
    </div>
    <dl class="status-list">
      <div class="status-row">
        <dt>API</dt>
        <dd :class="`status status-${system.apiStatus}`" data-testid="api-status">{{ label[system.apiStatus] }}</dd>
      </div>
      <div class="status-row">
        <dt>Database</dt>
        <dd :class="`status status-${system.databaseStatus}`" data-testid="db-status">
          {{ label[system.databaseStatus] }}
        </dd>
      </div>
    </dl>
    <p v-if="system.lastError" class="error" role="alert">
      {{ system.lastError.message }}
      <code>{{ system.lastError.code }}</code>
      <span v-if="system.lastError.requestId"> · request {{ system.lastError.requestId }}</span>
    </p>
    <p v-if="system.checkedAt" class="muted">Last checked {{ system.checkedAt.toLocaleTimeString() }}</p>
  </section>

  <section class="card">
    <h2>Getting started</h2>
    <p class="muted">
      The foundation (M0) is in place. Property setup and the business date arrive with milestone M1; front-office
      features follow milestone by milestone.
    </p>
  </section>
</template>

<style scoped>
h1 {
  margin: 0 0 20px;
  font-size: 24px;
  letter-spacing: -0.02em;
}
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 20px;
  margin-bottom: 16px;
}
.card h2 {
  margin: 0 0 12px;
  font-size: 16px;
}
.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.card-head h2 {
  margin: 0;
}
.status-list {
  margin: 16px 0 0;
}
.status-row {
  display: flex;
  justify-content: space-between;
  padding: 10px 0;
  border-top: 1px solid var(--border);
}
dt {
  font-weight: 500;
}
dd {
  margin: 0;
}
.status::before {
  content: '';
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 8px;
  vertical-align: middle;
  background: var(--text-muted);
}
.status-up::before {
  background: var(--ok);
}
.status-down::before {
  background: var(--danger);
}
.bd {
  font-size: 28px;
  font-weight: 650;
  letter-spacing: -0.02em;
  margin: 0;
}
.error {
  margin: 12px 0 0;
  color: var(--danger);
}
.error code {
  font-size: 12px;
  margin-left: 6px;
}
.muted {
  color: var(--text-muted);
  font-size: 14px;
  margin: 12px 0 0;
}
</style>
