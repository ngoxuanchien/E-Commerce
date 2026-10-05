import { z } from 'zod';

export const PAYOUT_MIN_AMOUNT = 50_000; // mirrors ledger.yaml createSellerPayout 400

export function payoutSchema(available: number) {
  return z.object({
    amount: z
      .string()
      .trim()
      .refine((val) => val !== '', 'Vui lòng nhập số tiền')
      .pipe(z.string().regex(/^\d+$/, 'Số tiền phải là số nguyên'))
      .transform(Number)
      .pipe(z.number().min(PAYOUT_MIN_AMOUNT, 'Số tiền rút tối thiểu là 50.000 ₫').max(available, 'Số tiền vượt quá số dư khả dụng')),
  });
}

export type PayoutFormInput = z.input<ReturnType<typeof payoutSchema>>;
export type PayoutFormValues = z.output<ReturnType<typeof payoutSchema>>;
