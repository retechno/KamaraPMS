import { computed, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, FreeRoom, Guest, GuestHistoryItem, GuestView, PatchGuestRequest, RoomType } from '@/api/types'
import { blankGuestForm } from '@/components/guestForm'
import { fetchFreeRooms } from '@/composables/useFreeRooms'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { CHECKIN_STEPS, type CheckInStep, DEPOSIT_METHODS, type DepositMethod, guestHints, isForeign, isPositive, proposedDeposit } from '@/utils/checkin'
import { isRoomReady, roomOptionLabel, sortFreeRooms } from '@/utils/freeRooms'
import { newIdempotencyKey } from '@/utils/reservations'

const METHOD_KEY = 'checkin.depositMethod'

/** The method the desk used last (a convenience of this browser only). */
function rememberedMethod(): DepositMethod {
  try {
    const m = window.localStorage.getItem(METHOD_KEY)
    if (m && (DEPOSIT_METHODS as readonly string[]).includes(m)) return m as DepositMethod
  } catch {
    // storage may be blocked: the default is fine
  }
  return 'CASH'
}

/** What was taken as a deposit in this sheet: kept so a check-in that failed afterwards is retried without taking the money again. */
export interface PostedDeposit {
  paymentId: number
  number: string
  amount: string
  method: DepositMethod
}

/**
 * The state and the actions of the check-in sheet of one arrival: guest, room, deposit, done.
 *
 * Nothing is written before the last step is confirmed except the guest profile (one PATCH, or one POST for a guest who is created here). The last step sends two requests, each its own
 * transaction with its own Idempotency-Key: the deposit first (on the reservation, as the endpoint that exists), then the check-in, which links that deposit folio to the stay. A deposit that
 * went through and a check-in that did not leaves the money safe on the reservation and the stay not started; the retry only repeats the check-in.
 */
