import type { ErrorBody, ErrorCode } from './types';

export const ERROR_MESSAGES: Record<ErrorCode, string> = {
  VALIDATION_ERROR: 'Dữ liệu chưa hợp lệ. Vui lòng kiểm tra các trường được đánh dấu.',
  UNAUTHENTICATED: 'Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.',
  FORBIDDEN: 'Bạn không có quyền thực hiện thao tác này.',
  NOT_FOUND: 'Không tìm thấy nội dung bạn yêu cầu.',
  CONFLICT: 'Dữ liệu đã thay đổi. Vui lòng tải lại trang và thử lại.',
  OUT_OF_STOCK: 'Một số sản phẩm không đủ hàng. Vui lòng điều chỉnh số lượng.',
  INSUFFICIENT_FUNDS: 'Số dư không đủ để thực hiện yêu cầu.',
  INVALID_STATE_TRANSITION: 'Trạng thái đơn hàng đã thay đổi. Vui lòng tải lại trang.',
  PAYLOAD_TOO_LARGE: 'Dữ liệu gửi lên quá lớn.',
  IDEMPOTENCY_KEY_REUSED: 'Yêu cầu đặt hàng bị trùng với nội dung khác. Vui lòng tải lại trang thanh toán.',
  RATE_LIMITED: 'Bạn thao tác quá nhanh. Vui lòng thử lại sau giây lát.',
  INTERNAL: 'Máy chủ gặp lỗi. Vui lòng thử lại.',
  UPSTREAM_UNAVAILABLE: 'Dịch vụ đang tạm gián đoạn. Vui lòng thử lại.',
};

const FIELD_MESSAGE = 'Giá trị không hợp lệ';

function codeFromStatus(status: number): ErrorCode {
  if (status === 401) return 'UNAUTHENTICATED';
  if (status === 413) return 'PAYLOAD_TOO_LARGE';
  if (status === 429) return 'RATE_LIMITED';
  if (status === 0 || status === 502 || status === 503 || status === 504) return 'UPSTREAM_UNAVAILABLE';
  return 'INTERNAL';
}

function isErrorBody(body: unknown): body is ErrorBody {
  const error = (body as { error?: { code?: unknown } } | null)?.error;
  return typeof error?.code === 'string' && error.code in ERROR_MESSAGES;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode,
    message: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  get retryable(): boolean {
    return this.code === 'RATE_LIMITED' || this.code === 'UPSTREAM_UNAVAILABLE';
  }

  static from(status: number, body: unknown): ApiError {
    if (isErrorBody(body)) {
      return new ApiError(status, body.error.code, body.error.message, body.error.details);
    }
    return new ApiError(status, codeFromStatus(status), `HTTP ${status}`);
  }
}

/** Resolves an openapi-fetch call to its data or throws an ApiError. */
export async function unwrap<T>(request: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  let result: { data?: T; error?: unknown; response: Response };
  try {
    result = await request;
  } catch (cause) {
    if (cause instanceof ApiError) throw cause; // e.g. a refresh that failed as RATE_LIMITED
    throw new ApiError(0, 'UPSTREAM_UNAVAILABLE', cause instanceof Error ? cause.message : 'network error');
  }
  if (!result.response.ok) throw ApiError.from(result.response.status, result.error);
  return result.data as T;
}

export function isRetryable(err: unknown): boolean {
  return err instanceof ApiError && err.retryable;
}

export function errorMessage(err: unknown, overrides: Partial<Record<ErrorCode, string>> = {}): string {
  if (!(err instanceof ApiError)) return ERROR_MESSAGES.INTERNAL;
  return overrides[err.code] ?? ERROR_MESSAGES[err.code];
}

export type Shortage = { sku_id: string; requested: number; available: number };

export function shortagesOf(err: unknown): Shortage[] {
  if (!(err instanceof ApiError) || err.code !== 'OUT_OF_STOCK') return [];
  const shortages = err.details?.shortages;
  return Array.isArray(shortages) ? (shortages as Shortage[]) : [];
}

/** VALIDATION_ERROR details.fields with JSON paths (`skus[0].price`) turned into form paths (`skus.0.price`). */
export function fieldErrorsOf(err: unknown, rename: Record<string, string> = {}): Record<string, string> {
  if (!(err instanceof ApiError) || err.code !== 'VALIDATION_ERROR') return {};
  const fields = err.details?.fields;
  if (!fields || typeof fields !== 'object') return {};
  const out: Record<string, string> = {};
  for (const [path, message] of Object.entries(fields as Record<string, unknown>)) {
    const formPath = path.replace(/\[(\d+)\]/g, '.$1');
    // Rename a field and everything under it, e.g. image_keys.0 → images.0.
    const renamed = Object.entries(rename).reduce(
      (p, [from, to]) => (p === from || p.startsWith(`${from}.`) ? to + p.slice(from.length) : p),
      formPath,
    );
    out[renamed] = String(message);
  }
  return out;
}

export function applyFieldErrors(
  err: unknown,
  setError: (name: never, error: { type: string; message: string }) => void,
  options: { stripPrefix?: string; rename?: Record<string, string> } = {},
): boolean {
  const fields = fieldErrorsOf(err, options.rename);
  const names = Object.keys(fields);
  for (const name of names) {
    const target = options.stripPrefix && name.startsWith(options.stripPrefix) ? name.slice(options.stripPrefix.length) : name;
    setError(target as never, { type: 'server', message: FIELD_MESSAGE });
  }
  return names.length > 0;
}
