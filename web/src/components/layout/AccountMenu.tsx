import { useState } from 'react';
import { Link } from 'react-router';
import { Button } from '@/components/ui/button';
import { useAuth } from '@/features/auth/auth-context';

const itemClass = 'block w-full rounded-sm px-3 py-2 text-left text-sm hover:bg-accent focus-visible:bg-accent focus-visible:outline-none';

export function AccountMenu() {
  const { status, user, isSeller, logout } = useAuth();
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);

  if (status === 'loading') return null;
  if (!user) {
    return (
      <div className="flex items-center gap-1 whitespace-nowrap text-sm font-medium">
        <Link to="/login" className="shrink-0 whitespace-nowrap rounded-md px-2 py-1.5 hover:bg-accent">Đăng nhập</Link>
        <Link to="/register" className="shrink-0 whitespace-nowrap rounded-md px-2 py-1.5 hover:bg-accent">Đăng ký</Link>
      </div>
    );
  }
  return (
    <div
      className="relative"
      onKeyDown={(e) => e.key === 'Escape' && close()}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) close();
      }}
    >
      <Button variant="ghost" aria-expanded={open} aria-controls="account-menu" onClick={() => setOpen((v) => !v)} className="max-w-40 truncate">
        {user.full_name}
      </Button>
      {open ? (
        <ul id="account-menu" className="absolute right-0 z-30 mt-2 w-52 rounded-md border bg-popover p-1">
          <li><Link to="/orders" className={itemClass} onClick={close}>Đơn mua</Link></li>
          <li><Link to="/seller" className={itemClass} onClick={close}>{isSeller ? 'Kênh người bán' : 'Mở shop'}</Link></li>
          <li>
            <button type="button" className={itemClass} onClick={() => { close(); void logout(); }}>
              Đăng xuất
            </button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}
