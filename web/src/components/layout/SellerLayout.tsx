import { Link, NavLink, Outlet } from 'react-router';
import { useMyShop } from '@/api/hooks/seller';
import { Awning } from '@/components/Awning';
import { cn } from '@/lib/utils';

const NAV = [
  { to: '/seller/products', label: 'Sản phẩm' },
  { to: '/seller/orders', label: 'Đơn hàng' },
  { to: '/seller/wallet', label: 'Ví' },
  { to: '/seller/payouts', label: 'Rút tiền' },
  { to: '/seller/shop', label: 'Hồ sơ shop' },
];

export function SellerLayout() {
  const shop = useMyShop();
  return (
    <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
      <aside className="space-y-3">
        <div className="overflow-hidden rounded-lg border bg-card">
          <Awning />
          <div className="space-y-0.5 p-3">
            <p className="text-xs text-muted-foreground">Kênh người bán</p>
            <p className="font-semibold">{shop.data?.name ?? 'Đang tải…'}</p>
            {shop.data ? <Link to={`/shops/${shop.data.slug}`} className="text-sm text-tarp hover:underline">Xem trang shop</Link> : null}
          </div>
        </div>
        <nav aria-label="Kênh người bán" className="-mx-4 flex gap-1 overflow-x-auto px-4 lg:mx-0 lg:flex-col lg:px-0">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) => cn('shrink-0 rounded-md px-3 py-2 text-sm font-medium', isActive ? 'bg-tarp text-white' : 'hover:bg-accent')}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  );
}
