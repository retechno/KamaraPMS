<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<HousekeepingBoardRoom[]>([])
const error = ref<ApiError | null>(null)
const loaded = ref(false)

const canRead = computed(() => auth.can('housekeeping.read', property.currentId) || auth.can('reservation.read', property.currentId))
const floors = computed(() => {
  const by = new Map<string, HousekeepingBoardRoom[]>()
  for (const r of rooms.value) by.set(r.floor ?? '', [...(by.get(r.floor ?? '') ?? []), r])
  return [...by.entries()].sort(([a], [b]) => a.localeCompare(b))
})
const counts = computed(() => ({
  occupied: rooms.value.filter((r) => r.occupancy === 'OCCUPIED').length,
  reserved: rooms.value.filter((r) => r.occupancy === 'RESERVED').length,
  vacant: rooms.value.filter((r) => r.occupancy === 'VACANT').length,
  blocked: rooms.value.filter((r) => r.block).length,
}))

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    rooms.value = data?.data ?? []
    loaded.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => property.currentId, () => {
  rooms.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Room status</h1>
    <button type="button" data-testid="refresh" @click="load">Refresh</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing room status.</p>

  <template v-else-if="loaded">
    <p class="muted" data-testid="counts">
      {{ counts.occupied }} occupied · {{ counts.reserved }} reserved · {{ counts.vacant }} vacant · {{ counts.blocked }} blocked
    </p>
    <section v-for="[floor, list] in floors" :key="floor" class="card">
      <h2>{{ floor ? `Floor ${floor}` : 'Rooms' }}</h2>
      <div class="tiles">
        <div v-for="r in list" :key="r.room_id" class="tile" :class="[r.occupancy.toLowerCase(), { blocked: r.block }]" :data-testid="`tile-${r.room_number}`">
          <strong>{{ r.room_number }}</strong>
          <small>{{ r.room_type_code }}</small>
          <small :data-testid="`occ-${r.room_number}`">{{ r.occupancy }}</small>
          <small>{{ r.status }}</small>
          <small v-if="r.block" class="block-note">{{ r.block.type }}</small>
        </div>
      </div>
    </section>
  </template>
</template>

<style scoped>
.tiles {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.tile {
  width: 92px;
  padding: 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 2px;
  background: var(--surface);
}
.tile.occupied {
  background: #cfe3ff;
}
.tile.reserved {
  background: #fff2cc;
}
.tile.blocked {
  background: #e5e5e5;
}
.block-note {
  font-weight: 600;
}
</style>
