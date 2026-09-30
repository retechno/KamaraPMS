import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import HomeView from '@/views/HomeView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    public?: boolean // reachable without signing in
    adminOnly?: boolean // tenant administrators only
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { title: 'Sign in', public: true } },
    { path: '/', name: 'home', component: HomeView, meta: { title: 'Dashboard' } },
    {
      path: '/housekeeping',
      name: 'housekeeping',
      component: () => import('@/views/rooms/HousekeepingView.vue'),
      meta: { title: 'Housekeeping' },
    },
    {
      path: '/room-blocks',
      name: 'room-blocks',
      component: () => import('@/views/rooms/RoomBlocksView.vue'),
      meta: { title: 'Room blocks' },
    },
    {
      path: '/setup/room-types',
      name: 'room-types',
      component: () => import('@/views/rooms/RoomTypesView.vue'),
      meta: { title: 'Room types' },
    },
    { path: '/setup/rooms', name: 'rooms', component: () => import('@/views/rooms/RoomsView.vue'), meta: { title: 'Rooms' } },
    { path: '/guests', name: 'guests', component: () => import('@/views/guests/GuestsView.vue'), meta: { title: 'Guests' } },
    {
      path: '/guests/:id(\\d+)',
      name: 'guest',
      component: () => import('@/views/guests/GuestDetailView.vue'),
      props: true,
      meta: { title: 'Guest' },
    },
    { path: '/account', name: 'account', component: () => import('@/views/AccountView.vue'), meta: { title: 'Account' } },
    {
      path: '/setup/properties',
      name: 'properties',
      component: () => import('@/views/setup/PropertiesView.vue'),
      meta: { title: 'Properties', adminOnly: true },
    },
    {
      path: '/setup/properties/new',
      name: 'property-new',
      component: () => import('@/views/setup/PropertyFormView.vue'),
      meta: { title: 'New property', adminOnly: true },
    },
    {
      path: '/setup/properties/:id(\\d+)',
      name: 'property-edit',
      component: () => import('@/views/setup/PropertyFormView.vue'),
      props: true,
      meta: { title: 'Property' },
    },
    { path: '/setup/users', name: 'users', component: () => import('@/views/setup/UsersView.vue'), meta: { title: 'Users', adminOnly: true } },
    {
      path: '/setup/users/new',
      name: 'user-new',
      component: () => import('@/views/setup/UserFormView.vue'),
      meta: { title: 'New user', adminOnly: true },
    },
    {
      path: '/setup/users/:id(\\d+)',
      name: 'user-edit',
      component: () => import('@/views/setup/UserFormView.vue'),
      props: true,
      meta: { title: 'User', adminOnly: true },
    },
    { path: '/setup/roles', name: 'roles', component: () => import('@/views/setup/RolesView.vue'), meta: { title: 'Roles', adminOnly: true } },
    {
      path: '/setup/roles/new',
      name: 'role-new',
      component: () => import('@/views/setup/RoleFormView.vue'),
      meta: { title: 'New role', adminOnly: true },
    },
    {
      path: '/setup/roles/:id(\\d+)',
      name: 'role-edit',
      component: () => import('@/views/setup/RoleFormView.vue'),
      props: true,
      meta: { title: 'Role', adminOnly: true },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { title: 'Not found' },
    },
  ],
})

// Every page needs a signed-in user except those marked public. The UI check is a
// convenience: the API enforces the same rules on every request.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.init()
  if (to.meta.public) {
    return auth.isAuthenticated && to.name === 'login' ? { name: 'home' } : true
  }
  if (!auth.isAuthenticated) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (to.meta.adminOnly && !auth.isAdmin) return { name: 'home' }
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · KamaraPMS` : 'KamaraPMS'
})
