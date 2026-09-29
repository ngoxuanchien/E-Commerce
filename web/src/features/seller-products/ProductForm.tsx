import { zodResolver } from '@hookform/resolvers/zod';
import { type UseFormReturn, useFieldArray, useForm } from 'react-hook-form';
import { Link } from 'react-router';
import type { Category, ProductInput } from '@/api/types';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { NativeSelect } from '@/components/ui/native-select';
import { Textarea } from '@/components/ui/textarea';
import { ImageUploader } from './ImageUploader';
import { type ProductFormInput, type ProductFormValues, emptySku, productSchema, toProductInput } from './product-schema';

export type ProductFormApi = UseFormReturn<ProductFormInput, unknown, ProductFormValues>;

export function ProductForm({ initial, categories, submitLabel, onSubmit }: {
  initial: ProductFormInput;
  categories: Category[];
  submitLabel: string;
  onSubmit: (input: ProductInput, form: ProductFormApi) => Promise<void>;
}) {
  const form = useForm<ProductFormInput, unknown, ProductFormValues>({ resolver: zodResolver(productSchema), defaultValues: initial });
  const skus = useFieldArray({ control: form.control, name: 'skus' });
  const images = useFieldArray({ control: form.control, name: 'images' });
  const { errors, isSubmitting } = form.formState;
  const submit = form.handleSubmit((values) => onSubmit(toProductInput(values), form));
  const skusError = errors.skus?.message ?? errors.skus?.root?.message;

  return (
    <form noValidate onSubmit={submit} className="space-y-6">
      {errors.root ? (
        <Alert variant="destructive">
          <AlertDescription>{errors.root.message}</AlertDescription>
        </Alert>
      ) : null}
      <section aria-labelledby="basic-title" className="space-y-4 rounded-lg border bg-card p-5">
        <h2 id="basic-title" className="text-lg font-semibold">Thông tin cơ bản</h2>
        <Field id="category_id" label="Danh mục" error={errors.category_id?.message}>
          <NativeSelect {...controlProps('category_id', errors.category_id?.message)} {...form.register('category_id')}>
            <option value="">Chọn danh mục</option>
            {categories.map((c) =>
              c.children.length ? (
                <optgroup key={c.id} label={c.name}>
                  {c.children.map((child) => (
                    <option key={child.id} value={child.id}>{child.name}</option>
                  ))}
                </optgroup>
              ) : (
                <option key={c.id} value={c.id}>{c.name}</option>
              ),
            )}
          </NativeSelect>
        </Field>
        <Field id="name" label="Tên sản phẩm" error={errors.name?.message}>
          <Input {...controlProps('name', errors.name?.message)} {...form.register('name')} />
        </Field>
        <Field id="description" label="Mô tả" error={errors.description?.message}>
          <Textarea rows={5} {...controlProps('description', errors.description?.message)} {...form.register('description')} />
        </Field>
        <Field id="status" label="Trạng thái" hint="Chỉ sản phẩm “Đang bán” mới hiện với người mua.">
          <NativeSelect id="status" {...form.register('status')}>
            <option value="active">Đang bán</option>
            <option value="draft">Nháp</option>
            <option value="archived">Đã lưu trữ</option>
          </NativeSelect>
        </Field>
      </section>

      <section aria-labelledby="images-title" className="space-y-3 rounded-lg border bg-card p-5">
        <h2 id="images-title" className="text-lg font-semibold">Ảnh sản phẩm</h2>
        <ImageUploader images={images.fields} onAdd={(img) => images.append(img)} onRemove={images.remove} onMove={images.move} />
        {errors.images?.message ? <p className="text-sm text-destructive">{errors.images.message}</p> : null}
      </section>

      <section aria-labelledby="skus-title" className="space-y-3 rounded-lg border bg-card p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 id="skus-title" className="text-lg font-semibold">Phân loại hàng</h2>
          <Button type="button" variant="outline" size="sm" disabled={skus.fields.length >= 50} onClick={() => skus.append(emptySku())}>
            Thêm phân loại
          </Button>
        </div>
        <p className="text-sm text-muted-foreground">Phân loại bị xoá sẽ ngừng bán nhưng vẫn còn trong lịch sử đơn hàng.</p>
        {skusError ? <p className="text-sm text-destructive">{skusError}</p> : null}
        <ul className="space-y-3">
          {skus.fields.map((field, i) => {
            const e = errors.skus?.[i];
            const n = i + 1;
            return (
              <li key={field.id} className="grid gap-3 rounded-md border p-3 sm:grid-cols-[1.5fr_1fr_1fr_auto_auto] sm:items-end">
                <Field id={`skus.${i}.name`} label={<>Tên phân loại<span className="sr-only"> {n}</span></>} error={e?.name?.message}>
                  <Input {...controlProps(`skus.${i}.name`, e?.name?.message)} {...form.register(`skus.${i}.name`)} />
                </Field>
                <Field id={`skus.${i}.price`} label={<>Giá (₫)<span className="sr-only"> phân loại {n}</span></>} error={e?.price?.message}>
                  <Input inputMode="numeric" {...controlProps(`skus.${i}.price`, e?.price?.message)} {...form.register(`skus.${i}.price`)} />
                </Field>
                <Field id={`skus.${i}.stock`} label={<>Tồn kho<span className="sr-only"> phân loại {n}</span></>} error={e?.stock?.message}>
                  <Input inputMode="numeric" {...controlProps(`skus.${i}.stock`, e?.stock?.message)} {...form.register(`skus.${i}.stock`)} />
                </Field>
                <label className="flex items-center gap-2 text-sm sm:pb-2">
                  <input type="checkbox" className="size-4 accent-[var(--tarp)]" {...form.register(`skus.${i}.active`)} />
                  Đang bán<span className="sr-only"> phân loại {n}</span>
                </label>
                <Button type="button" variant="ghost" size="sm" aria-label={`Xoá phân loại ${n}`} disabled={skus.fields.length === 1} onClick={() => skus.remove(i)}>
                  Xoá
                </Button>
              </li>
            );
          })}
        </ul>
      </section>

      <div className="flex justify-end gap-2">
        <Button variant="ghost" asChild>
          <Link to="/seller/products">Huỷ</Link>
        </Button>
        <Button type="submit" disabled={isSubmitting}>{isSubmitting ? 'Đang lưu…' : submitLabel}</Button>
      </div>
    </form>
  );
}
