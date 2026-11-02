import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { i18n, setLocale } from '@/i18n'
import { routeTitle } from '@/utils/routeTitle'
import { router } from '@/router'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn().mockResolvedValue({ data: { data: [] } }), POST: vi.fn() } }))
vi.mock('@/api/session', () => ({ refreshSession: vi.fn().mockResolvedValue(null), setAccessToken: vi.fn() }))

const reports = router.getRoutes().filter((r) => r.meta.titleKey)

describe('the breadcrumb of a report says what the heading says', () => {
  it('covers the inventory and banquet reports', () => {
    expect(reports.length).toBeGreaterThanOrEqual(29)
    expect(reports.every((r) => r.path.startsWith('/inventory/reports') || r.path.startsWith('/banquet/reports'))).toBe(true)
  })

  it.each(['en', 'id'] as const)('has the heading key in %s and routeTitle returns it', async (locale) => {
    await setLocale(locale)
    for (const r of reports) {
      const key = r.meta.titleKey as string
      expect(i18n.global.te(key), `${r.path} ${key}`).toBe(true)
      expect(routeTitle({ path: r.path, name: r.name, meta: r.meta } as never)).toBe(i18n.global.t(key))
    }
    await setLocale('en')
  })

  it('uses the same key as the PageHeader of the view', () => {
    const routes = readFileSync(resolve(__dirname, 'index.ts'), 'utf8')
    const found = [...routes.matchAll(/path: '([^']+)',[^\n]*import\('@\/(views\/[^']+\.vue)'\)[^\n]*titleKey: '([^']+)'/g)]
    expect(found.length).toBe(reports.length)
    for (const [, path, file, key] of found) {
      const src = readFileSync(resolve(__dirname, '..', file), 'utf8')
      expect(src, `${path} should show ${key} as its title`).toContain(`:title="t('${key}')"`)
    }
  })
})
