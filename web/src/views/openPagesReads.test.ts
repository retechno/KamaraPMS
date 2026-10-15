import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { navigation } from '@/navigation'

// The reference data of setup (room types, rate plans, charge codes...) is read by whoever manages it or uses it, and by nobody else (docs/architecture/33-setup-read-permissions.md). A page that
// the menu shows to everyone (no permission named) must not depend on it: it would break for housekeeping. This reads the source of those pages and fails when one calls a gated read
// without asking for the permission that lets it.
const SRC = path.resolve(__dirname, '..')
const GATED = [
  /\/room-types['/]/, /\/bed-types'/, /\/rooms'/, /\/taxes'/, /\/service-charges'/, /\/charge-codes['/]/, /\/rate-plans['/]/, /\/rates'/, /\/rate-restrictions['/]/, /\/yield-rules['/]/, /\/approval-rules'/,
]

function viewOf(to: string): string | null {
  const router = fs.readFileSync(path.join(SRC, 'router', 'index.ts'), 'utf8')
  if (to === '/') return 'views/HomeView.vue'
  const at = router.indexOf(`path: '${to}'`)
  if (at < 0) return null
  const m = /import\('@\/(views\/[^']+)'\)/.exec(router.slice(at, at + 400))
  return m ? m[1]! : null
}

describe('the pages that the menu shows to every member of the property', () => {
  const open = navigation.flatMap((s) => s.items).filter((i) => !i.permission && !i.adminOnly)

  it('are found (the scan looks at something)', () => {
    expect(open.map((i) => i.id)).toEqual(expect.arrayContaining(['dashboard', 'roomStatus', 'housekeeping', 'cleaningList', 'roomBlocks']))
    for (const i of open) expect(viewOf(i.to), i.to).not.toBeNull()
  })

  it('do not read the reference data of setup, or ask for the permission that allows it', () => {
    const problems: string[] = []
    for (const i of open) {
      const file = viewOf(i.to)!
      const source = fs.readFileSync(path.join(SRC, file), 'utf8')
      for (const [, call] of source.matchAll(/api\.GET\('([^']+)'/g)) {
        if (!GATED.some((g) => g.test(`${call}'`))) continue
        // a read that is made only for who may manage the thing names the permission in the page
        if (!/auth\.can\('(room_block|room|rate|billing_config)\.manage'/.test(source)) problems.push(`${i.id} (${file}) reads ${call} for everyone`)
      }
    }
    expect(problems).toEqual([])
  })

  it('read the rooms of the blocks only for who may block a room', () => {
    const source = fs.readFileSync(path.join(SRC, 'views/rooms/RoomBlocksView.vue'), 'utf8')
    expect(source).toMatch(/canManage\.value\s*\n?\s*\? fetchAll\(\(cursor\) => api\.GET\('\/api\/v1\/properties\/\{propertyId\}\/rooms'/)
  })
})
