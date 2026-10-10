import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { t, te } from '@/i18n'
import { navigation } from '@/navigation'

/**
 * The title of a page in the language of the page, for the tab of the browser and the breadcrumb: the label the menu gives it when it is in the menu,
 * else `routeTitles.<name>`, else the English title of the route.
 */
export function routeTitle(route: Pick<RouteLocationNormalizedLoaded, 'path' | 'name' | 'meta'>): string {
  for (const section of navigation) {
    const item = section.items.find((i) => i.to === route.path)
    if (item) return t(`nav.items.${item.id}` as never)
  }
  const key = `routeTitles.${String(route.name ?? '').replace(/-/g, '_')}`
  return te(key) ? t(key as never) : String(route.meta.title ?? '')
}
