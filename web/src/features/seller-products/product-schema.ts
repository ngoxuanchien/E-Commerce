import { z } from 'zod';
import type { Product, ProductInput, ProductPatch, Sku } from '@/api/types';

const PRICE_RANGE = 'Giá từ 1.000 ₫ đến 1.000.000.000 ₫';

// Inputs are strings (what <input> holds); the output has the numbers the API expects.
const skuSchema = z.object({
  sku_id: z.string().optional(),
  name: z.string().trim().min(1, 'Nhập tên phân loại'),
  price: z.string().trim().regex(/^\d+$/, 'Nhập giá là số nguyên').transform(Number).pipe(z.number().min(1_000, PRICE_RANGE).max(1_000_000_000, PRICE_RANGE)),
  stock: z.string().trim().regex(/^\d+$/, 'Nhập tồn kho là số nguyên').transform(Number).pipe(z.number().max(100_000, 'Tồn kho tối đa 100.000')),
  active: z.boolean(),
});

export const productSchema = z
  .object({
    category_id: z.string().min(1, 'Vui lòng chọn danh mục'),
    name: z.string().trim().min(5, 'Tên sản phẩm cần từ 5 đến 200 ký tự').max(200, 'Tên sản phẩm cần từ 5 đến 200 ký tự'),
    description: z.string(),
    status: z.enum(['draft', 'active', 'archived']),
    images: z.array(z.object({ object_key: z.string(), url: z.string() })).max(9, 'Tối đa 9 ảnh'),
    skus: z.array(skuSchema).min(1, 'Cần ít nhất 1 phân loại').max(50, 'Tối đa 50 phân loại'),
  })
  .superRefine((value, ctx) => {
    const seen = new Set<string>();
    value.skus.forEach((sku, i) => {
      const key = sku.name.trim().toLowerCase();
      if (seen.has(key)) ctx.addIssue({ code: 'custom', path: ['skus', i, 'name'], message: 'Tên phân loại bị trùng' });
      seen.add(key);
    });
  });

export type ProductFormInput = z.input<typeof productSchema>;
export type ProductFormValues = z.output<typeof productSchema>;

export const emptySku = (): ProductFormInput['skus'][number] => ({ name: '', price: '', stock: '0', active: true });

export const emptyProduct = (): ProductFormInput => ({ category_id: '', name: '', description: '', status: 'active', images: [], skus: [emptySku()] });

export function fromProduct(p: Product): ProductFormInput {
  return {
    category_id: p.category_id,
    name: p.name,
    description: p.description,
    status: p.status,
    images: p.images.map((i) => ({ object_key: i.object_key, url: i.url })),
    skus: p.skus.map((s) => ({ sku_id: s.id, name: s.name, price: String(s.price), stock: String(s.stock), active: s.active })),
  };
}

/** SKUs keep their id (update); new rows have none (create); rows removed in the form are omitted (deactivated). */
export function toProductInput(v: ProductFormValues): ProductInput {
  return {
    category_id: v.category_id,
    name: v.name,
    description: v.description,
    status: v.status,
    image_keys: v.images.map((i) => i.object_key),
    skus: v.skus.map((s) => ({ ...(s.sku_id ? { id: s.sku_id } : {}), name: s.name, price: s.price, stock: s.stock, active: s.active })),
  };
}

/**
 * PATCH body with only the fields that differ from the loaded product, so an edit that does not touch SKUs
 * never writes stock.
 */
export function diffProductInput(base: ProductInput, next: ProductInput): ProductPatch {
  const patch: ProductPatch = {};
  const keys = ['category_id', 'name', 'description', 'status', 'image_keys', 'skus'] as const;
  for (const key of keys) {
    if (JSON.stringify(base[key]) !== JSON.stringify(next[key])) Object.assign(patch, { [key]: next[key] });
  }
  return patch;
}

/**
 * Adds the loaded stock of every existing SKU as `expected_stock`. The server writes `stock` only while it
 * still equals that value. If orders reserved stock after the form opened, it answers 409 CONFLICT instead of
 * silently undoing them.
 */
export function withExpectedStock(patch: ProductPatch, loaded: Sku[]): ProductPatch {
  if (!patch.skus) return patch;
  const stock = new Map(loaded.map((s) => [s.id, s.stock]));
  return {
    ...patch,
    skus: patch.skus.map((s) => {
      const expected = s.id === undefined ? undefined : stock.get(s.id);
      return expected === undefined ? s : { ...s, expected_stock: expected };
    }),
  };
}
