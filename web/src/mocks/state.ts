import { MockDb } from './db';

let current: MockDb | undefined;

export function getDb(): MockDb {
  current ??= new MockDb();
  return current;
}

/** Fresh seed data; pass localStorage in the browser so state survives reloads. */
export function resetDb(storage?: Storage): MockDb {
  current = new MockDb(storage);
  return current;
}
