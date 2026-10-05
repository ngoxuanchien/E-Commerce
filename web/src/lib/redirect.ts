/**
 * Redirect the user to the given URL by assigning to window.location.
 * Testable by mocking: vi.mock('@/lib/redirect', () => ({ redirectTo: vi.fn() }))
 */
export function redirectTo(url: string): void {
  window.location.assign(url);
}
