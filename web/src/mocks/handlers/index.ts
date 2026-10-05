import type { MockDb } from '../db';
import { catalogHandlers } from './catalog';
import { identityHandlers } from './identity';
import { orderHandlers } from './order';
import { paymentHandlers } from './payment';
import { uploadHandlers } from './uploads';

export function createHandlers(db: () => MockDb) {
  return [...identityHandlers(db), ...catalogHandlers(db), ...orderHandlers(db), ...paymentHandlers(db), ...uploadHandlers(db)];
}
