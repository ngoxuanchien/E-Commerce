import type { Sku } from '@/api/types';
import { cn } from '@/lib/utils';

/** Native radio group: arrow keys move between variants; sold-out variants are disabled. */
export function VariantSelector({ skus, value, onChange }: { skus: Sku[]; value: string | undefined; onChange: (skuId: string) => void }) {
  const offered = skus.filter((s) => s.active);
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">Phân loại</legend>
      <div className="flex flex-wrap gap-2">
        {offered.map((sku) => {
          const soldOut = sku.stock === 0;
          return (
            <label
              key={sku.id}
              className={cn(
                'relative inline-flex min-h-10 cursor-pointer items-center rounded-md border bg-card px-3 text-sm',
                'has-[:checked]:border-tarp has-[:checked]:bg-tarp/10 has-[:checked]:text-tarp',
                'has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring',
                soldOut && 'cursor-not-allowed text-muted-foreground line-through',
              )}
            >
              <input
                type="radio"
                name="sku"
                value={sku.id}
                aria-label={soldOut ? `${sku.name} (hết hàng)` : sku.name}
                checked={value === sku.id}
                disabled={soldOut}
                onChange={() => onChange(sku.id)}
                className="absolute inset-0 cursor-pointer opacity-0 disabled:cursor-not-allowed"
              />
              {sku.name}
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}
