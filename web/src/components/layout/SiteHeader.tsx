import { ShoppingBasket } from 'lucide-react';
import type { FormEvent } from 'react';
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router';
import { cartItemCount, useCart } from '@/api/hooks/cart';
import { Awning } from '@/components/Awning';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuth } from '@/features/auth/auth-context';
import { AccountMenu } from './AccountMenu';

export function SiteHeader() {
  const { user } = useAuth();
  const cart = useCart(!!user);
  const count = user ? cartItemCount(cart.data) : 0;
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const currentQuery = location.pathname === '/search' ? (params.get('q') ?? '') : '';

  const onSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const q = String(new FormData(event.currentTarget).get('q') ?? '').trim();
    navigate(q ? `/search?q=${encodeURIComponent(q)}` : '/search');
  };

  return (
    <header className="bg-card">
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2">
        Bỏ qua đến nội dung chính
      </a>
      <div className="mx-auto grid max-w-6xl grid-cols-[auto_1fr] items-center gap-x-4 gap-y-3 px-4 py-3 md:grid-cols-[auto_1fr_auto]">
        <Link to="/" className="whitespace-nowrap font-display text-xl font-bold tracking-tight text-tarp sm:text-2xl">
          Chợ Phiên
        </Link>
        <nav aria-label="Tài khoản" className="flex items-center justify-self-end gap-1 md:col-start-3 md:row-start-1">
          <Link to="/cart" aria-label={`Giỏ hàng, ${count} sản phẩm`} className="inline-flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm font-medium hover:bg-accent">
            <ShoppingBasket aria-hidden="true" className="size-5" />
            <span className="hidden sm:inline">Giỏ hàng</span>
            {count > 0 ? <span className="rounded-full bg-chili px-1.5 text-xs font-semibold text-white">{count}</span> : null}
          </Link>
          <AccountMenu />
        </nav>
        <form role="search" onSubmit={onSearch} className="col-span-2 flex gap-2 md:col-span-1 md:col-start-2 md:row-start-1">
          <Input key={currentQuery} type="search" name="q" defaultValue={currentQuery} aria-label="Tìm sản phẩm" placeholder="Tìm áo thun, gốm Bát Tràng…" className="flex-1" />
          <Button type="submit">Tìm</Button>
        </form>
      </div>
      <Awning />
    </header>
  );
}
