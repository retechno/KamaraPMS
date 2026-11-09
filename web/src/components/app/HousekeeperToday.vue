<script setup lang="ts">
/**
 * The work card of Today for a person who cleans: no reading of reservations, but the right to update rooms. It says how many rooms are dirty and being cleaned (the board of housekeeping is
 * open to every member of the property) and leads to the three places that matter: the dirty rooms, "My tasks" and the list of cleaning tasks.
 */
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

const property = usePropertyStore()
const dirty = ref<number | null>(null)
const cleaning = ref(0)
const failed = ref(false)
const pid = computed(() => property.currentId)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  failed.value = false
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    const rows = data?.data ?? []
    dirty.value = rows.filter((r) => r.status === 'DIRTY').length
    cleaning.value = rows.filter((r) => r.status === 'CLEANING').length
  } catch {
    failed.value = true
  }
}
watch(pid, () => void load(), { immediate: true })
</script>

<template>
  <Card class="mb-4" data-testid="housekeeper-today">
    <CardHeader><CardTitle>{{ t('today.hkTitle') }}</CardTitle></CardHeader>
    <CardContent>
      <p v-if="failed" class="m-0 text-sm text-muted-foreground" data-testid="hk-today-failed">{{ t('today.hkFailed') }}</p>
      <p v-else-if="dirty !== null" class="m-0" data-testid="hk-today-counts">{{ t('today.hkCounts', { dirty, cleaning }) }}</p>
      <div class="mt-3 flex flex-wrap gap-2">
        <Button as-child size="sm" data-testid="hk-today-dirty"><RouterLink :to="{ path: '/housekeeping', query: { status: 'DIRTY' } }">{{ t('today.hkDirty') }}</RouterLink></Button>
        <Button as-child size="sm" variant="outline" data-testid="hk-today-my"><RouterLink to="/housekeeping/my">{{ t('nav.items.myTasks') }}</RouterLink></Button>
        <Button as-child size="sm" variant="outline" data-testid="hk-today-tasks"><RouterLink to="/housekeeping/tasks">{{ t('nav.items.cleaningList') }}</RouterLink></Button>
      </div>
    </CardContent>
  </Card>
</template>
