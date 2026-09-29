import type { ErrorCode } from '@/api/types';

/** A failure the mock API reports with the standard `{error:{code,message,details}}` envelope. */
export class MockError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode,
    message: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message);
  }
}

export const validation = (fields: Record<string, string>) => new MockError(400, 'VALIDATION_ERROR', 'request validation failed', { fields });
export const notFound = (what: string) => new MockError(404, 'NOT_FOUND', `${what} not found`);
