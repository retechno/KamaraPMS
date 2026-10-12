import { navigation } from '@/navigation'

/** Every permission that a page of the menu asks for: what a person who may see the whole menu holds (the menu is filtered by it). */
export const NAV_PERMISSIONS: string[] = [...new Set(navigation.flatMap((s) => s.items.flatMap((i) => (i.permission ? [i.permission].flat() : []))))]
