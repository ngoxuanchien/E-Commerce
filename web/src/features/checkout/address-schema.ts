import { z } from 'zod';
import type { Address } from '@/api/types';

export const addressSchema = z.object({
  full_name: z.string().trim().min(1, 'Vui lòng nhập họ tên người nhận'),
  phone: z.string().trim().regex(/^(0|\+84)\d{9,10}$/, 'Số điện thoại không hợp lệ'),
  line1: z.string().trim().min(1, 'Vui lòng nhập số nhà, tên đường'),
  ward: z.string().trim().min(1, 'Vui lòng nhập phường/xã'),
  district: z.string().trim().min(1, 'Vui lòng nhập quận/huyện'),
  province: z.string().trim().min(1, 'Vui lòng nhập tỉnh/thành phố'),
}) satisfies z.ZodType<Address>;
