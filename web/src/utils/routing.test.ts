import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { routingText } from './routing'

const group = (over: object = {}) => ({ id: 4, code: 'GRP1', name: 'Conference', master_folio_id: 30, master_folio_number: 'FOL000030', master_status: 'OPEN', ...over })
const notice = (over: object = {}) => ({ room: 'GROUP', excluded: false, group: group(), group_instructions: [{ scope: 'ROOM', charge_code_id: null }], ...over }) as never

describe('routingText', () => {
  beforeEach(() => setLocale('en'))

  it('says the room charges go to the master folio of the group', () => {
    expect(routingText(notice())).toBe('The room charges go to the master folio FOL000030 of group GRP1.')
  })

  it('says it is closed when the night audit cannot post to it', () => {
    expect(routingText(notice({ group: group({ master_status: 'CLOSED' }) }))).toBe('The master folio FOL000030 of group GRP1 is closed: the night audit cannot post the room nights until it is reopened.')
  })

  it('says a room that stays out keeps its own folio, only when the group routes anything', () => {
    expect(routingText(notice({ room: 'NONE', excluded: true }))).toBe('This room stays out of the routing of group GRP1: its charges go to its own folio.')
    expect(routingText(notice({ room: 'NONE', excluded: true, group_instructions: [] }))).toBe('')
  })

  it('says nothing when the room has instructions of its own, no group or nothing routed', () => {
    expect(routingText(notice({ room: 'LINE' }))).toBe('')
    expect(routingText(notice({ room: 'NONE' }))).toBe('')
    expect(routingText(notice({ group: null }))).toBe('')
    expect(routingText(null)).toBe('')
  })

  it('is in Indonesian', () => {
    setLocale('id')
    expect(routingText(notice())).toBe('Biaya kamar masuk ke master folio FOL000030 grup GRP1.')
  })
})
