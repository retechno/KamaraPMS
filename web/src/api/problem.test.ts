import { describe, expect, it } from 'vitest'
import { ApiError, toApiError } from './problem'

describe('toApiError', () => {
  it('keeps the stable code and details of a problem document', () => {
    const err = toApiError(
      {
        type: 'urn:kamarapms:problem:VALIDATION_FAILED',
        title: 'Unprocessable Entity',
        status: 422,
        code: 'VALIDATION_FAILED',
        detail: 'check the input',
        errors: [{ field: 'arrival_date', code: 'REQUIRED', message: 'arrival date is required' }],
        context: { nights: ['2026-10-01'] },
        request_id: 'abc',
      },
      422,
    )
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('VALIDATION_FAILED')
    expect(err.status).toBe(422)
    expect(err.message).toBe('check the input')
    expect(err.fieldMessage('arrival_date')).toBe('arrival date is required')
    expect(err.fieldMessage('departure_date')).toBeUndefined()
    expect(err.context).toEqual({ nights: ['2026-10-01'] })
    expect(err.requestId).toBe('abc')
    expect(err.retryable).toBe(false)
  })

  it('marks retryable contention errors', () => {
    const err = toApiError(
      { type: 't', title: 'Conflict', status: 409, code: 'RESOURCE_BUSY', retryable: true },
      409,
    )
    expect(err.retryable).toBe(true)
  })

  it('wraps non-problem responses', () => {
    const err = toApiError('<html>bad gateway</html>', 502)
    expect(err.code).toBe('UNEXPECTED_RESPONSE')
    expect(err.status).toBe(502)
  })
})
