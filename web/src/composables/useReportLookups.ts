import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Department, InventoryCategory, InventoryItem, InventorySupplier, InventoryWarehouse } from '@/api/types'
import { loadDepartments } from '@/composables/useDepartments'
import { usePropertyStore } from '@/stores/property'

/**
 * The lists a report of the inventory filters by (warehouses, categories, departments, items, suppliers), read once for the property on the page. A list the person may not read stays empty and its select is left out.
 */
export function useReportLookups(want: { warehouses?: boolean; categories?: boolean; departments?: boolean; items?: boolean; suppliers?: boolean }) {
  const property = usePropertyStore()
  const warehouses = ref<InventoryWarehouse[]>([])
  const categories = ref<InventoryCategory[]>([])
  const departments = ref<Department[]>([])
  const items = ref<InventoryItem[]>([])
  const suppliers = ref<InventorySupplier[]>([])

  async function load(): Promise<void> {
    const propertyId = property.currentId
    if (propertyId === null) return
    const path = { params: { path: { propertyId } } }
    await Promise.all([
      want.warehouses
        ? api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path).then((r) => (warehouses.value = r.data?.data ?? [])).catch(() => undefined)
        : undefined,
      want.categories
        ? api.GET('/api/v1/properties/{propertyId}/inventory/categories', path).then((r) => (categories.value = r.data?.data ?? [])).catch(() => undefined)
        : undefined,
      want.departments ? loadDepartments(propertyId).then((d) => (departments.value = d)) : undefined,
      want.items
        ? api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200 } } }).then((r) => (items.value = r.data?.items ?? [])).catch(() => undefined)
        : undefined,
      want.suppliers
        ? api.GET('/api/v1/properties/{propertyId}/inventory/suppliers', { params: { path: { propertyId } } }).then((r) => (suppliers.value = r.data?.data ?? [])).catch(() => undefined)
        : undefined,
    ])
  }
  watch(() => property.currentId, load, { immediate: true })
  return { warehouses, categories, departments, items, suppliers }
}
