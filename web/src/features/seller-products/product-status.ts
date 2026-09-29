import type { ProductStatus } from '@/api/types';

export const PRODUCT_STATUS_LABEL: Record<ProductStatus, string> = { active: 'Đang bán', draft: 'Nháp', archived: 'Đã lưu trữ' };

export const PRODUCT_STATUS_TABS: ReadonlyArray<{ value: ProductStatus | undefined; label: string }> = [
  { value: undefined, label: 'Tất cả' },
  { value: 'active', label: 'Đang bán' },
  { value: 'draft', label: 'Nháp' },
  { value: 'archived', label: 'Đã lưu trữ' },
];

export function parseProductStatus(value: string | null): ProductStatus | undefined {
  return value !== null && Object.hasOwn(PRODUCT_STATUS_LABEL, value) ? (value as ProductStatus) : undefined;
}
