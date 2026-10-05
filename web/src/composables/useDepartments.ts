import { api } from '@/api/client'
import type { Department } from '@/api/types'

/**
 * The departments of a property for the screens that name one (a journal line, a bill line, a charge code, a budget row). They are read once per
 * property and kept: a screen that changes them calls `resetDepartments`. A person who may not read them (accounting.view) gets none, and the
 * select is not shown.
 */
const cache = new Map<number, Promise<Department[]>>()

export function loadDepartments(propertyId: number): Promise<Department[]> {
  let hit = cache.get(propertyId)
  if (!hit) {
    hit = (async () => {
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/departments', { params: { path: { propertyId } } })
        return Array.isArray(data?.data) ? data.data : []
      } catch {
        cache.delete(propertyId) // try again next time
        return []
      }
    })()
    cache.set(propertyId, hit)
  }
  return hit
}

export function resetDepartments(): void {
  cache.clear()
}

/** "FB · Food and beverage", and a sub-department under its department with a dash before it. */
export function departmentLabel(d: Pick<Department, 'code' | 'name' | 'level'>): string {
  return `${d.level === 2 ? '– ' : ''}${d.code} · ${d.name}`
}
