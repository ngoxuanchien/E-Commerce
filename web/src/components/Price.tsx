import { formatVnd } from '@/lib/format';
import { cn } from '@/lib/utils';

export function Price({ value, className }: { value: number | null | undefined; className?: string }) {
  return <span className={cn('price', className)}>{formatVnd(value)}</span>;
}
