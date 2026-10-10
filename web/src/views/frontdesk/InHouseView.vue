<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, watch } from 'vue'
import EmptyState from '@/components/app/EmptyState.vue'
import InHouseTable from '@/components/InHouseTable.vue'
import { Button } from '@/components/ui/button'
import { useStayList } from '@/composables/useStayList'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/** The in-house tab of the front desk: every open stay, with what the desk needs to see without opening it. The page header and the tabs belong to FrontDeskView. */
const props = withDefaults(defineProps<{ refresh?: number }>(), { refresh: 0 })
const emit = defineEmits<{ loaded: [count: number, more: boolean] }>()

const property = usePropertyStore()
const list = useStayList(() => ({}), (rows, more) => emit('loaded', rows.length, more))
const { rows, nextCursor, error, loading, loaded, canRead } = list
const businessDate = computed(() => property.clock?.business_date ?? '')

watch(() => props.refresh, () => list.restart())
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="list.restart()">{{ t('frontDesk.page.retry') }}</Button>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessStays') }}</p>

  <template v-else>
    <InHouseTable v-if="!error || loaded" :rows="rows" :loaded="loaded" :business-date="businessDate" @guest-saved="list.renameGuest" @rate-saved="list.reloadLoaded()" />
    <EmptyState v-else :description="t('emptyState.loadFailed')" :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />
    <div v-if="nextCursor" class="flex justify-center p-3">
      <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="list.load(true)">{{ t('frontDesk.page.loadMore') }}</Button>
    </div>
  </template>
</template>
