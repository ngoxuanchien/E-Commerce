import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import { useMyShop, useUpdateMyShop } from '@/api/hooks/seller';
import { applyFieldErrors, errorMessage, isRetryable } from '@/api/errors';
import type { Shop } from '@/api/types';
import { ErrorState, PageSpinner } from '@/components/states';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';

const schema = z.object({
  name: z.string().trim().min(3, 'Tên shop cần từ 3 đến 80 ký tự').max(80, 'Tên shop cần từ 3 đến 80 ký tự'),
  description: z.string().max(1000, 'Giới thiệu tối đa 1000 ký tự'),
});
type Values = z.infer<typeof schema>;

export function SellerShopPage() {
  const shop = useMyShop();
  if (shop.isPending) return <PageSpinner />;
  if (shop.isError) return <ErrorState error={shop.error} onRetry={() => void shop.refetch()} />;
  return <ShopProfileForm key={`${shop.data.name}|${shop.data.description}`} shop={shop.data} />;
}

function ShopProfileForm({ shop }: { shop: Shop }) {
  const update = useUpdateMyShop();
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { name: shop.name, description: shop.description } });
  const { errors, isDirty, isSubmitting, dirtyFields } = form.formState;
  const onSubmit = form.handleSubmit(async (values) => {
    try {
      // PATCH only what changed (ShopUpdate needs at least one property).
      await update.mutateAsync({
        ...(dirtyFields.name ? { name: values.name } : {}),
        ...(dirtyFields.description ? { description: values.description } : {}),
      });
      toast.success('Đã lưu thay đổi');
    } catch (err) {
      if (!applyFieldErrors(err, form.setError) && !isRetryable(err)) form.setError('root', { message: errorMessage(err) });
    }
  });
  return (
    <form noValidate onSubmit={onSubmit} className="max-w-xl space-y-4">
      <h1 className="text-2xl font-bold">Hồ sơ shop</h1>
      {errors.root ? (
        <Alert variant="destructive">
          <AlertDescription>{errors.root.message}</AlertDescription>
        </Alert>
      ) : null}
      <div className="space-y-4 rounded-lg border bg-card p-5">
        <Field id="name" label="Tên shop" error={errors.name?.message}>
          <Input {...controlProps('name', errors.name?.message)} {...form.register('name')} />
        </Field>
        <div className="grid gap-1.5">
          <span className="text-sm font-medium">Địa chỉ shop</span>
          <p className="text-sm text-muted-foreground">/shops/{shop.slug}</p>
        </div>
        <Field id="description" label="Giới thiệu shop" error={errors.description?.message}>
          <Textarea rows={4} {...controlProps('description', errors.description?.message)} {...form.register('description')} />
        </Field>
      </div>
      <Button type="submit" disabled={!isDirty || isSubmitting}>{isSubmitting ? 'Đang lưu…' : 'Lưu thay đổi'}</Button>
    </form>
  );
}
