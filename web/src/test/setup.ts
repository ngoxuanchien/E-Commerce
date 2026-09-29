import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterAll, afterEach, beforeAll, beforeEach } from 'vitest';
import { clearSession, setAuthFailureHandler } from '@/api/session';
import { resetDb } from '@/mocks/state';
import { server } from './server';

// jsdom gaps used by Radix and sonner (skipped in files that opt into the node environment).
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false }) as MediaQueryList;
}
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};
if (typeof Element !== 'undefined') {
  Element.prototype.scrollIntoView ??= function () {};
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.setPointerCapture ??= () => {};
}
URL.createObjectURL ??= () => 'blob:mock';
URL.revokeObjectURL ??= () => {};

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
beforeEach(() => setAuthFailureHandler(() => {}));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  server.events.removeAllListeners();
  resetDb();
  clearSession();
});
afterAll(() => server.close());
