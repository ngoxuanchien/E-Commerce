// @vitest-environment node
// Node environment: jsdom replaces the global File, which breaks undici's multipart parser in the handler.
import { expect, test } from 'vitest';
import { api } from '@/api/client';
import { unwrap } from '@/api/errors';
import { setSession } from '@/api/session';
import { SEED } from './seed';
import { getDb } from './state';

async function login(email: string) {
  setSession(await unwrap(api.POST('/api/auth/login', { body: { email, password: SEED.password } })));
}

test('presigned upload stores the image and product images resolve to it', async () => {
  await login('binh@example.com');
  const presign = await unwrap(api.POST('/api/seller/uploads', { body: { content_type: 'image/png' } }));
  // Encode the multipart body by hand so the test does not depend on a FormData implementation.
  const boundary = 'ws5-test-boundary';
  const field = (name: string, value: string) => `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`;
  const body =
    Object.entries(presign.fields).map(([k, v]) => field(k, v)).join('') +
    `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="a.png"\r\nContent-Type: image/png\r\n\r\nPNG\r\n--${boundary}--\r\n`;
  const upload = await fetch(presign.upload_url, {
    method: 'POST',
    headers: { 'Content-Type': `multipart/form-data; boundary=${boundary}` },
    body,
  });
  expect(upload.status).toBe(204);
  const product = await unwrap(
    api.POST('/api/seller/products', {
      body: { category_id: getDb().state.categories[1].children[0].id, name: 'Chén trà men ngọc', status: 'active', image_keys: [presign.object_key], skus: [{ name: 'Mặc định', price: 60_000, stock: 4 }] },
    }),
  );
  expect(product.images[0].url.startsWith('data:image/png;base64,')).toBe(true);
});
