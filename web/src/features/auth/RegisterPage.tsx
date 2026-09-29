import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { api } from '@/api/client';
import { ApiError, applyFieldErrors, errorMessage, unwrap } from '@/api/errors';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { safeNext } from '@/lib/safe-next';

const registerSchema = z
  .object({
    full_name: z.string().trim().min(1, 'Vui lòng nhập họ tên').max(100, 'Họ tên tối đa 100 ký tự'),
    email: z.string().trim().max(254, 'Email quá dài').regex(/^[^@\s]+@[^@\s]+\.[^@\s]+$/, 'Email không hợp lệ'),
    password: z.string().min(8, 'Mật khẩu cần ít nhất 8 ký tự').max(72, 'Mật khẩu tối đa 72 ký tự'),
    confirm: z.string(),
  })
  .refine((v) => v.password === v.confirm, { path: ['confirm'], message: 'Mật khẩu nhập lại không khớp' });
type RegisterValues = z.infer<typeof registerSchema>;

export function RegisterPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const nextQuery = params.get('next') ? `?next=${encodeURIComponent(safeNext(params.get('next')))}` : '';
  const form = useForm<RegisterValues>({ resolver: zodResolver(registerSchema), defaultValues: { full_name: '', email: '', password: '', confirm: '' } });
  const { errors, isSubmitting } = form.formState;

  const onSubmit = form.handleSubmit(async ({ full_name, email, password }) => {
    try {
      await unwrap(api.POST('/api/auth/register', { body: { full_name, email, password } }));
      navigate(`/login${nextQuery}`, { state: { registeredEmail: email.toLowerCase() } });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'CONFLICT') {
        form.setError('email', { message: 'Email này đã được đăng ký.' });
        return;
      }
      if (!applyFieldErrors(err, form.setError)) form.setError('root', { message: errorMessage(err) });
    }
  });

  return (
    <section className="mx-auto max-w-sm space-y-6" aria-labelledby="register-title">
      <h1 id="register-title" className="text-3xl font-bold">Tạo tài khoản</h1>
      <form noValidate onSubmit={onSubmit} className="space-y-4 rounded-lg border bg-card p-5">
        {errors.root ? (
          <Alert variant="destructive">
            <AlertDescription>{errors.root.message}</AlertDescription>
          </Alert>
        ) : null}
        <Field id="full_name" label="Họ và tên" error={errors.full_name?.message}>
          <Input autoComplete="name" {...controlProps('full_name', errors.full_name?.message)} {...form.register('full_name')} />
        </Field>
        <Field id="email" label="Email" error={errors.email?.message}>
          <Input type="email" autoComplete="email" {...controlProps('email', errors.email?.message)} {...form.register('email')} />
        </Field>
        <Field id="password" label="Mật khẩu" hint="Từ 8 đến 72 ký tự." error={errors.password?.message}>
          <Input type="password" autoComplete="new-password" {...controlProps('password', errors.password?.message)} {...form.register('password')} />
        </Field>
        <Field id="confirm" label="Nhập lại mật khẩu" error={errors.confirm?.message}>
          <Input type="password" autoComplete="new-password" {...controlProps('confirm', errors.confirm?.message)} {...form.register('confirm')} />
        </Field>
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? 'Đang tạo tài khoản…' : 'Tạo tài khoản'}
        </Button>
      </form>
      <p className="text-sm">
        Đã có tài khoản?{' '}
        <Link to={`/login${nextQuery}`} className="font-medium text-tarp hover:underline">Đăng nhập</Link>
      </p>
    </section>
  );
}
