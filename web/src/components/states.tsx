import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { errorMessage } from '@/api/errors';
import { Button } from '@/components/ui/button';

export function PageSpinner({ label = 'Đang tải…' }: { label?: string }) {
  return (
    <div role="status" className="flex justify-center py-16 text-muted-foreground">
      {label}
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div role="alert" className="rounded-lg border bg-card p-6 text-center">
      <p>{errorMessage(error)}</p>
      {onRetry ? (
        <Button variant="outline" className="mt-3" onClick={onRetry}>
          Thử lại
        </Button>
      ) : null}
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed bg-card p-8 text-center">
      <p className="font-medium">{title}</p>
      {children ? <div className="mt-3 text-sm text-muted-foreground">{children}</div> : null}
    </div>
  );
}

export function NotFoundPage() {
  return (
    <EmptyState title="Không tìm thấy trang">
      <Link to="/" className="font-medium text-tarp hover:underline">
        Về trang chủ
      </Link>
    </EmptyState>
  );
}