export function useCheckIn(arrival: Arrival) {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const pid = computed(() => property.currentId)
  const decimals = computed(() => property.current?.currency_decimals ?? 0)

  const step = ref<CheckInStep>('guest')
  const reached = ref(0)
  const busy = ref(false)
  const error = ref<ApiError | null>(null)
  const result = ref<CheckInResult | null>(null)
  const idx = (s: CheckInStep): number => CHECKIN_STEPS.indexOf(s)

  // ---- guest
  const guestId = ref<number | null>(arrival.guest_id)
  const guest = ref<GuestView | null>(null)
  const form = reactive(blankGuestForm())
  const baseline = ref(JSON.stringify(form))
  const history = ref<GuestHistoryItem[]>([])
  const guestLoading = ref(false)
  const query = ref('')
  const results = ref<Guest[]>([])
  const searched = ref(false)
  const newGuest = reactive({ first_name: '', last_name: '' })
  const canWriteGuest = computed(() => auth.can('guest.write', pid.value))
  const dirty = computed(() => JSON.stringify(form) !== baseline.value)
  const hints = computed(() => guestHints(form))
  const foreign = computed(() => isForeign(form.nationality))
  const stays = computed(() => history.value.filter((h) => h.type === 'STAY'))
  const lastStay = computed(() => stays.value[0] ?? null)

  function fill(g: GuestView): void {
    guest.value = g
    for (const k of Object.keys(form) as (keyof typeof form)[]) form[k] = (g[k as keyof GuestView] as string | undefined) ?? ''
    baseline.value = JSON.stringify(form)
  }

  async function loadGuest(): Promise<void> {
    const id = guestId.value
    if (id === null) return
    guestLoading.value = true
    try {
      const { data } = await api.GET('/api/v1/guests/{id}', { params: { path: { id } } })
      if (data) fill(data)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    } finally {
      guestLoading.value = false
    }
    try {
      const { data } = await api.GET('/api/v1/guests/{id}/history', { params: { path: { id }, query: { limit: 10 } } })
      history.value = data?.data ?? []
    } catch {
      history.value = [] // the line about earlier stays is a courtesy: the check-in does not need it
    }
  }

  async function search(): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null || !query.value.trim()) return
    error.value = null
    try {
      const { data } = await api.GET('/api/v1/guests', { params: { query: { q: query.value.trim(), property_id: propertyId, limit: 10 } } })
      results.value = data?.data ?? []
      searched.value = true
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  async function pick(g: { id: number }): Promise<void> {
    guestId.value = g.id
    error.value = null
    await loadGuest()
  }

  /** A guest who is not in the file yet is created here: the check-in takes the guest id itself, so the reservation is not touched. */
  async function createGuest(): Promise<boolean> {
    const propertyId = pid.value
    if (propertyId === null || !newGuest.last_name.trim()) return false
    busy.value = true
    error.value = null
    try {
      const { data } = await api.POST('/api/v1/guests', { body: { origin_property_id: propertyId, first_name: newGuest.first_name.trim() || undefined, last_name: newGuest.last_name.trim() } })
      if (data) {
        guestId.value = data.id
        await loadGuest()
        return true
      }
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    } finally {
      busy.value = false
    }
    return false
  }

  /** The profile is saved when it changed and may be edited; every field is sent (an empty string clears it), as the guest page does. */
  async function saveGuest(): Promise<boolean> {
    if (guestId.value === null || !dirty.value || !guest.value?.can_edit) return true
    busy.value = true
    error.value = null
    try {
      const { data } = await api.PATCH('/api/v1/guests/{id}', { params: { path: { id: guestId.value } }, body: { ...form } as PatchGuestRequest })
      if (data) fill(data)
      return true
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
      return false
    } finally {
      busy.value = false
    }
  }

  // ---- room
  const types = ref<RoomType[]>([])
  const typeId = ref(arrival.room_type_id)
  const rooms = ref<FreeRoom[]>([])
  const roomId = ref<number | null>(arrival.room_id ?? null)
  const party = reactive({ adults: arrival.adult_count, children: arrival.child_count, override: false, reason: '' })
  const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
  const activeTypes = computed(() => types.value.filter((ty) => ty.is_active))
  const selected = computed(() => rooms.value.find((r) => r.room_id === roomId.value))
  const roomLabel = (r: FreeRoom): string => roomOptionLabel(r, { requestedBedTypeId: arrival.requested_bed_type_id, requiresInspection: requiresInspection.value })
  const notReady = computed(() => !!selected.value && !isRoomReady(selected.value.housekeeping_status, requiresInspection.value))
  const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
  const isUpgrade = computed(() => typeId.value !== arrival.room_type_id)
  const roomOk = computed(() => roomId.value !== null && (!notReady.value || party.override))

  async function loadRooms(): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    try {
      const free = await fetchFreeRooms(propertyId, { typeId: typeId.value, arrival: arrival.arrival_date, departure: arrival.departure_date }, property.clock?.business_date ?? '')
      // A room already assigned to this line is not "free" (the line holds it), so it is offered explicitly.
      if (arrival.room_id && typeId.value === arrival.room_type_id && !free.some((r) => r.room_id === arrival.room_id)) {
        free.unshift({ room_id: arrival.room_id, room_number: arrival.room_number ?? String(arrival.room_id), housekeeping_status: arrival.housekeeping_status ?? 'DIRTY' })
      }
      // The order of every list of rooms (`sortFreeRooms`): the room on the line first, then the ready rooms with the bed that was asked for, the ready rooms, then the rooms that are not ready.
      free.splice(0, free.length, ...sortFreeRooms(free, { requestedBedTypeId: arrival.requested_bed_type_id ?? null, currentRoomId: arrival.room_id ?? null, requiresInspection: requiresInspection.value }))
      rooms.value = free
      if (!free.some((r) => r.room_id === roomId.value)) roomId.value = free[0]?.room_id ?? null
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  async function loadTypes(): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    try {
      types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  // ---- deposit
  const estimate = ref<{ net: string; service: string; tax: string; total: string } | null>(null)
  const estimateLoaded = ref(false)
  const company = computed(() => arrival.company ?? null)
  const incidental = computed(() => property.current?.checkin_incidental_deposit ?? '0')
  const posted = ref<PostedDeposit | null>(null)
  const dep = reactive({ amount: '', method: rememberedMethod(), reference: '', touched: false })
  const canTakeDeposit = computed(() => auth.can('payment.post', pid.value))
  const heldBefore = computed(() => arrival.deposit?.paid ?? '0')
  const proposal = computed(() => proposedDeposit({ estimate: estimate.value?.total ?? null, incidental: incidental.value, paid: heldBefore.value, decimals: decimals.value }))
  /** Nothing is due: what the reservation holds already covers the stay and the incidental. */
  const covered = computed(() => estimateLoaded.value && isPositive(heldBefore.value, decimals.value) && !isPositive(proposal.value, decimals.value))
  const willTake = computed(() => isPositive(dep.amount, decimals.value))
  /** A guest who pays is asked for a deposit (or a deliberate "without deposit"); a company, a cover that is enough, or a desk without the right to take money is not. */
  const depositRequired = computed(() => !company.value && !covered.value && canTakeDeposit.value)
  const canFinish = computed(() => roomOk.value && (posted.value !== null || willTake.value || !depositRequired.value))

  async function loadEstimate(): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId, id: arrival.reservation_id } } })
      estimate.value = data?.rooms.find((r) => r.id === arrival.reservation_room_id)?.estimate ?? null
    } catch {
      estimate.value = null // the proposal is then the incidental alone, and the screen says the stay estimate is missing
    } finally {
      estimateLoaded.value = true
    }
    fillAmount()
  }

  /** The amount is proposed once, for a guest who pays, until the person types their own. */
  function fillAmount(): void {
    if (dep.touched || company.value || posted.value || !canTakeDeposit.value) return
    dep.amount = isPositive(proposal.value, decimals.value) ? proposal.value : ''
  }

  // ---- the steps
  const canGoBack = computed(() => step.value === 'room' || step.value === 'deposit')
  const finished = computed(() => step.value === 'done')

  function go(target: CheckInStep): void {
    if (finished.value || idx(target) > reached.value) return
    error.value = null
    step.value = target
  }

  function back(): void {
    if (canGoBack.value) go(CHECKIN_STEPS[idx(step.value) - 1]!)
  }

  function advance(to: CheckInStep): void {
    reached.value = Math.max(reached.value, idx(to))
    error.value = null
    step.value = to
  }

  /** Enter on a step: the next step, when the step is complete. */
  async function next(): Promise<void> {
    if (busy.value) return
    if (step.value === 'guest') {
      if (guestId.value === null) {
        if (!(await createGuest())) return
      }
      if (!(await saveGuest())) return
      advance('room')
    } else if (step.value === 'room') {
      if (!roomOk.value) return
      advance('deposit')
      if (!estimateLoaded.value) void loadEstimate()
      else fillAmount()
    } else if (step.value === 'deposit') {
      if (!canFinish.value) return // Enter alone does not skip the deposit: that is the deliberate button
      await finish(false)
    }
  }

  let depositKey = newIdempotencyKey() // like the check-in key: kept while a request may have been lost, renewed once the server has answered
  let checkInKey = newIdempotencyKey()
  /** The deposit went through and the check-in did not: the desk is told so, and the retry repeats the check-in only. */
  const failedAfterDeposit = ref(false)

  /** The last step: the deposit (when one is taken), then the check-in, each its own request. `withoutDeposit` is the deliberate click. */
  async function finish(withoutDeposit: boolean): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null || roomId.value === null || (guestId.value ?? arrival.guest_id) === null || busy.value) return
    busy.value = true
    error.value = null
    failedAfterDeposit.value = false
    try {
      if (!withoutDeposit && willTake.value && posted.value === null) {
        try {
          const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/deposits', {
            params: { path: { propertyId, id: arrival.reservation_id }, header: { 'Idempotency-Key': depositKey } },
            body: { amount: dep.amount.trim(), payment_method: dep.method, reference_number: dep.reference.trim() || undefined },
          })
          if (data) posted.value = { paymentId: data.payment.id, number: data.payment.payment_number, amount: data.payment.amount, method: dep.method }
        } catch (e) {
          error.value = e instanceof ApiError ? e : null
          if (e instanceof ApiError) depositKey = newIdempotencyKey()
          return
        }
      }
      try {
        const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/check-in', {
          params: { path: { propertyId, id: arrival.reservation_id, lineId: arrival.reservation_room_id }, header: { 'Idempotency-Key': checkInKey } },
          body: {
            version: arrival.reservation_version, room_id: roomId.value, guest_id: guestId.value ?? arrival.guest_id!, adult_count: party.adults, child_count: party.children,
            override_room_not_ready: party.override, override_reason: party.override ? party.reason : undefined,
          },
        })
        if (data) {
          result.value = data
          try {
            window.localStorage.setItem(METHOD_KEY, dep.method)
          } catch {
            // not remembered: harmless
          }
          advance('done')
        }
      } catch (e) {
        error.value = e instanceof ApiError ? e : null
        if (e instanceof ApiError) checkInKey = newIdempotencyKey()
        failedAfterDeposit.value = posted.value !== null
      }
    } finally {
      busy.value = false
    }
  }

  /** The first reads, in parallel: the guest, the types and rooms (the room step is ready when it is reached) and the estimate. */
  async function init(): Promise<void> {
    await Promise.all([loadGuest(), loadTypes().then(loadRooms), loadEstimate()])
  }

  return {
    step, reached, busy, error, result, go, back, next, finish, init, canGoBack, finished,
    guestId, guest, form, dirty, hints, foreign, stays, lastStay, guestLoading, query, results, searched, newGuest, canWriteGuest, search, pick, createGuest, saveGuest, loadGuest,
    types, typeId, rooms, roomId, party, activeTypes, selected, roomLabel, notReady, canOverride, isUpgrade, roomOk, requiresInspection, loadRooms,
    estimate, estimateLoaded, company, incidental, posted, dep, canTakeDeposit, heldBefore, proposal, covered, willTake, depositRequired, canFinish, failedAfterDeposit, decimals,
  }
}

export type CheckInState = ReturnType<typeof useCheckIn>
