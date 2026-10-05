import { zodResolver } from '@hookform/resolvers/zod';
import { type FormEvent, useRef, useState } from 'react';
import { useForm } from 'react-hook-form';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { toast } from 'sonner';
import { useCart } from '@/api/hooks/cart';
import { useCheckout } from '@/api/hooks/orders';
import { useStartPayment } from '@/api/hooks/payments';
import { ApiError, applyFieldErrors, errorMessage, isRetryable, paymentErrorMessage, shortagesOf } from '@/api/errors';
import type { Address, PaymentMethod } from '@/api/types';
import { payKey } from '@/features/payment/pay-key';
import { formatDateTime } from '@/lib/format';
import { Awning } from '@/components/Awning';
import { Price } from '@/components/Price';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Field, controlProps } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { useAuth } from '@/features/auth/auth-context';
import { groupForCheckout } from '@/features/cart/cart-summary';
import { addressSchema } from './address-schema';

const ADDRESS_FIELDS: Array<{ name: keyof Address; label: string; autoComplete: string; wide?: boolean; inputMode?: 'tel' }> = [
  { name: 'full_name', label: 'Họ và tên người nhận', autoComplete: 'name' },
  { name: 'phone', label: 'Số điện thoại', autoComplete: 'tel', inputMode: 'tel' },
  { name: 'line1', label: 'Số nhà, tên đường', autoComplete: 'address-line1', wide: true },
  { name: 'ward', label: 'Phường/Xã', autoComplete: 'address-level3' },
  { name: 'district', label: 'Quận/Huyện', autoComplete: 'address-level2' },
  { name: 'province', label: 'Tỉnh/Thành phố', autoComplete: 'address-level1' },
];

