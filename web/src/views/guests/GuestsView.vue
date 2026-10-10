<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CreateGuestRequest, CreatedGuest, Guest } from '@/api/types'
import GuestFields from '@/components/GuestFields.vue'
import { blankGuestForm } from '@/components/guestForm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const query = ref('')
const results = ref<Guest[]>([])
const nextCursor = ref<string | undefined>()
const searched = ref(false)
const loading = ref(false)
const error = ref<ApiError | null>(null)

const creating = ref(false)
const form = reactive(blankGuestForm())
const saving = ref(false)
const created = ref<CreatedGuest | null>(null)

const columns = computed<Column<Guest>[]>(() => [
  { key: 'code', label: t('guests.code') },
  { key: 'name', label: t('guests.name') },
  { key: 'email', label: t('guests.email') },
  { key: 'phone', label: t('guests.phone') },
  { key: 'nationality', label: t('guests.nationality') },
])

const canRead = computed(() => auth.can('guest.read', property.currentId))
const canWrite = computed(() => auth.can('guest.write', property.currentId))

async function search(more = false): Promise<void> {
  if (property.currentId === null) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/guests', {
      params: { query: { q: query.value, property_id: property.currentId, limit: 50, cursor: more ? nextCursor.value : undefined } },
    })
    results.value = more ? [...results.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

function startCreate(): void {
  Object.assign(form, blankGuestForm())
  created.value = null
  error.value = null
  creating.value = true
}

/** Sends only the fields that were filled in. */
function body(): CreateGuestRequest {
  const b: Record<string, unknown> = { origin_property_id: property.currentId }
  for (const [k, v] of Object.entries(form)) if (v !== '') b[k] = v
  return b as unknown as CreateGuestRequest
}

async function create(): Promise<void> {
  saving.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/guests', { body: body() })
    created.value = data ?? null
    creating.value = false
    await search()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

function fullName(g: Guest): string {
  return [g.first_name, g.last_name].filter(Boolean).join(' ')
}

watch(() => property.currentId, () => {
  results.value = []
  searched.value = false
  created.value = null
  if (property.currentId !== null && canRead.value) void search()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('guests.title')">
    <template #actions>
      <Button v-if="canWrite && !creating" type="button" data-testid="new-guest" @click="startCreate">{{ t('guests.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('guests.noAccess') }}</p>

  <template v-else>
    <div v-if="created" class="alert warning" role="status" data-testid="created">
      {{ t('guests.createdPrefix') }} <RouterLink :to="`/guests/${created.id}`" class="underline">{{ created.code }}</RouterLink>.
      <template v-if="created.possible_duplicates.length || created.hidden_duplicate_count">
        <b>{{ t('guests.maybeDuplicate') }}</b>
        <ul class="m-0 mt-1 pl-5" data-testid="duplicates">
          <li v-for="d in created.possible_duplicates" :key="d.guest.id">
            <RouterLink :to="`/guests/${d.guest.id}`" class="underline">{{ d.guest.code }} {{ fullName(d.guest) }}</RouterLink>
            <small> ({{ d.reasons.map((r) => r.toLowerCase().replaceAll('_', ' ')).join(', ') }})</small>
          </li>
          <li v-if="created.hidden_duplicate_count" data-testid="hidden-duplicates">
            {{ t('guests.hiddenDuplicates', { n: created.hidden_duplicate_count }) }}
          </li>
        </ul>
      </template>
    </div>

    <Card v-if="creating" class="mb-4">
      <form v-autofocus novalidate data-testid="create-form" @submit.prevent="create">
        <CardHeader><CardTitle>{{ t('guests.new') }}</CardTitle></CardHeader>
        <CardContent>
          <GuestFields v-model="form" :error="error" />
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card class="mb-4">
      <form v-autofocus class="flex items-end gap-3 p-4" role="search" @submit.prevent="search()">
        <FormField class="flex-1" :label="t('guests.search')">
          <template #default="{ id }"><Input :id="id" v-model="query" name="q" type="search" :placeholder="t('guests.searchPlaceholder')" /></template>
        </FormField>
        <Button type="submit" variant="outline" :disabled="loading">{{ t('guests.search') }}</Button>
      </form>
    </Card>

    <Card>
      <EmptyState v-if="searched && !results.length" :title="t('guests.empty')" data-testid="empty" />
      <DataTable v-else-if="results.length" :columns="columns" :rows="results" row-key="id" :row-test-id="(g) => `guest-${g.code}`" :caption="t('guests.title')">
        <template #cell-code="{ row }"><RouterLink :to="`/guests/${row.id}`" class="text-primary hover:underline">{{ row.code }}</RouterLink></template>
        <template #cell-name="{ row }">{{ fullName(row) }}</template>
      </DataTable>
      <div v-if="nextCursor" class="flex justify-center p-3">
        <Button type="button" variant="outline" :disabled="loading" data-testid="more" @click="search(true)">{{ t('guests.more') }}</Button>
      </div>
    </Card>
  </template>
</template>
