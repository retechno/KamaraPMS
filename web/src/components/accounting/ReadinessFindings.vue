<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type ReadinessFinding, readinessFix, readinessText } from '@/utils/readiness'

/**
 * The findings of the accounting go-live check, in the words of the page: the readiness page and the accounting card of the night audit show the same list.
 * Each finding links to the screen that fixes it when the user may open that screen; otherwise it asks for an accountant. The three findings that only an incomplete
 * restore of the data leaves (no settings, a start date after the business date, no journal numbering) have no screen: they point to the support team.
 */
const props = defineProps<{ items: ReadinessFinding[]; severity?: 'blocker' | 'warning' }>()

const auth = useAuthStore()
const property = usePropertyStore()

const rows = computed(() => props.items.map((f) => {
  const fix = readinessFix(f.code)
  const link = fix !== undefined && fix !== 'SUPPORT' && auth.can(fix.permission, property.currentId) ? fix : null
  return { f, text: readinessText(f), support: fix === 'SUPPORT', link }
}))
</script>

<template>
  <ul class="m-0 mt-1 pl-5" :data-severity="severity ?? 'blocker'">
    <li v-for="r in rows" :key="`${r.f.code}-${r.f.ref ?? ''}`" :data-testid="`readiness-${r.f.code}`">
      {{ r.text }}
      <template v-if="r.support"> {{ t('readiness.support') }}</template>
      <RouterLink v-else-if="r.link" :to="r.link.to">{{ t(r.link.label as never) }}</RouterLink>
      <template v-else> {{ t('readiness.askAccountant') }}</template>
    </li>
  </ul>
</template>
