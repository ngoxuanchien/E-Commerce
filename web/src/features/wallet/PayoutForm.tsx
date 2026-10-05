import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type FieldValues } from 'react-hook-form';
import { toast } from 'sonner';
import { useCreatePayout } from '@/api/hooks/wallet';
import { ApiError, applyFieldErrors, errorMessage, isRetryable, paymentErrorMessage } from '@/api/errors';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { payoutSchema, type PayoutFormInput } from './payout-schema';

export function PayoutForm({ available }: { available: number }) {
  const [key, setKey] = useState(() => crypto.randomUUID());
  const form = useForm<PayoutFormInput>({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    resolver: zodResolver(payoutSchema(available)) as any,
    defaultValues: { amount: '' },
  });
  const createPayout = useCreatePayout();
  const { errors } = form.formState;

  const onSubmit = (values: FieldValues) => {
    const amount = typeof values.amount === 'number' ? values.amount : parseInt(values.amount as string, 10);
    createPayout.mutate(
      { idempotencyKey: key, amount },
      {
        onSuccess: () => {
          toast.success('Đã gửi yêu cầu rút tiền');
          form.reset();
          setKey(crypto.randomUUID());
        },
        onError: (err) => {
          if (err instanceof ApiError && err.code === 'INSUFFICIENT_FUNDS') {
            form.setError('root', { message: paymentErrorMessage(err) });
            return;
          }
          if (applyFieldErrors(err, form.setError)) return;
          if (!isRetryable(err)) form.setError('root', { message: errorMessage(err) });
        },
      },
    );
  };

  return (
    <section className="space-y-4 rounded-lg border bg-card p-5">
      <h2 className="text-lg font-semibold">Rút tiền</h2>
      <form noValidate onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
        <div>
          <Field id="payout-amount" label="Số tiền muốn rút (₫)" error={errors.amount?.message}>
            <Input
              inputMode="numeric"
              data-testid="payout-amount"
              {...controlProps('payout-amount', errors.amount?.message)}
              {...form.register('amount')}
            />
          </Field>
        </div>
        {errors.root ? (
          <Alert variant="destructive">
            <AlertDescription>{errors.root.message}</AlertDescription>
          </Alert>
        ) : null}
        <Button type="submit" data-testid="payout-submit" disabled={createPayout.isPending}>
          {createPayout.isPending ? 'Đang gửi…' : 'Gửi yêu cầu rút tiền'}
        </Button>
      </form>
    </section>
  );
}
