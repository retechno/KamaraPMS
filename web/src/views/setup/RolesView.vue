<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'

const roles = ref<components['schemas']['Role'][]>([])
const error = ref<ApiError | null>(null)

onMounted(async () => {
  try {
    roles.value = (await api.GET('/api/v1/roles')).data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Roles</h1>
    <RouterLink to="/setup/roles/new" custom v-slot="{ navigate }">
      <button type="button" class="btn-primary" @click="navigate">New role</button>
    </RouterLink>
  </div>
  <p v-if="error" class="alert" role="alert">{{ error.message }}</p>
  <section class="card">
    <p v-if="!roles.length" class="muted">No roles yet. A role is a set of permissions you grant to users per property.</p>
    <table v-else class="list">
      <thead>
        <tr>
          <th>Name</th>
          <th>Description</th>
          <th>Permissions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in roles" :key="r.id">
          <td><RouterLink :to="`/setup/roles/${r.id}`">{{ r.name }}</RouterLink></td>
          <td>{{ r.description }}</td>
          <td>{{ r.permissions.length }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
