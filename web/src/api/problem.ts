import type { components } from './schema'

export type Problem = components['schemas']['Problem']
export type FieldError = components['schemas']['FieldError']

/**
 * ApiError is thrown for every non-2xx API response. It carries the stable
 * `code` from the RFC 9457 problem document, which the UI switches on
 * (never on HTTP status text or messages).
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fieldErrors: FieldError[]
  readonly context: Record<string, unknown>
  readonly retryable: boolean
  readonly requestId?: string

  constructor(problem: Problem) {
    super(problem.detail ?? problem.title)
    this.name = 'ApiError'
    this.status = problem.status
    this.code = problem.code
    this.fieldErrors = problem.errors ?? []
    this.context = problem.context ?? {}
    this.retryable = problem.retryable ?? false
    this.requestId = problem.request_id
  }

  /** Message for a given input field, if the server rejected it. */
  fieldMessage(field: string): string | undefined {
    const e = this.fieldErrors.find((f) => f.field === field)
    return e ? (e.message ?? e.code) : undefined
  }
}

function isProblem(value: unknown): value is Problem {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.code === 'string' && typeof v.status === 'number'
}

/** Converts any failed response body into an ApiError (non-problem bodies included). */
export function toApiError(body: unknown, status: number): ApiError {
  if (isProblem(body)) return new ApiError(body)
  return new ApiError({
    type: 'about:blank',
    title: 'Unexpected response',
    status,
    code: status === 0 ? 'NETWORK_ERROR' : 'UNEXPECTED_RESPONSE',
    detail: status === 0 ? 'The server could not be reached.' : `The server answered with HTTP ${status}.`,
  })
}
