/**
 * What a folio holds after a payment, when that is a credit: the balance is charged minus paid (positive = the guest owes), so a payment above it leaves a credit of the difference. Display only;
 * the server decides what it accepts. Returns the credit as a plain number string-free amount (0 when there is none or the amount is not a number).
 */
export function creditAfterPayment(balance: string, amount: string): number {
  const b = Number(balance)
  const a = Number(amount.trim())
  if (!Number.isFinite(b) || !Number.isFinite(a) || a <= 0) return 0
  const left = Math.round((b - a) * 1000) / 1000
  return left < 0 ? -left : 0
}
