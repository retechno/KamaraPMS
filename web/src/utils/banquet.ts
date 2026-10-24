import type { BanquetCalendarLock, BanquetCalendarSession } from '@/api/types'
import { addDays } from '@/utils/dates'
import { formatDateShort } from '@/utils/format'

/** Pure rules of the banquet screens (docs/architecture/36-banquet.md 6): where a session sits on the time axis, how it is drawn, which day a week starts on. */

/** Minutes since midnight of "HH:MM" or "HH:MM:SS"; 0 for anything else. */
export function minutesOf(time: string | null | undefined): number {
  const m = /^(\d{1,2}):(\d{2})/.exec(time ?? '')
  return m ? Number(m[1]) * 60 + Number(m[2]) : 0
}

export function hhmm(minutes: number): string {
  const m = ((minutes % 1440) + 1440) % 1440
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
}

/** The span of a session in minutes from the midnight of its day; a session that ends after midnight runs past 1440. */
export function spanOf(s: Pick<BanquetCalendarSession, 'start_time' | 'end_time' | 'ends_next_day'>): { start: number; end: number } {
  const start = minutesOf(s.start_time)
  return { start, end: minutesOf(s.end_time) + (s.ends_next_day ? 1440 : 0) }
}

/** The room is held from `setup_minutes` before the start to `teardown_minutes` after the end. */
export function heldSpan(s: Pick<BanquetCalendarSession, 'start_time' | 'end_time' | 'ends_next_day' | 'setup_minutes' | 'teardown_minutes'>): { start: number; end: number } {
  const { start, end } = spanOf(s)
  return { start: start - s.setup_minutes, end: end + s.teardown_minutes }
}

export interface Geometry {
  /** percent from the top of the grid */
  top: number
  height: number
  setupHeight: number
  teardownHeight: number
}

/** Where a session is drawn on a grid that shows `fromMin` to `toMin`; the part outside the grid is cut. */
export function geometry(s: BanquetCalendarSession, fromMin: number, toMin: number): Geometry {
  const total = Math.max(1, toMin - fromMin)
  const pct = (m: number): number => ((Math.min(Math.max(m, fromMin), toMin) - fromMin) / total) * 100
  const { start, end } = spanOf(s)
  const held = heldSpan(s)
  const top = pct(held.start)
  return { top, height: Math.max(0, pct(held.end) - top), setupHeight: pct(start) - top, teardownHeight: pct(held.end) - pct(end) }
}

export type Tone = 'definite' | 'first' | 'queued' | 'taken'

/** Definite is solid; the first option is amber; an option behind another is grey with its place; an option under a confirmed event is struck. */
export function toneOf(s: Pick<BanquetCalendarSession, 'event_status' | 'rank' | 'room_taken'>): Tone {
  if (s.event_status === 'DEFINITE' || s.event_status === 'COMPLETED') return 'definite'
  if (s.room_taken) return 'taken'
  return (s.rank ?? 1) > 1 ? 'queued' : 'first'
}

export const TONE_CLASS: Record<Tone, string> = {
  definite: 'border-primary bg-primary/20',
  first: 'border-warning bg-warning/25',
  queued: 'border-border bg-muted text-muted-foreground',
  taken: 'border-destructive/50 bg-muted text-muted-foreground line-through',
}

/** The Monday on or before a date. */
export function weekStart(date: string): string {
  const [y, m, d] = date.split('-').map(Number)
  const dow = new Date(Date.UTC(y!, m! - 1, d!)).getUTCDay()
  return addDays(date, -((dow + 6) % 7))
}

export function weekDays(date: string): string[] {
  const first = weekStart(date)
  return Array.from({ length: 7 }, (_, i) => addDays(first, i))
}

/** The sessions of a room on a day, earliest first. */
export function sessionsOf(list: BanquetCalendarSession[], roomId: number, date: string): BanquetCalendarSession[] {
  return list.filter((s) => s.room_id === roomId && s.session_date === date).sort((a, b) => minutesOf(a.start_time) - minutesOf(b.start_time) || a.session_id - b.session_id)
}

/** "2026-11-07T18:30" as its day and minutes; the day is "" for anything else. */
function stamp(value: string): { date: string; minutes: number } {
  const [date = '', time = ''] = value.split('T')
  return { date, minutes: minutesOf(time) }
}

/**
 * The part of a lock (a hold on the room from another booking: the parts of a combined room, the combined rooms of a part) that falls on a day, in minutes from the midnight of that day.
 * The hold comes with its set-up and tear-down from the server; a hold that runs over midnight is cut at it and continues on the next day. Nothing when the hold does not touch the day.
 */
export function lockSpan(l: Pick<BanquetCalendarLock, 'hold_from' | 'hold_to'>, date: string): { start: number; end: number } | null {
  const a = stamp(l.hold_from)
  const b = stamp(l.hold_to)
  const dayOf = (d: string): number => Date.parse(`${d}T00:00:00Z`) / 60000
  const from = dayOf(a.date) + a.minutes - dayOf(date)
  const to = dayOf(b.date) + b.minutes - dayOf(date)
  const start = Math.max(from, 0)
  const end = Math.min(to, 1440)
  return end > start ? { start, end } : null
}

/** The locks of a room on a day, earliest first. */
export function locksOf(list: BanquetCalendarLock[], roomId: number, date: string): BanquetCalendarLock[] {
  return list.filter((l) => l.room_id === roomId && lockSpan(l, date) !== null).sort((a, b) => lockSpan(a, date)!.start - lockSpan(b, date)!.start || a.session_id - b.session_id)
}

export interface Lane {
  lane: number
  lanes: number
}

/**
 * Items that overlap in time sit side by side: each overlapping run shares the width of the column between as many lanes as it needs, an item takes the first lane that is free when it starts.
 * `end` is exclusive, so an item that starts when another ends does not share a lane count with it.
 */
export function layoutLanes(items: { id: number; start: number; end: number }[]): Map<number, Lane> {
  const sorted = [...items].sort((a, b) => a.start - b.start || b.end - a.end || a.id - b.id)
  const out = new Map<number, Lane>()
  let run: { id: number; lane: number }[] = []
  let laneEnds: number[] = []
  let runEnd = -Infinity
  const close = (): void => {
    for (const r of run) out.set(r.id, { lane: r.lane, lanes: Math.max(1, laneEnds.length) })
    run = []
    laneEnds = []
  }
  for (const it of sorted) {
    if (it.start >= runEnd) {
      close()
      runEnd = -Infinity
    }
    let lane = laneEnds.findIndex((e) => e <= it.start)
    if (lane < 0) {
      lane = laneEnds.length
      laneEnds.push(it.end)
    } else {
      laneEnds[lane] = it.end
    }
    run.push({ id: it.id, lane })
    runEnd = Math.max(runEnd, it.end)
  }
  close()
  return out
}

/** "23 Okt 09:30–15:15", or with both days when the hold passes midnight: "23 Okt 22:00 – 24 Okt 02:00". */
export function formatHoldRange(from: string, to: string): string {
  const a = stamp(from)
  const b = stamp(to)
  if (!a.date || !b.date) return `${from} – ${to}`
  const time = (m: number): string => hhmm(m)
  if (a.date === b.date) return `${formatDateShort(a.date)} ${time(a.minutes)}–${time(b.minutes)}`
  return `${formatDateShort(a.date)} ${time(a.minutes)} – ${formatDateShort(b.date)} ${time(b.minutes)}`
}
