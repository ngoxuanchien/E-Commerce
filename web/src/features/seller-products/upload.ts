import { api } from '@/api/client';
import { ApiError, unwrap } from '@/api/errors';
import type { UploadRequest } from '@/api/types';

/** Presigned POST (WS2): ask the API for a form, then send the file straight to object storage. */
export async function uploadImage(file: File, onProgress: (percent: number) => void): Promise<{ object_key: string; public_url: string }> {
  const presign = await unwrap(api.POST('/api/seller/uploads', { body: { content_type: file.type as UploadRequest['content_type'] } }));
  const form = new FormData();
  for (const [key, value] of Object.entries(presign.fields)) form.append(key, value);
  form.append('file', file); // must come after the policy fields
  await postWithProgress(presign.upload_url, form, onProgress);
  return { object_key: presign.object_key, public_url: presign.public_url };
}

function postWithProgress(url: string, body: FormData, onProgress: (percent: number) => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest(); // fetch has no upload progress events
    xhr.open('POST', url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress(100);
        resolve();
      } else {
        reject(new ApiError(xhr.status, xhr.status === 413 ? 'PAYLOAD_TOO_LARGE' : 'INTERNAL', `upload failed with ${xhr.status}`));
      }
    };
    xhr.onerror = () => reject(new ApiError(0, 'UPSTREAM_UNAVAILABLE', 'upload network error'));
    xhr.send(body);
  });
}
