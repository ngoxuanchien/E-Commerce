import { useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { toast } from 'sonner';
import { useArchiveSellerProduct, useSellerProducts } from '@/api/hooks/seller';
import { errorMessage, isRetryable } from '@/api/errors';
import type { Product } from '@/api/types';
import { LoadMore } from '@/components/LoadMore';
import { Price } from '@/components/Price';
import { StatusTabs } from '@/components/StatusTabs';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { PRODUCT_STATUS_LABEL, PRODUCT_STATUS_TABS, parseProductStatus } from './product-status';

const activeStock = (p: Product) => p.skus.filter((s) => s.active).reduce((sum, s) => sum + s.stock, 0);

export function SellerProductsPage() {
  const [params, setParams] = useSearchParams();
  const status = parseProductStatus(params.get('status'));
  const products = useSellerProducts(status);
  const items = products.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-bold">Sản phẩm</h1>
        <Button asChild>
          <Link to="/seller/products/new">Thêm sản phẩm</Link>
        </Button>
      </div>
      <StatusTabs label="Lọc theo trạng thái" options={PRODUCT_STATUS_TABS} value={status} onChange={(s) => setParams(s ? { status: s } : {})} />
      {products.isPending ? (
        <PageSpinner />
      ) : products.isError ? (
        <ErrorState error={products.error} onRetry={() => void products.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="Chưa có sản phẩm nào ở trạng thái này.">
          <Link to="/seller/products/new" className="font-medium text-tarp hover:underline">Thêm sản phẩm đầu tiên</Link>
        </EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full text-sm">
            <caption className="sr-only">Danh sách sản phẩm</caption>
            <thead className="border-b text-left text-muted-foreground">
              <tr>
                <th scope="col" className="p-3 font-medium">Sản phẩm</th>
                <th scope="col" className="hidden p-3 font-medium sm:table-cell">Trạng thái</th>
                <th scope="col" className="p-3 font-medium">Giá từ</th>
                <th scope="col" className="hidden p-3 font-medium md:table-cell">Tồn kho</th>
                <th scope="col" className="p-3"><span className="sr-only">Thao tác</span></th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {items.map((p) => (
                <tr key={p.id}>
                  <th scope="row" className="p-3 text-left font-normal">
                    <div className="flex items-center gap-3">
                      <div className="size-12 shrink-0 overflow-hidden rounded-md border bg-muted">
                        {p.images[0] ? <img src={p.images[0].url} alt="" className="size-full object-cover" /> : null}
                      </div>
                      <div className="min-w-0">
                        <p className="line-clamp-2 font-medium">{p.name}</p>
                        <p className="text-xs text-muted-foreground sm:hidden">{PRODUCT_STATUS_LABEL[p.status]}</p>
                      </div>
                    </div>
                  </th>
                  <td className="hidden p-3 sm:table-cell"><Badge variant="secondary">{PRODUCT_STATUS_LABEL[p.status]}</Badge></td>
                  <td className="p-3"><Price value={p.min_price} /></td>
                  <td className="hidden p-3 tabular-nums md:table-cell">{activeStock(p)}</td>
                  <td className="p-3">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" asChild>
                        <Link to={`/seller/products/${p.id}`} aria-label={`Sửa ${p.name}`}>Sửa</Link>
                      </Button>
                      {p.status !== 'archived' ? <ArchiveButton product={p} /> : null}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <LoadMore hasNextPage={products.hasNextPage} isFetchingNextPage={products.isFetchingNextPage} onLoadMore={() => void products.fetchNextPage()} />
    </div>
  );
}

function ArchiveButton({ product }: { product: Product }) {
  const [open, setOpen] = useState(false);
  const archive = useArchiveSellerProduct();
  const confirm = () =>
    archive.mutate(product.id, {
      onSuccess: () => {
        setOpen(false);
        toast.success('Đã lưu trữ sản phẩm');
      },
      onError: (err) => {
        if (!isRetryable(err)) toast.error(errorMessage(err));
      },
    });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="text-chili" aria-label={`Lưu trữ ${product.name}`}>Lưu trữ</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Lưu trữ sản phẩm?</DialogTitle>
          <DialogDescription>“{product.name}” sẽ ẩn khỏi cửa hàng. Bạn có thể bán lại bằng cách đổi trạng thái sang Đang bán.</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>Giữ lại</Button>
          <Button variant="destructive" onClick={confirm} disabled={archive.isPending}>Lưu trữ</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
