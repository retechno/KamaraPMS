<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

/** The actions in the header of a report of the day: back to the reports, the PDF and the CSV of what is on the screen. */
defineProps<{ ready: boolean; busy?: boolean }>()
defineEmits<{ csv: []; pdf: [] }>()
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 print:hidden">
    <Button as-child variant="outline" size="sm"><RouterLink to="/reports" data-testid="back">{{ t('dayReports.back') }}</RouterLink></Button>
    <Button variant="outline" size="sm" data-testid="pdf" :disabled="!ready || busy" @click="$emit('pdf')">{{ t('dayReports.pdf') }}</Button>
    <Button variant="outline" size="sm" data-testid="csv" :disabled="!ready || busy" @click="$emit('csv')">{{ t('dayReports.csv') }}</Button>
  </div>
</template>
