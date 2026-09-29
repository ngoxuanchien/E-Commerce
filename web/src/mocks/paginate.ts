import { validation } from './mock-error';

/** Offset cursor encoded as opaque base64, mirroring the `?limit=&cursor=` convention. */
export function paginate<T>(items: T[], limit?: number | null, cursor?: string | null): { items: T[]; next_cursor: string | null } {
  const size = limit ?? 20;
  if (!Number.isInteger(size) || size < 1 || size > 100) throw validation({ limit: 'must be between 1 and 100' });
  let offset = 0;
  if (cursor) {
    let decoded = Number.NaN;
    try {
      decoded = Number(atob(cursor));
    } catch {
      // fall through to the validation error
    }
    if (!Number.isInteger(decoded) || decoded < 0) throw validation({ cursor: 'invalid cursor' });
    offset = decoded;
  }
  const next = offset + size;
  return { items: items.slice(offset, next), next_cursor: next < items.length ? btoa(String(next)) : null };
}
