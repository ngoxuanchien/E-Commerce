import { Link, Navigate, Outlet, useLocation } from 'react-router';
import { Awning } from '@/components/Awning';
import { PageSpinner } from '@/components/states';
import { Button } from '@/components/ui/button';
import { useAuth } from '@/features/auth/auth-context';

export function RequireAuth() {
  const { status, user } = useAuth();
  const location = useLocation();
  if (status === 'loading') return <PageSpinner />;
  if (!user) return <Navigate replace to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} />;
  return <Outlet />;
}

export function RequireSeller() {
  const { isSeller } = useAuth();
  if (isSeller) return <Outlet />;
  return (
    <section className="mx-auto max-w-md overflow-hidden rounded-lg border bg-card" aria-labelledby="no-shop-title">
      <Awning />
      <div className="space-y-3 p-6">
        <h1 id="no-shop-title" className="text-2xl font-bold">
          Bạn chưa có shop
        </h1>
        <p className="text-muted-foreground">Mở shop để đăng bán sản phẩm và nhận đơn hàng từ người mua.</p>
        <Button asChild>
          <Link to="/seller/open-shop">Mở shop của bạn</Link>
        </Button>
      </div>
    </section>
  );
}
