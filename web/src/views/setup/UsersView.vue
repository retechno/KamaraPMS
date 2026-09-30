<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'

type User = components['schemas']['User']

const users = ref<User[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

onMounted(async () => {
  try {
    const { data } = await api.GET('/api/v1/users', { params: { query: { limit: 200 } } })
    users.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
})

function access(u: User): string {
  if (u.is_tenant_admin) return 'All properties (tenant administrator)'
  if (!u.grants.length) return 'No property access'
  return u.grants.map((g) => `${g.property_code}: ${g.role_name}`).join(', ')
}
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Users</h1>
    <RouterLink to="/setup/users/new" custom v-slot="{ navigate }">
      <button type="button" class="btn-primary" @click="navigate">New user</button>
    </RouterLink>
  </div>
  <p v-if="error" class="alert" role="alert">{{ error.message }}</p>
  <section class="card">
    <table v-if="loaded" class="list">
      <thead>
        <tr>
          <th>Name</th>
          <th>Email</th>
          <th>Access</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td><RouterLink :to="`/setup/users/${u.id}`">{{ u.full_name }}</RouterLink></td>
          <td>{{ u.email }}</td>
          <td>{{ access(u) }}</td>
          <td>{{ u.is_active ? 'Active' : 'Inactive' }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
