/**
 * The writes after which the steps of the night audit may have changed: a guest checked in, checked out, marked no-show or cancelled, a stay moved or its departure changed, a walk-in, a
 * cashier shift opened or closed, the no-shows of the night audit, the run itself. The path is the one of the request, as sent.
 */
const TRIGGERS: RegExp[] = [
  /\/properties\/\d+\/reservations\/\d+\/cancel$/,
  /\/properties\/\d+\/reservations\/\d+\/rooms\/\d+\/(check-in|no-show|cancel)$/,
  /\/properties\/\d+\/groups\/\d+\/(check-ins|cancellations)$/,
  /\/properties\/\d+\/walk-ins$/,
  /\/properties\/\d+\/stays\/\d+\/(check-out|reverse-check-in|change-departure|move)$/,
  /\/properties\/\d+\/cashier\/shifts$/,
  /\/properties\/\d+\/cashier\/shifts\/\d+\/close$/,
  /\/properties\/\d+\/night-audit\/(no-shows|run)$/,
]

export function touchesNightAudit(method: string, path: string): boolean {
  return method !== 'GET' && TRIGGERS.some((re) => re.test(path))
}
