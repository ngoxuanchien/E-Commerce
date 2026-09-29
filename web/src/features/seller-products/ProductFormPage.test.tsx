import { screen, waitFor, within } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import { beforeEach, expect, test, vi } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { server } from '@/test/server';
import { currentPath, renderApp } from '@/test/render';
import { uploadImage } from './upload';

vi.mock('./upload', () => ({ uploadImage: vi.fn() }));

beforeEach(() => {
  let n = 0;
  vi.mocked(uploadImage).mockImplementation(async (_file, onProgress) => {
    onProgress(40);
    onProgress(100);
    n += 1;
    return { object_key: `shops/${SEED.shops.gom}/test-${n}.png`, public_url: `http://localhost:9000/product-images/test-${n}.png` };
  });
});

const png = (name: string) => new File([new Uint8Array([137, 80, 78, 71])], name, { type: 'image/png' });

test('validates on the client before sending anything', async () => {
  const { user } = renderApp('/seller/products/new', { as: 'binh@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Tạo sản phẩm' }));
  expect(await screen.findByText('Vui lòng chọn danh mục')).toBeInTheDocument();
  expect(screen.getByText('Tên sản phẩm cần từ 5 đến 200 ký tự')).toBeInTheDocument();
  expect(screen.getByText('Nhập tên phân loại')).toBeInTheDocument();
  expect(screen.getByText('Nhập giá là số nguyên')).toBeInTheDocument();
});

test('creates a product with images in the chosen order and two SKUs', async () => {
  const { user } = renderApp('/seller/products/new', { as: 'binh@example.com' });
  await user.selectOptions(await screen.findByLabelText('Danh mục'), 'Gốm sứ');
  await user.type(screen.getByLabelText('Tên sản phẩm'), 'Chén trà men ngọc');
  await user.upload(screen.getByLabelText('Thêm ảnh'), [png('a.png'), png('b.png')]);
  expect(await screen.findByRole('img', { name: 'Ảnh 2' })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Đưa ảnh 2 lên trước' }));

  await user.type(screen.getByLabelText('Tên phân loại 1'), 'Chén nhỏ');
  await user.type(screen.getByLabelText('Giá (₫) phân loại 1'), '60000');
  await user.click(screen.getByRole('button', { name: 'Thêm phân loại' }));
  await user.type(screen.getByLabelText('Tên phân loại 2'), 'Chén lớn');
  await user.type(screen.getByLabelText('Giá (₫) phân loại 2'), '80000');
  await user.clear(screen.getByLabelText('Tồn kho phân loại 2'));
  await user.type(screen.getByLabelText('Tồn kho phân loại 2'), '7');
  await user.click(screen.getByRole('button', { name: 'Tạo sản phẩm' }));

  await waitFor(() => expect(currentPath()).toBe('/seller/products'));
  const created = getDb().state.products.find((p) => p.name === 'Chén trà men ngọc')!;
  expect(created.image_keys).toEqual([`shops/${SEED.shops.gom}/test-2.png`, `shops/${SEED.shops.gom}/test-1.png`]);
  expect(created.skus.map((s) => [s.name, s.price, s.stock])).toEqual([['Chén nhỏ', 60_000, 0], ['Chén lớn', 80_000, 7]]);
});

test('editing: a removed SKU is deactivated on the server', async () => {
  const { user } = renderApp(`/seller/products/${SEED.products.bowl}`, { as: 'binh@example.com' });
  expect(await screen.findByLabelText('Tên sản phẩm')).toHaveValue('Bát ăn cơm men lam Bát Tràng');
  const price = screen.getByLabelText('Giá (₫) phân loại 1');
  await user.clear(price);
  await user.type(price, '50000');
  await user.click(screen.getByRole('button', { name: 'Xoá phân loại 2' }));
  await user.click(screen.getByRole('button', { name: 'Lưu thay đổi' }));
  await waitFor(() => expect(currentPath()).toBe('/seller/products'));
  const skus = getDb().state.products.find((p) => p.id === SEED.products.bowl)!.skus;
  expect(skus.find((s) => s.id === SEED.skus.bowl11)).toMatchObject({ price: 50_000, active: true });
  expect(skus.find((s) => s.id === SEED.skus.bowl13)).toMatchObject({ active: false });
});

test('server VALIDATION_ERROR fields appear on the matching SKU row', async () => {
  server.use(
    http.patch('*/api/seller/products/:id', () =>
      HttpResponse.json({ error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { 'skus[0].price': 'too low' } } } }, { status: 400 }),
    ),
  );
  const { user } = renderApp(`/seller/products/${SEED.products.bowl}`, { as: 'binh@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Lưu thay đổi' }));
  const row = screen.getByLabelText('Giá (₫) phân loại 1').closest('li')!;
  expect(await within(row).findByText('Giá trị không hợp lệ')).toBeInTheDocument();
  expect(screen.getByText('Dữ liệu chưa hợp lệ. Vui lòng kiểm tra các trường được đánh dấu.')).toBeInTheDocument();
});

test('rejects unsupported and oversized images before uploading', async () => {
  const { user } = renderApp('/seller/products/new', { as: 'binh@example.com' });
  const gif = new File(['x'], 'a.gif', { type: 'image/gif' });
  const huge = new File([new Uint8Array(5 * 1024 * 1024 + 1)], 'big.png', { type: 'image/png' });
  await user.upload(await screen.findByLabelText('Thêm ảnh'), [gif, huge]);
  expect(await screen.findByText('a.gif: chỉ hỗ trợ ảnh JPG, PNG hoặc WEBP.')).toBeInTheDocument();
  expect(await screen.findByText('big.png: ảnh vượt quá 5 MB.')).toBeInTheDocument();
  expect(uploadImage).not.toHaveBeenCalled();
});
