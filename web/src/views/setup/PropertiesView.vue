<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { usePropertyStore } from '@/stores/property'

const store = usePropertyStore()
onMounted(() => {
  if (!store.loaded) void store.loadProperties()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Properties</h1>
    <RouterLink to="/setup/properties/new" custom v-slot="{ navigate }">
      <button type="button" class="btn-primary" @click="navigate">New property</button>
    </RouterLink>
  </div>

  <p v-if="store.error" class="alert" role="alert">{{ store.error.message }} <code>{{ store.error.code }}</code></p>

  <section class="card">
    <p v-if="store.loaded && !store.hasProperties" class="muted" data-testid="empty">
      No properties yet. Create the first one to open its business day.
    </p>
    <table v-else class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>City</th>
          <th>Time zone</th>
          <th>Currency</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in store.properties" :key="p.id">
          <td><RouterLink :to="`/setup/properties/${p.id}`">{{ p.code }}</RouterLink></td>
          <td>{{ p.name }}</td>
          <td>{{ p.city }}</td>
          <td>{{ p.timezone }}</td>
          <td>{{ p.currency_code }} ({{ p.currency_decimals }} dp)</td>
          <td>{{ p.status }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
