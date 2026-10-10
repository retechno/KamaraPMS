<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

type Kind = 'COMPLIMENTARY' | 'HOUSE_USE'
const KINDS: Kind[] = ['COMPLIMENTARY', 'HOUSE_USE']

const auth = useAuthStore()
const property = usePropertyStore()

// What is typed for each kind: empty means no limit.
const nights = reactive<Record<Kind, string>>({ COMPLIMENTARY: '', HOUSE_USE: '' })
const saved = reactive<Record<Kind, string>>({ COMPLIMENTARY: '', HOUSE_USE: '' })
const loaded = ref(false)
const saving = ref<Kind | null>(null)
const notice = ref('')
const error = ref<ApiError | null>(null)

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const changed = (k: Kind): boolean => nights[k].trim() !== saved[k]

async function load(): Promise<void> {
  const propertyId = property.currentId
  loaded.value = false
  for (const k of KINDS) nights[k] = saved[k] = ''
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/free-night-quotas', { params: { path: { propertyId } } })
    for (const q of data?.data ?? []) nights[q.occupancy_kind] = saved[q.occupancy_kind] = String(q.monthly_nights)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function save(kind: Kind): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = kind
  error.value = null
  notice.value = ''
  const raw = nights[kind].trim()
  try {
    await api.PUT('/api/v1/properties/{propertyId}/free-night-quotas/{kind}', {
      params: { path: { propertyId, kind } },
      body: { monthly_nights: raw === '' ? null : Math.trunc(Number(raw)) },
    })
    saved[kind] = raw
    notice.value = raw === '' ? t('freeQuotas.removed', { kind: t(`occupancy.kind_${kind}`) }) : t('freeQuotas.saved', { kind: t(`occupancy.kind_${kind}`), n: raw })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = null
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('freeQuotas.title')" />
  <p class="muted">{{ t('freeQuotas.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('freeQuotas.readOnly', { permission: 'rate.manage' }) }}</p>

  <div v-if="property.currentId !== null && loaded" class="grid gap-4 sm:grid-cols-2">
    <Card v-for="k in KINDS" :key="k" :data-testid="`quota-${k}`">
      <form novalidate @submit.prevent="save(k)">
        <CardHeader><CardTitle>{{ t(`occupancy.kind_${k}`) }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-3">
          <FormField :label="t('freeQuotas.monthly')" :error="error?.fieldMessage('monthly_nights')">
            <template #default="{ id, invalid }">
              <Input :id="id" v-model="nights[k]" :name="`monthly_${k}`" inputmode="numeric" :placeholder="t('freeQuotas.noLimit')" :disabled="!canManage" :aria-invalid="invalid" />
            </template>
          </FormField>
          <small class="text-xs text-muted-foreground">{{ t('freeQuotas.hint') }}</small>
          <div v-if="canManage" class="flex justify-end">
            <Button type="submit" size="sm" :disabled="saving !== null || !changed(k)" :data-testid="`save-${k}`">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
  </div>
</template>
