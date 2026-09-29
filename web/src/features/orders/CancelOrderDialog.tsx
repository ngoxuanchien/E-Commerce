import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { errorMessage, isRetryable } from '@/api/errors';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Field, controlProps } from '@/components/ui/field';
import { Textarea } from '@/components/ui/textarea';

const schema = z.object({ reason: z.string().trim().min(1, 'Vui lòng nhập lý do huỷ').max(200, 'Lý do tối đa 200 ký tự') });
type Values = z.infer<typeof schema>;

export function CancelOrderDialog({ triggerLabel, onConfirm, pending, size }: {
  triggerLabel: string;
  onConfirm: (reason: string) => Promise<unknown>;
  pending: boolean;
  size?: 'sm' | 'default';
}) {
  const [open, setOpen] = useState(false);
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { reason: '' } });
  const { errors } = form.formState;
  const submit = form.handleSubmit(async ({ reason }) => {
    try {
      await onConfirm(reason);
      setOpen(false);
      form.reset();
    } catch (err) {
      if (!isRetryable(err)) form.setError('root', { message: errorMessage(err) });
    }
  });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size={size} className="text-chili">{triggerLabel}</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Huỷ đơn hàng</DialogTitle>
          <DialogDescription>Hàng trong đơn sẽ được hoàn lại kho. Không thể hoàn tác sau khi huỷ.</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="space-y-4">
          {errors.root ? (
            <Alert variant="destructive">
              <AlertDescription>{errors.root.message}</AlertDescription>
            </Alert>
          ) : null}
          <Field id="cancel-reason" label="Lý do huỷ" error={errors.reason?.message}>
            <Textarea rows={3} {...controlProps('cancel-reason', errors.reason?.message)} {...form.register('reason')} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setOpen(false)}>Giữ đơn</Button>
            <Button type="submit" variant="destructive" disabled={pending}>Xác nhận huỷ</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
