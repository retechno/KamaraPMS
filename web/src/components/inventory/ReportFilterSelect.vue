<script setup lang="ts">
import FormField from '@/components/app/FormField.vue'
import { NativeSelect } from '@/components/ui/native-select'

/** A select of a report filter: "all" first, then the options. The value is the id as text ('' for all). */
defineProps<{ label: string; allLabel: string; options: { id: number; label: string }[]; name: string }>()
const model = defineModel<string>({ default: '' })
</script>

<template>
  <FormField :label="label">
    <template #default="{ id }">
      <NativeSelect :id="id" v-model="model" :name="name" class="w-48">
        <option value="">{{ allLabel }}</option>
        <option v-for="o in options" :key="o.id" :value="String(o.id)">{{ o.label }}</option>
      </NativeSelect>
    </template>
  </FormField>
</template>
