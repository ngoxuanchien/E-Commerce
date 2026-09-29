import type { ReactNode } from 'react';
import { Label } from './label';

export function Field({ id, label, error, hint, children }: {
  id: string;
  label: ReactNode;
  error?: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      {error ? (
        <p id={`${id}-error`} className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}

/** Accessibility wiring for a control rendered inside <Field id={id}>. */
export function controlProps(id: string, error?: string) {
  return { id, 'aria-invalid': error ? true : undefined, 'aria-describedby': error ? `${id}-error` : undefined } as const;
}
