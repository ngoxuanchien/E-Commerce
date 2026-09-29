import { describe, expect, test } from 'vitest';
import type { Product } from '@/api/types';
import { diffProductInput, emptyProduct, emptySku, fromProduct, productSchema, toProductInput } from './product-schema';

const valid = () => ({ ...emptyProduct(), category_id: 'c1', name: 'Bình gốm men lam', skus: [{ ...emptySku(), name: 'Nhỏ', price: '45000', stock: '3' }] });
const issues = (input: unknown) => {
  const r = productSchema.safeParse(input);
  return r.success ? {} : Object.fromEntries(r.error.issues.map((i) => [i.path.join('.'), i.message]));
};

describe('productSchema', () => {
  test('accepts a valid product and converts numeric strings', () => {
    expect(productSchema.parse(valid()).skus[0]).toMatchObject({ price: 45_000, stock: 3, active: true });
  });

  test.each([
    ['999', 'Giá từ 1.000 ₫ đến 1.000.000.000 ₫'],
    ['1000000001', 'Giá từ 1.000 ₫ đến 1.000.000.000 ₫'],
    ['12.5', 'Nhập giá là số nguyên'],
    ['', 'Nhập giá là số nguyên'],
  ])('rejects price %j', (price, message) => {
    expect(issues({ ...valid(), skus: [{ ...valid().skus[0], price }] })['skus.0.price']).toBe(message);
  });

  test('rejects bad stock, short names, missing category and more than 9 images', () => {
    expect(
      issues({
        ...valid(),
        category_id: '',
        name: 'Bát',
        images: Array.from({ length: 10 }, (_, i) => ({ object_key: `k${i}`, url: 'u' })),
        skus: [{ ...valid().skus[0], stock: '100001' }],
      }),
    ).toMatchObject({ category_id: 'Vui lòng chọn danh mục', name: 'Tên sản phẩm cần từ 5 đến 200 ký tự', images: 'Tối đa 9 ảnh', 'skus.0.stock': 'Tồn kho tối đa 100.000' });
  });

  test('rejects duplicate variant names and an empty SKU list', () => {
    expect(issues({ ...valid(), skus: [valid().skus[0], { ...valid().skus[0], name: ' nhỏ ' }] })['skus.1.name']).toBe('Tên phân loại bị trùng');
    expect(issues({ ...valid(), skus: [] }).skus).toBe('Cần ít nhất 1 phân loại');
  });
});

test('toProductInput maps sku_id → id and images → image_keys in display order', () => {
  const values = productSchema.parse({
    ...valid(),
    images: [{ object_key: 'shops/s/2.png', url: 'u2' }, { object_key: 'shops/s/1.png', url: 'u1' }],
    skus: [{ sku_id: 'sku-1', name: 'Nhỏ', price: '45000', stock: '3', active: false }, { name: 'Lớn', price: '55000', stock: '0', active: true }],
  });
  expect(toProductInput(values)).toEqual({
    category_id: 'c1', name: 'Bình gốm men lam', description: '', status: 'active', image_keys: ['shops/s/2.png', 'shops/s/1.png'],
    skus: [{ id: 'sku-1', name: 'Nhỏ', price: 45_000, stock: 3, active: false }, { name: 'Lớn', price: 55_000, stock: 0, active: true }],
  });
});

test('fromProduct turns a product into form input', () => {
  const product = {
    id: 'p', shop: { id: 's', name: 'S', slug: 's' }, category_id: 'c1', name: 'Bình gốm men lam', description: 'd', status: 'draft',
    images: [{ object_key: 'k', url: 'u' }], skus: [{ id: 'x', name: 'Nhỏ', price: 45_000, stock: 3, active: true }], min_price: 45_000,
    created_at: '2026-09-28T00:00:00Z', updated_at: '2026-09-28T00:00:00Z',
  } satisfies Product;
  expect(fromProduct(product)).toEqual({
    category_id: 'c1', name: 'Bình gốm men lam', description: 'd', status: 'draft', images: [{ object_key: 'k', url: 'u' }],
    skus: [{ sku_id: 'x', name: 'Nhỏ', price: '45000', stock: '3', active: true }],
  });
});

test('diffProductInput keeps only the fields that changed', () => {
  const base = toProductInput(productSchema.parse(valid()));
  expect(diffProductInput(base, { ...base, name: 'Bình gốm men lam lớn' })).toEqual({ name: 'Bình gốm men lam lớn' });
  expect(diffProductInput(base, base)).toEqual({});
  const skus = [{ ...base.skus[0], price: 50_000 }];
  expect(diffProductInput(base, { ...base, skus })).toEqual({ skus });
});
