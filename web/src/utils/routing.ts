import type { RoutingNotice } from '@/api/types'
import { t } from '@/i18n'

/**
 * Where the room charges of a line go, in the words of the page, when its group has a master folio (docs/architecture/30-group-folio.md, part C): the master folio, the guest's own folio for a room
 * that stays out of the routing, or a closed master that stops the night audit. Empty when the group says nothing about the room (no group, no instruction, the room has instructions of its own).
 */
export function routingText(r: RoutingNotice | null): string {
  const g = r?.group
  if (!r || !g) return ''
  if (r.excluded) return r.group_instructions.length ? t('groupMaster.noticeExcluded', { group: g.code }) : ''
  // what the group sends to its master: every instruction that applies to the room (the room's own instruction for the room nights wins over the group's)
  const what = r.group_instructions
    .filter((i) => !(i.scope === 'ROOM' && r.room === 'LINE'))
    .map((i) => (i.scope === 'ROOM' ? t('groupMaster.whatRoom') : i.scope === 'ALL' ? t('groupMaster.whatAll') : t('groupMaster.whatCode', { code: i.charge_code ?? String(i.charge_code_id) })))
  if (!what.length) return ''
  if (g.master_status === 'CLOSED') return t('groupMaster.noticeMasterClosed', { folio: g.master_folio_number, group: g.code })
  return t('groupMaster.noticeGoes', { folio: g.master_folio_number, group: g.code, what: what.join(', ') })
}
