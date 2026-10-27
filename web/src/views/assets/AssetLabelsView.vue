<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetLocation } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'
import { documentPath, openPdf } from '@/utils/documents'

/** The QR labels (docs/architecture/37-fixed-assets.md 4.13, mockup 61): which assets or places, the layout, and the PDF. The QR holds the code only (FA: or FL:). */
const route = useRoute()
const { pid, canRead } = useAssetAccess()
const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)

const ids = computed(() => String(route.query.ids ?? '').split(',').filter((x) => /^\d+$/.test(x)))
const what = ref<'selected' | 'all' | 'location' | 'category' | 'places'>('all')
const locationId = ref(0)
const categoryId = ref(0)
const layout = ref<'A4_24' | 'ROLL_50x30'>('A4_24')

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  try {
    const [c, l] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } }),
    ])
    categories.value = c.data?.data ?? []
    locations.value = l.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, load, { immediate: true })
watch(ids, (v) => { what.value = v.length ? 'selected' : 'all' }, { immediate: true })

const ready = computed(() => (what.value === 'selected' ? ids.value.length > 0 : what.value === 'location' ? locationId.value > 0 : what.value === 'category' ? categoryId.value > 0 : true))

async function print(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  const q: Record<string, string | undefined> = { layout: layout.value }
  if (what.value === 'places') q.kind = 'place'
  if (what.value === 'selected') q.ids = ids.value.join(',')
  if (what.value === 'location') q.location_id = String(locationId.value)
  if (what.value === 'category') q.category_id = String(categoryId.value)
  try {
    await openPdf(documentPath.assetLabels(propertyId, q))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <RouterLink to="/assets" class="text-sm text-primary hover:underline">← {{ t('assets.register.title') }}</RouterLink>
  <PageHeader :title="t('assets.labels.title')" :description="t('assets.labels.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <Card v-else class="flex max-w-xl flex-col gap-4 p-4" data-testid="labels-form">
    <FormField :label="t('assets.labels.what')">
      <template #default="{ id }">
        <NativeSelect :id="id" v-model="what" name="what" data-testid="labels-what">
          <option v-if="ids.length" value="selected">{{ t('assets.labels.selected', { n: ids.length }) }}</option>
          <option value="all">{{ t('assets.labels.all') }}</option>
          <option value="location">{{ t('assets.labels.byLocation') }}</option>
          <option value="category">{{ t('assets.labels.byCategory') }}</option>
          <option value="places">{{ t('assets.labels.places') }}</option>
        </NativeSelect>
      </template>
    </FormField>
    <FormField v-if="what === 'location'" :label="t('assets.col.place')">
      <template #default="{ id }"><NativeSelect :id="id" v-model.number="locationId" name="location_id" data-testid="labels-location"><option :value="0">–</option><option v-for="l in locations" :key="l.id" :value="l.id">{{ l.name }}</option></NativeSelect></template>
    </FormField>
    <FormField v-if="what === 'category'" :label="t('assets.col.category')">
      <template #default="{ id }"><NativeSelect :id="id" v-model.number="categoryId" name="category_id" data-testid="labels-category"><option :value="0">–</option><option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }}</option></NativeSelect></template>
    </FormField>
    <fieldset class="m-0 flex flex-col gap-1 border-0 p-0">
      <legend class="mb-1 text-sm font-semibold">{{ t('assets.labels.layout') }}</legend>
      <label class="flex min-h-9 items-center gap-2 text-sm"><input v-model="layout" type="radio" name="layout" value="A4_24" class="accent-primary" />{{ t('assets.labels.a4') }}</label>
      <label class="flex min-h-9 items-center gap-2 text-sm"><input v-model="layout" type="radio" name="layout" value="ROLL_50x30" class="accent-primary" />{{ t('assets.labels.roll') }}</label>
    </fieldset>
    <p class="m-0 text-xs text-muted-foreground">{{ t('assets.labels.qrNote') }}</p>
    <div class="flex justify-end"><Button :disabled="busy || !ready" data-testid="labels-print" @click="print">{{ t('assets.labels.print') }}</Button></div>
  </Card>
</template>
