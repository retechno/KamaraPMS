import { newIdempotencyKey } from '@/utils/reservations'

/**
 * The Idempotency-Key of one kind of request (a payment). The key is kept while an attempt has no sure answer from the server (the network dropped, the page waited), so that "try again"
 * with the same content is the same request and cannot pay twice. It is renewed when the server answered (done or refused: `answered`), or when the content changed (the amount, the method
 * or the reference are another request: the same key with other content is `IDEMPOTENCY_KEY_REUSED`, and a new amount must not be mistaken for the old attempt).
 */
export function createAttemptKey(): { keyFor: (content: readonly unknown[]) => string; answered: () => void } {
  let kept: { fingerprint: string; key: string } | null = null
  return {
    keyFor(content) {
      const fingerprint = JSON.stringify(content)
      if (!kept || kept.fingerprint !== fingerprint) kept = { fingerprint, key: newIdempotencyKey() }
      return kept.key
    },
    answered() {
      kept = null
    },
  }
}