export function CheckoutPage() {
  const [params] = useSearchParams();
  const skuIds = params.getAll('sku');
  const { user } = useAuth();
  const cart = useCart();
  const navigate = useNavigate();
  const [paymentMethod, setPaymentMethod] = useState<PaymentMethod>('COD');
  // One key per checkout page visit; reused for retries/double-clicks; replaced after success.
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID());
  const submitting = useRef(false);
  const form = useForm<Address>({
    resolver: zodResolver(addressSchema),
    defaultValues: { full_name: user?.full_name ?? '', phone: '', line1: '', ward: '', district: '', province: '' },
  });
  const startPayment = useStartPayment();
  const checkout = useCheckout({
    onSuccess: (res) => {
      setIdempotencyKey(crypto.randomUUID());
      const isOnline = paymentMethod !== 'COD';
      if (isOnline) {
        startPayment.mutate(
          { checkoutId: res.checkout_id, idempotencyKey: payKey(res.checkout_id) },
          {
            onError: (err) => {
              if (isRetryable(err)) {
                toast.error(`Đơn đã được tạo, vui lòng thanh toán trước ${formatDateTime(res.orders[0].payment_deadline!)}`);
              } else {
                toast.error(paymentErrorMessage(err));
              }
              navigate('/orders?status=AWAITING_PAYMENT', { replace: true });
            },
          },
        );
      } else {
        navigate(`/orders?placed=${res.orders.length}`, { replace: true });
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === 'OUT_OF_STOCK') {
        navigate('/cart', { state: { shortages: shortagesOf(err) } });
        return;
      }
      if (err instanceof ApiError && err.code === 'CONFLICT') {
        toast.error('Một số sản phẩm không còn bán. Vui lòng kiểm tra lại giỏ hàng.');
        navigate('/cart');
        return;
      }
      if (applyFieldErrors(err, form.setError, { stripPrefix: 'shipping_address.' })) return;
      if (!isRetryable(err)) form.setError('root', { message: errorMessage(err) });
    },
  });
  const { errors } = form.formState;

  if (cart.isPending) return <PageSpinner />;
  if (cart.isError) return <ErrorState error={cart.error} onRetry={() => void cart.refetch()} />;
  const groups = groupForCheckout(cart.data, skuIds);
  if (groups.length === 0) {
    return (
      <EmptyState title="Không có sản phẩm nào để thanh toán">
        <Link to="/cart" className="font-medium text-tarp hover:underline">Quay lại giỏ hàng</Link>
      </EmptyState>
    );
  }
  const total = groups.reduce((sum, g) => sum + g.subtotal + g.shipping_fee, 0);

  const placeOrder = (address: Address) => {
    checkout.mutate(
      { idempotencyKey, body: { sku_ids: groups.flatMap((g) => g.items.map((i) => i.sku_id)), shipping_address: address, payment_method: paymentMethod } },
      { onSettled: () => { submitting.current = false; } },
    );
  };
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting.current) return; // double-click guard: React state updates land after the second click
    submitting.current = true; // taken before async validation so a second click cannot slip in
    void form.handleSubmit(placeOrder, () => {
      submitting.current = false;
    })(event);
  };

  return (
    <form noValidate onSubmit={onSubmit} className="grid gap-6 lg:grid-cols-[1fr_340px] lg:items-start">
      <div className="space-y-6">
        <h1 className="text-3xl font-bold">Thanh toán</h1>
        <section aria-labelledby="address-title" className="space-y-4 rounded-lg border bg-card p-5">
          <h2 id="address-title" className="text-lg font-semibold">Địa chỉ nhận hàng</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            {ADDRESS_FIELDS.map((f) => (
              <div key={f.name} className={f.wide ? 'sm:col-span-2' : undefined}>
                <Field id={f.name} label={f.label} error={errors[f.name]?.message}>
                  <Input autoComplete={f.autoComplete} inputMode={f.inputMode} {...controlProps(f.name, errors[f.name]?.message)} {...form.register(f.name)} />
                </Field>
              </div>
            ))}
          </div>
        </section>
        {groups.map((g) => (
          <section key={g.shop.id} aria-label={`Đơn hàng từ ${g.shop.name}`} className="overflow-hidden rounded-lg border bg-card">
            <Awning />
            <div className="space-y-3 p-4">
              <h2 className="font-semibold">{g.shop.name}</h2>
              <ul className="divide-y text-sm">
                {g.items.map((i) => (
                  <li key={i.sku_id} className="flex items-start justify-between gap-3 py-2">
                    <span>
                      {i.product_name}
                      <span className="block text-muted-foreground">{i.variant_name}, số lượng {i.quantity}</span>
                    </span>
                    <Price value={i.unit_price * i.quantity} />
                  </li>
                ))}
              </ul>
              <dl className="space-y-1 border-t pt-3 text-sm">
                <div className="flex justify-between"><dt>Tạm tính</dt><dd><Price value={g.subtotal} /></dd></div>
                <div className="flex justify-between"><dt>Phí vận chuyển</dt><dd><Price value={g.shipping_fee} /></dd></div>
              </dl>
            </div>
          </section>
        ))}
      </div>
      <aside aria-label="Thanh toán" className="space-y-4 rounded-lg border bg-card p-5 lg:sticky lg:top-4">
        <fieldset>
          <legend className="font-semibold">Phương thức thanh toán</legend>
          <label className="mt-2 flex items-center gap-2 text-sm">
            <input type="radio" name="payment_method" value="COD" checked={paymentMethod === 'COD'} onChange={() => setPaymentMethod('COD')} className="accent-[var(--tarp)]" data-testid="payment-method-COD" />
            Thanh toán khi nhận hàng (COD)
          </label>
          <label className="mt-2 flex items-center gap-2 text-sm">
            <input type="radio" name="payment_method" value="VNPAY" checked={paymentMethod === 'VNPAY'} onChange={() => setPaymentMethod('VNPAY')} className="accent-[var(--tarp)]" data-testid="payment-method-VNPAY" />
            VNPay
          </label>
          <label className="mt-2 flex items-center gap-2 text-sm">
            <input type="radio" name="payment_method" value="STRIPE" checked={paymentMethod === 'STRIPE'} onChange={() => setPaymentMethod('STRIPE')} className="accent-[var(--tarp)]" data-testid="payment-method-STRIPE" />
            Thẻ quốc tế (Stripe)
          </label>
        </fieldset>
        <p className="text-sm text-muted-foreground">Mỗi shop tạo một đơn hàng riêng ({groups.length} đơn).</p>
        <div className="flex items-baseline justify-between gap-3">
          <span>Tổng thanh toán</span>
          <Price value={total} className="text-3xl" />
        </div>
        {errors.root ? (
          <Alert variant="destructive">
            <AlertDescription>{errors.root.message}</AlertDescription>
          </Alert>
        ) : null}
        <Button type="submit" size="lg" className="w-full" disabled={checkout.isPending || startPayment.isPending}>
          {checkout.isPending
            ? 'Đang đặt hàng…'
            : startPayment.isPending
              ? 'Đang chuyển đến trang thanh toán…'
              : paymentMethod === 'COD'
                ? 'Đặt hàng'
                : 'Đặt hàng và thanh toán'}
        </Button>
      </aside>
    </form>
  );
}
