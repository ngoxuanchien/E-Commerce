import { ArrowLeft, ArrowRight, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { errorMessage } from '@/api/errors';
import { Button, buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { uploadImage } from './upload';

type Image = { object_key: string; url: string };
type Uploading = { id: string; name: string; progress: number };

const ACCEPTED = ['image/jpeg', 'image/png', 'image/webp'];
const MAX_BYTES = 5 * 1024 * 1024;

export function ImageUploader({ images, onAdd, onRemove, onMove, max = 9 }: {
  images: Image[];
  onAdd: (image: Image) => void;
  onRemove: (index: number) => void;
  onMove: (from: number, to: number) => void;
  max?: number;
}) {
  const [uploading, setUploading] = useState<Uploading[]>([]);
  const remaining = max - images.length - uploading.length;

  const uploadOne = async (file: File) => {
    if (!ACCEPTED.includes(file.type)) {
      toast.error(`${file.name}: chỉ hỗ trợ ảnh JPG, PNG hoặc WEBP.`);
      return;
    }
    if (file.size > MAX_BYTES) {
      toast.error(`${file.name}: ảnh vượt quá 5 MB.`);
      return;
    }
    const id = crypto.randomUUID();
    setUploading((list) => [...list, { id, name: file.name, progress: 0 }]);
    try {
      const result = await uploadImage(file, (progress) => setUploading((list) => list.map((u) => (u.id === id ? { ...u, progress } : u))));
      // Preview from the local file; the saved product returns the public URL.
      onAdd({ object_key: result.object_key, url: URL.createObjectURL(file) });
    } catch (err) {
      toast.error(`Không tải được ảnh ${file.name}. ${errorMessage(err)}`);
    } finally {
      setUploading((list) => list.filter((u) => u.id !== id));
    }
  };

  const onFiles = (files: FileList | null) => {
    if (!files) return;
    const list = Array.from(files);
    if (list.length > remaining) toast.error(`Mỗi sản phẩm có tối đa ${max} ảnh.`);
    // One at a time, so images are appended in the order they were picked.
    void list.slice(0, Math.max(0, remaining)).reduce((chain, file) => chain.then(() => uploadOne(file)), Promise.resolve());
  };

  return (
    <div className="space-y-3">
      {images.length + uploading.length > 0 ? (
        <ol className="grid grid-cols-3 gap-3 sm:grid-cols-5">
          {images.map((img, i) => (
            <li key={img.object_key} className="space-y-1">
              <div className="relative aspect-square overflow-hidden rounded-md border bg-muted">
                <img src={img.url} alt={`Ảnh ${i + 1}`} className="size-full object-cover" />
                {i === 0 ? <span className="absolute left-1 top-1 rounded bg-tarp px-1.5 text-xs font-medium text-white">Ảnh bìa</span> : null}
              </div>
              <div className="flex justify-between">
                <Button type="button" variant="ghost" size="icon" aria-label={`Đưa ảnh ${i + 1} lên trước`} disabled={i === 0} onClick={() => onMove(i, i - 1)}>
                  <ArrowLeft aria-hidden="true" />
                </Button>
                <Button type="button" variant="ghost" size="icon" aria-label={`Đưa ảnh ${i + 1} ra sau`} disabled={i === images.length - 1} onClick={() => onMove(i, i + 1)}>
                  <ArrowRight aria-hidden="true" />
                </Button>
                <Button type="button" variant="ghost" size="icon" aria-label={`Xoá ảnh ${i + 1}`} onClick={() => onRemove(i)}>
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
            </li>
          ))}
          {uploading.map((u) => (
            <li key={u.id} className="flex aspect-square flex-col justify-center gap-2 rounded-md border border-dashed p-2 text-xs">
              <span className="truncate">{u.name}</span>
              <progress value={u.progress} max={100} aria-label={`Đang tải ${u.name}`} className="w-full" />
            </li>
          ))}
        </ol>
      ) : null}
      <div className="flex flex-wrap items-center gap-3">
        <label className={cn(buttonVariants({ variant: 'outline' }), 'has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring', remaining <= 0 && 'pointer-events-none opacity-50')}>
          Thêm ảnh
          <input
            type="file"
            accept={ACCEPTED.join(',')}
            multiple
            className="sr-only"
            disabled={remaining <= 0}
            onChange={(e) => {
              onFiles(e.target.files);
              e.target.value = '';
            }}
          />
        </label>
        <p className="text-sm text-muted-foreground">{images.length}/{max} ảnh. Ảnh đầu tiên là ảnh bìa.</p>
      </div>
    </div>
  );
}
