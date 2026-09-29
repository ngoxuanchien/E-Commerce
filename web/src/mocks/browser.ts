import { setupWorker } from 'msw/browser';
import { createHandlers } from './handlers';
import { getDb, resetDb } from './state';

export async function startMockWorker(): Promise<void> {
  resetDb(window.localStorage);
  const worker = setupWorker(...createHandlers(getDb));
  await worker.start({ onUnhandledRequest: 'bypass', quiet: true });
}
