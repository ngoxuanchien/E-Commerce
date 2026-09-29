/** Absolute origin for API requests (openapi-fetch builds `Request`s, which need absolute URLs under Node). */
export function apiBaseUrl(): string {
  return typeof window === 'undefined' ? 'http://localhost' : window.location.origin;
}
