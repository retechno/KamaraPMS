<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, ref, watch } from 'vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import InHouseTable from '@/components/InHouseTable.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useRoomTypes } from '@/composables/useRoomTypes'
import { useStayList } from '@/composables/useStayList'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The departures tab of the front desk: the open stays that leave by a date (the business date by default, so the stays that were due and are still here are in it), with the balance of their
 * folios and what is known before a check-out. The filters are asked of the server; the rows are the in-house read model. The header and the tabs belong to FrontDeskView.
 */
const props = withDefaults(defineProps<{ refresh?: number }>(), { refresh: 0 })
const emit = defineEmits<{ loaded: [count: number, more: boolean] }>()

const property = usePropertyStore()
const { types } = useRoomTypes()
const businessDate = computed(() => property.clock?.business_date ?? '')

// The date is the business date until the person chooses another; "only this date" asks for the stays that leave on it, not by it.
const chosen = ref('')
const exact = ref(false)
const q = ref('')
const typeId = ref('')
const date = computed(() => chosen.value || businessDate.value)
// The input shows the date that is asked for (the business date until another is chosen); choosing the business date again is the default again.
const shownDate = computed({
  get: () => date.value,
  set: (v: string) => {
    chosen.value = v === businessDate.value ? '' : v
  },
})
const isDefault = computed(() => !chosen.value && !exact.value && !q.value.trim() && !typeId.value)

const list = useStayList(
  () => (!date.value ? null : {
    departure_until: date.value && !exact.value ? date.value : undefined,
    departure_date: date.value && exact.value ? date.value : undefined,
    room_type_id: typeId.value ? Number(typeId.value) : undefined,
    q: q.value.trim(),
  }),
  (rows, more) => {
    if (isDefault.value) emit('loaded', rows.length, more) // the count on the tab is the count of the default list
  },
)
const { rows, nextCursor, error, loading, loaded, canRead } = list

function clearFilters(): void {
  chosen.value = ''
  exact.value = false
  q.value = ''
  typeId.value = ''
}

watch(() => props.refresh, () => list.restart())
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="list.restart()">{{ t('frontDesk.page.retry') }}</Button>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessStays') }}</p>

  <template v-else-if="businessDate">
    <form class="mb-3 flex flex-wrap items-end gap-x-3 gap-y-6 pb-5" novalidate data-testid="departure-filters" @submit.prevent>
      <FormField float-hint :hint="$weekday(shownDate)" class="w-44" :label="exact ? t('frontDesk.departures.on') : t('frontDesk.departures.by')">
        <template #default="{ id }"><Input :id="id" v-model="shownDate" name="departure_date" type="date" /></template>
      </FormField>
      <label class="mb-2 flex items-center gap-2 text-sm"><input v-model="exact" type="checkbox" name="exact" class="size-4 accent-primary" />{{ t('frontDesk.departures.onlyThatDate') }}</label>
      <FormField class="w-64" :label="t('frontDesk.page.search')">
        <template #default="{ id }"><Input :id="id" v-model="q" name="q" type="search" :placeholder="t('frontDesk.page.searchPlaceholder')" /></template>
      </FormField>
      <FormField v-if="types.length" class="w-44" :label="t('frontDesk.page.roomType')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="typeId" name="room_type_id">
            <option value="">{{ t('dataTable.all') }}</option>
            <option v-for="rt in types" :key="rt.id" :value="String(rt.id)">{{ rt.code }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <Button v-if="!isDefault" type="button" variant="outline" size="sm" class="mb-1" data-testid="clear-filters" @click="clearFilters">{{ t('dataTable.clearFilters') }}</Button>
    </form>

    <InHouseTable v-if="!error || loaded" mode="departures" :rows="rows" :loaded="loaded" :business-date="businessDate" @guest-saved="list.renameGuest" @rate-saved="list.reloadLoaded()" />
    <EmptyState v-else :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />
    <div v-if="nextCursor" class="flex justify-center p-3">
      <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="list.load(true)">{{ t('frontDesk.page.loadMore') }}</Button>
    </div>
  </template>
</template>
