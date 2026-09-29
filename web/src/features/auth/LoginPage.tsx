import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import { Link, Navigate, useLocation, useNavigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { errorMessage } from '@/api/errors';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { safeNext } from '@/lib/safe-next';
import { useAuth } from './auth-context';

const loginSchema = z.object({
  email: z.string().trim().min(1, 'Vui lòng nhập email'),
  password: z.string().min(1, 'Vui lòng nhập mật khẩu'),
});
type LoginValues = z.infer<typeof loginSchema>;

export function LoginPage() {
  const { user, login } = useAuth();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const location = useLocation();
  const next = safeNext(params.get('next'));
  const registeredEmail = (location.state as { registeredEmail?: string } | null)?.registeredEmail;
  const form = useForm<LoginValues>({ resolver: zodResolver(loginSchema), defaultValues: { email: registeredEmail ?? '', password: '' } });
  const { errors, isSubmitting, submitCount } = form.formState;

  if (user && submitCount === 0) return <Navigate to={next} replace />;

  const onSubmit = form.handleSubmit(async ({ email, password }) => {
    try {
      await login(email, password);
      navigate(next, { replace: true });
    } catch (err) {
      form.setError('root', { message: errorMessage(err, { UNAUTHENTICATED: 'Email hoặc mật khẩu không đúng.' }) });
    }
  });

  const registerHref = params.get('next') ? `/register?next=${encodeURIComponent(next)}` : '/register';
  return (
    <section className="mx-auto max-w-sm space-y-6" aria-labelledby="login-title">
      <h1 id="login-title" className="text-3xl font-bold">Đăng nhập</h1>
      {registeredEmail ? (
        <Alert>
          <AlertDescription>Tạo tài khoản thành công. Mời bạn đăng nhập.</AlertDescription>
        </Alert>
      ) : null}
      <form noValidate onSubmit={onSubmit} className="space-y-4 rounded-lg border bg-card p-5">
        {errors.root ? (
          <Alert variant="destructive">
            <AlertDescription>{errors.root.message}</AlertDescription>
          </Alert>
        ) : null}
        <Field id="email" label="Email" error={errors.email?.message}>
          <Input type="email" autoComplete="email" {...controlProps('email', errors.email?.message)} {...form.register('email')} />
        </Field>
        <Field id="password" label="Mật khẩu" error={errors.password?.message}>
          <Input type="password" autoComplete="current-password" {...controlProps('password', errors.password?.message)} {...form.register('password')} />
        </Field>
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? 'Đang đăng nhập…' : 'Đăng nhập'}
        </Button>
      </form>
      <p className="text-sm">
        Chưa có tài khoản?{' '}
        <Link to={registerHref} className="font-medium text-tarp hover:underline">Đăng ký</Link>
      </p>
    </section>
  );
}
