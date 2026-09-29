import { HttpResponse, http } from 'msw';
import { MOCK_ASSET_BASE, type MockDb } from '../db';

const MAX_BYTES = 5 * 1024 * 1024;

async function toDataUrl(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = '';
  for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return `data:${file.type || 'application/octet-stream'};base64,${btoa(binary)}`;
}

/** Stands in for the MinIO presigned POST target (not part of the gateway OpenAPI). */
export function uploadHandlers(db: () => MockDb) {
  return [
    http.post(MOCK_ASSET_BASE, async ({ request }) => {
      const form = await request.formData();
      const key = form.get('key');
      const file = form.get('file');
      // Checked structurally: File classes differ between the browser, jsdom and Node.
      if (typeof key !== 'string' || file === null || typeof file === 'string') return new HttpResponse('missing key or file', { status: 400 });
      if (file.size > MAX_BYTES) return new HttpResponse('EntityTooLarge', { status: 400 });
      db().storeBlob(key, await toDataUrl(file));
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
