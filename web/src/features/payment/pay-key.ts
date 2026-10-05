/**
 * Per-checkout payment key for idempotency.
 * Stored in sessionStorage under pay:{checkoutId}.
 */
export function payKey(checkoutId: string): string {
  const key = `pay:${checkoutId}`;
  try {
    const stored = sessionStorage.getItem(key);
    if (stored) return stored;
  } catch {
    // sessionStorage access denied or unavailable
  }

  const newKey = crypto.randomUUID();
  try {
    sessionStorage.setItem(key, newKey);
  } catch {
    // sessionStorage access denied or unavailable; return unstored key
  }
  return newKey;
}

/**
 * Renew the payment key for a checkout, always creating a new one.
 */
export function renewPayKey(checkoutId: string): string {
  const key = `pay:${checkoutId}`;
  const newKey = crypto.randomUUID();
  try {
    sessionStorage.setItem(key, newKey);
  } catch {
    // sessionStorage access denied or unavailable
  }
  return newKey;
}
