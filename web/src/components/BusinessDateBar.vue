<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate, wallClock } from '@/utils/dates'

const store = usePropertyStore()

const local = computed(() => (store.clock ? wallClock(store.clock.property_local_time) : null))
const clockTitle = computed(() => {
  const c = store.clock
  if (!c || !local.value) return ''
  return `Property local time ${formatBusinessDate(local.value.date)} ${local.value.time} (${c.timezone}). Server time ${c.server_time}.`
})

function onSelect(event: Event): void {
  const id = Number((event.target as HTMLSelectElement).value)
  if (id) void store.select(id)
}
</script>

<template>
  <div class="bar">
    <template v-if="store.hasProperties">
      <label class="switcher">
        <span class="sr-only">Property</span>
        <select :value="store.currentId ?? ''" data-testid="property-switcher" @change="onSelect">
          <option v-for="p in store.properties" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </select>
      </label>
      <span v-if="store.clock" class="chip" :class="{ overdue: store.clock.night_audit_overdue }" :title="clockTitle" data-testid="business-date">
        Business date <strong>{{ formatBusinessDate(store.clock.business_date) }}</strong>
        <span v-if="local && local.date !== store.clock.business_date" class="local">· local {{ formatBusinessDate(local.date) }} {{ local.time }}</span>
      </span>
    </template>
    <RouterLink v-else-if="store.loaded" to="/setup/properties/new" class="chip">No property yet · create one</RouterLink>
  </div>
</template>

<style scoped>
.bar {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
select {
  font: inherit;
  font-size: 13px;
  padding: 4px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  max-width: 260px;
}
.chip {
  font-size: 13px;
  padding: 4px 10px;
  border-radius: 999px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  color: var(--text);
  text-decoration: none;
}
.chip.overdue {
  border-color: var(--warning);
}
.local {
  color: var(--text-muted);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
</style>
