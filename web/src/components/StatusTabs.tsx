import { Button } from '@/components/ui/button';

export function StatusTabs<T extends string>({ label, options, value, onChange }: {
  label: string;
  options: ReadonlyArray<{ value: T | undefined; label: string }>;
  value: T | undefined;
  onChange: (value: T | undefined) => void;
}) {
  return (
    <div role="group" aria-label={label} className="-mx-4 flex gap-1 overflow-x-auto px-4 pb-1">
      {options.map((option) => (
        <Button
          key={option.value ?? 'all'}
          size="sm"
          variant={option.value === value ? 'default' : 'ghost'}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
          className="shrink-0"
        >
          {option.label}
        </Button>
      ))}
    </div>
  );
}
