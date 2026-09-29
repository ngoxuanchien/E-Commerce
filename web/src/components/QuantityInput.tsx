import { Minus, Plus } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';

export function QuantityInput({ id, value, max, onChange, label = 'Số lượng', disabled }: {
  id: string;
  value: number;
  max: number;
  onChange: (value: number) => void;
  label?: string;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState(String(value));
  const [synced, setSynced] = useState(value);
  if (value !== synced) {
    // The server value changed (e.g. after a mutation): show it.
    setSynced(value);
    setDraft(String(value));
  }
  const commit = (next: number) => {
    const clamped = Math.min(Math.max(1, Math.trunc(next) || 1), max);
    setDraft(String(clamped));
    if (clamped !== value) onChange(clamped);
  };
  return (
    <div className="inline-flex items-center rounded-md border bg-card">
      <Button type="button" variant="ghost" size="icon" aria-label="Giảm số lượng" disabled={disabled || value <= 1} onClick={() => commit(value - 1)}>
        <Minus aria-hidden="true" />
      </Button>
      <input
        id={id}
        aria-label={label}
        inputMode="numeric"
        value={draft}
        disabled={disabled}
        onChange={(e) => setDraft(e.target.value.replace(/\D/g, ''))}
        onBlur={() => commit(Number(draft))}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            commit(Number(draft));
          }
        }}
        className="h-9 w-12 bg-transparent text-center text-sm tabular-nums focus-visible:outline-none"
      />
      <Button type="button" variant="ghost" size="icon" aria-label="Tăng số lượng" disabled={disabled || value >= max} onClick={() => commit(value + 1)}>
        <Plus aria-hidden="true" />
      </Button>
    </div>
  );
}
