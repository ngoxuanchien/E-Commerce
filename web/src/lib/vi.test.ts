import { expect, test } from 'vitest';
import { normalizeVi, slugify } from './vi';

test('normalizeVi strips diacritics including đ and lowercases', () => {
  expect(normalizeVi('Áo Thun Đà Lạt')).toBe('ao thun da lat');
  expect(normalizeVi('Bát ăn cơm')).toBe('bat an com');
});

test('slugify builds shop slugs that satisfy ^[a-z0-9-]{3,50}$', () => {
  expect(slugify('Gốm Bát Tràng Cô Bình')).toBe('gom-bat-trang-co-binh');
  expect(slugify('  Đồ Da -- Đà Lạt!! ')).toBe('do-da-da-lat');
  expect(slugify('x'.repeat(80))).toHaveLength(50);
});
