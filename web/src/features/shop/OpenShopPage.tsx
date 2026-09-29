import { zodResolver } from '@hookform/resolvers/zod';
import { useForm, useWatch } from 'react-hook-form';
import { Navigate, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { z } from 'zod';
import { useCreateShop } from '@/api/hooks/seller';
import { ApiError, applyFieldErrors, errorMessage, isRetryable } from '@/api/errors';
import { Awning } from '@/components/Awning';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { useAuth } from '@/features/auth/auth-context';
import { slugify } from '@/lib/vi';

const RESERVED_SLUGS = ['me', 'new', 'admin', 'seller'];
const schema = z.object({
  name: z.string().trim().min(3, 'Tên shop cần từ 3 đến 80 ký tự').max(80, 'Tên shop cần từ 3 đến 80 ký tự'),
  slug: z
    .string()
    .regex(/^[a-z0-9-]{3,50}$/, 'Chỉ dùng chữ thường không dấu, số và dấu gạch ngang (3 đến 50 ký tự)')
    .refine((s) => !RESERVED_SLUGS.includes(s), 'Địa chỉ này không được phép dùng'),
  description: z.string().max(1000, 'Giới thiệu tối đa 1000 ký tự'),
});
type Values = z.infer<typeof schema>;

export function OpenShopPage() {
  const { isSeller, reloadSession } = useAuth();
  const navigate = useNavigate();
  const createShop = useCreateShop();
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { name: '', slug: '', description: '' } });
  const slug = useWatch({ control: form.control, name: 'slug' });
  const { errors, isSubmitting } = form.formState;

  if (isSeller && !isSubmitting) return <Navigate to="/seller/products" replace />;

  // The slug follows the name until the user types into it (setValue does not mark it dirty).
  const nameField = form.register('name', {
    onChange: (e: { target: { value: string } }) => {
      if (!form.getFieldState('slug').isDirty) form.setValue('slug', slugify(e.target.value), { shouldValidate: form.formState.isSubmitted });
    },
  });
  const slugField = form.register('slug');

  const onSubmit = form.handleSubmit(async (values) => {
    try {
      await createShop.mutateAsync(values);
      await reloadSession(); // the new access token carries the seller role
      toast.success('Đã mở shop. Hãy đăng sản phẩm đầu tiên.');
      navigate('/seller/products', { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'CONFLICT') {
        form.setError('slug', { message: 'Địa chỉ shop này đã có người dùng.' });
        return;
      }
      if (!applyFieldErrors(err, form.setError) && !isRetryable(err)) form.setError('root', { message: errorMessage(err) });
    }
  });

  return (
    <section aria-labelledby="open-shop-title" className="mx-auto max-w-lg overflow-hidden rounded-lg border bg-card">
      <Awning />
      <form noValidate onSubmit={onSubmit} className="space-y-4 p-6">
        <h1 id="open-shop-title" className="text-2xl font-bold">Mở shop của bạn</h1>
        <p className="text-sm text-muted-foreground">Sau khi mở shop, bạn có thể đăng sản phẩm và nhận đơn hàng.</p>
        {errors.root ? (
          <Alert variant="destructive">
            <AlertDescription>{errors.root.message}</AlertDescription>
          </Alert>
        ) : null}
        <Field id="name" label="Tên shop" error={errors.name?.message}>
          <Input {...controlProps('name', errors.name?.message)} {...nameField} />
        </Field>
        <Field id="slug" label="Địa chỉ shop" hint={`Trang shop của bạn: /shops/${slug || 'ten-shop'}. Không đổi được sau khi mở.`} error={errors.slug?.message}>
          <Input autoCapitalize="none" spellCheck={false} {...controlProps('slug', errors.slug?.message)} {...slugField} />
        </Field>
        <Field id="description" label="Giới thiệu shop" error={errors.description?.message}>
          <Textarea rows={3} {...controlProps('description', errors.description?.message)} {...form.register('description')} />
        </Field>
        <Button type="submit" className="w-full" disabled={isSubmitting}>{isSubmitting ? 'Đang mở shop…' : 'Mở shop'}</Button>
      </form>
    </section>
  );
}
