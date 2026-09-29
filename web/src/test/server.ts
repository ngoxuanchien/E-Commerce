import { setupServer } from 'msw/node';
import { createHandlers } from '@/mocks/handlers';
import { getDb } from '@/mocks/state';

export const server = setupServer(...createHandlers(getDb));
