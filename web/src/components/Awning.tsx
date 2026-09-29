import { cn } from '@/lib/utils';

/** Market-tarp stripe marking a block that belongs to one shop. */
export function Awning({ className }: { className?: string }) {
  return <div aria-hidden="true" className={cn('awning', className)} />;
}
