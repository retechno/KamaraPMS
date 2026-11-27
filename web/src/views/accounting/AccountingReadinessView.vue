<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AccountingReadiness } from '@/api/types'
import ReadinessFindings from '@/components/accounting/ReadinessFindings.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * Accounting readiness: the go-live check that the night audit makes before it closes a day (GET .../accounting/readiness, accounting.view). It writes nothing:
 * each finding links to the screen that fixes it. Item 10 of PR C2.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const readiness = ref<AccountingReadiness | null>(null)
const error = ref<ApiError | null>(null)
const loading = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/readiness', { params: { path: { propertyId } } })
    readiness.value = data ?? null
  } catch (e) {
    readiness.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('readiness.title')" :description="t('readiness.intro')">
    <template #actions>
      <Button type="button" variant="outline" :disabled="loading" data-testid="readiness-reload" @click="load">{{ t('common.refresh') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="readiness-error" />
  <template v-if="readiness">
    <p class="m-0 mb-4" data-testid="readiness-status">
      <Badge :variant="readiness.ready ? 'success' : 'destructive'">{{ readiness.ready ? t('readiness.ready') : t('readiness.notReady') }}</Badge>
    </p>
    <Card class="mb-4" data-testid="readiness-blockers">
      <CardHeader><CardTitle>{{ t('readiness.blockersTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="readiness.blockers.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="readiness-ok">{{ t('readiness.none') }}</p>
        <ReadinessFindings v-else :items="readiness.blockers" />
      </CardContent>
    </Card>
    <Card v-if="readiness.warnings.length" data-testid="readiness-warnings">
      <CardHeader><CardTitle>{{ t('readiness.warningsTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="m-0 text-sm text-muted-foreground">{{ t('readiness.warningsIntro') }}</p>
        <ReadinessFindings :items="readiness.warnings" severity="warning" />
      </CardContent>
    </Card>
  </template>
</template>
