import { screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { renderApp } from '@/test/render';

test('edits the shop name; the slug is read-only and saving needs a change', async () => {
  const { user } = renderApp('/seller/shop', { as: 'binh@example.com' });
  const name = await screen.findByLabelText('Tên shop');
  expect(name).toHaveValue('Gốm Bát Tràng Cô Bình');
  expect(screen.getByText('/shops/gom-bat-trang')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Lưu thay đổi' })).toBeDisabled();
  await user.clear(name);
  await user.type(name, 'Gốm Bát Tràng Bình Trần');
  await user.click(screen.getByRole('button', { name: 'Lưu thay đổi' }));
  expect(await screen.findByText('Đã lưu thay đổi')).toBeInTheDocument();
  await waitFor(() => expect(getDb().state.shops.find((s) => s.id === SEED.shops.gom)!.name).toBe('Gốm Bát Tràng Bình Trần'));
});
