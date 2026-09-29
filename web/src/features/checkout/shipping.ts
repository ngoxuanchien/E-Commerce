/**
 * Flat shipping fee per order (WS3 SHIPPING_FEE_FLAT default). The contract exposes no fee before
 * POST /api/checkout, so the preview uses this constant; the created orders carry the authoritative
 * shipping_fee. See "Contract Gaps Found" in docs/superpowers/plans/2026-09-28-ws5-web.md.
 */
export const SHIPPING_FEE_FLAT = 30_000;
