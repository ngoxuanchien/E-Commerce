import { Button } from '@/components/ui/button';

export function LoadMore({ hasNextPage, isFetchingNextPage, onLoadMore }: { hasNextPage: boolean; isFetchingNextPage: boolean; onLoadMore: () => void }) {
  if (!hasNextPage) return null;
  return (
    <div className="flex justify-center py-4">
      <Button variant="outline" onClick={onLoadMore} disabled={isFetchingNextPage}>
        {isFetchingNextPage ? 'Đang tải…' : 'Xem thêm'}
      </Button>
    </div>
  );
}
