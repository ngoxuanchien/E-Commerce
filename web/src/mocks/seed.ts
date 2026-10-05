import type { Category, Shop, Sku } from '@/api/types';
import type { DbState, MockOrder, MockProduct, MockUser, MockWalletEntry } from './db';

const id = (n: number) => `0199a000-0000-7000-8000-${String(n).padStart(12, '0')}`;

export const SEED = {
  password: 'matkhau123',
  users: { an: id(1), binh: id(2), chi: id(3) },
  shops: { gom: id(11), aoThun: id(12) },
  products: { bowl: id(31), vase: id(32), teaSet: id(33), plate: id(34), tee: id(35), busTee: id(36), jacket: id(37), polo: id(38) },
  skus: {
    bowl11: id(51), bowl13: id(52), vase: id(53), teaSet: id(54), plate20: id(55), plate26: id(56),
    teeS: id(57), teeM: id(58), teeL: id(59), teeXL: id(60), busM: id(61), busL: id(62),
    jacketNavy: id(63), jacketBlack: id(64), poloM: id(65),
  },
  orders: { delivered: id(81), pending: id(82), paidCompleted: id(83) },
} as const;

const at = (hoursAfterBase: number) => new Date(Date.UTC(2026, 8, 20, 1) + hoursAfterBase * 3_600_000).toISOString();
const sku = (skuId: string, name: string, price: number, stock: number, active = true): Sku => ({ id: skuId, name, price, stock, active });

function categories(): Category[] {
  const node = (n: number, name: string, slug: string, children: Category[] = []): Category => ({ id: id(n), name, slug, children });
  return [
    node(21, 'Thời trang', 'thoi-trang', [node(211, 'Áo thun', 'ao-thun'), node(212, 'Áo khoác', 'ao-khoac')]),
    node(22, 'Nhà cửa & đời sống', 'nha-cua-doi-song', [node(221, 'Gốm sứ', 'gom-su'), node(222, 'Đồ bếp', 'do-bep')]),
    node(23, 'Điện tử', 'dien-tu', [node(231, 'Tai nghe', 'tai-nghe'), node(232, 'Phụ kiện điện thoại', 'phu-kien-dien-thoai')]),
    node(24, 'Sách', 'sach', [node(241, 'Văn học', 'van-hoc'), node(242, 'Kỹ năng sống', 'ky-nang-song')]),
  ];
}

export function buildSeed(): DbState {
  const { users: U, shops: S, products: P, skus: K } = SEED;
  const users: MockUser[] = [
    { id: U.an, email: 'an@example.com', password: SEED.password, full_name: 'An Nguyễn', roles: ['buyer'], created_at: at(0) },
    { id: U.binh, email: 'binh@example.com', password: SEED.password, full_name: 'Bình Trần', roles: ['buyer', 'seller'], created_at: at(0) },
    { id: U.chi, email: 'chi@example.com', password: SEED.password, full_name: 'Chi Lê', roles: ['buyer', 'seller'], created_at: at(0) },
  ];
  const shops: Shop[] = [
    { id: S.gom, name: 'Gốm Bát Tràng Cô Bình', slug: 'gom-bat-trang', description: 'Gốm men lam, men rạn làm thủ công tại làng Bát Tràng.', owner_user_id: U.binh, created_at: at(1) },
    { id: S.aoThun, name: 'Áo Thun Sài Gòn', slug: 'ao-thun-sai-gon', description: 'Áo thun cotton in hình Sài Gòn, may tại Quận 5.', owner_user_id: U.chi, created_at: at(2) },
  ];
  const product = (p: Omit<MockProduct, 'updated_at'>): MockProduct => ({ ...p, updated_at: p.created_at });
  const products: MockProduct[] = [
    product({ id: P.bowl, shop_id: S.gom, category_id: id(221), name: 'Bát ăn cơm men lam Bát Tràng', description: 'Bát gốm men lam vẽ tay, dùng được trong lò vi sóng.', status: 'active', image_keys: ['seed/bat-com.svg'], skus: [sku(K.bowl11, 'Đường kính 11 cm', 45_000, 50), sku(K.bowl13, 'Đường kính 13 cm', 55_000, 30)], created_at: at(10) }),
    product({ id: P.vase, shop_id: S.gom, category_id: id(221), name: 'Bình hoa gốm men rạn cổ', description: 'Bình men rạn, mỗi chiếc có vân rạn riêng.', status: 'active', image_keys: ['seed/binh-hoa.svg'], skus: [sku(K.vase, 'Cao 25 cm', 320_000, 1)], created_at: at(11) }),
    product({ id: P.teaSet, shop_id: S.gom, category_id: id(221), name: 'Bộ ấm chén vẽ sen', description: 'Một ấm và sáu chén, hoạ tiết sen vẽ tay.', status: 'active', image_keys: ['seed/am-chen.svg'], skus: [sku(K.teaSet, 'Bộ 1 ấm 6 chén', 450_000, 8)], created_at: at(12) }),
    product({ id: P.plate, shop_id: S.gom, category_id: id(222), name: 'Đĩa gốm vẽ tay hoạ tiết cá', description: 'Đĩa gốm tráng men, hoạ tiết cá chép.', status: 'active', image_keys: ['seed/dia-ca.svg'], skus: [sku(K.plate20, 'Đĩa 20 cm', 85_000, 25), sku(K.plate26, 'Đĩa 26 cm', 120_000, 0)], created_at: at(13) }),
    product({ id: P.tee, shop_id: S.aoThun, category_id: id(211), name: 'Áo thun cotton trơn cổ tròn', description: 'Cotton 100%, dày dặn, không xù lông.', status: 'active', image_keys: ['seed/ao-thun-tron.svg'], skus: [sku(K.teeS, 'S', 149_000, 20), sku(K.teeM, 'M', 149_000, 15), sku(K.teeL, 'L', 149_000, 10), sku(K.teeXL, 'XL', 149_000, 5, false)], created_at: at(14) }),
    product({ id: P.busTee, shop_id: S.aoThun, category_id: id(211), name: 'Áo thun in hình xe buýt Sài Gòn', description: 'Hình in lụa xe buýt Sài Gòn xưa.', status: 'active', image_keys: ['seed/ao-thun-xe-buyt.svg'], skus: [sku(K.busM, 'M', 199_000, 12), sku(K.busL, 'L', 199_000, 0)], created_at: at(15) }),
    product({ id: P.jacket, shop_id: S.aoThun, category_id: id(212), name: 'Áo khoác gió unisex chống nước', description: 'Vải dù chống nước nhẹ, có mũ.', status: 'active', image_keys: ['seed/ao-khoac.svg'], skus: [sku(K.jacketNavy, 'Xanh navy', 350_000, 7), sku(K.jacketBlack, 'Đen', 350_000, 9)], created_at: at(16) }),
    product({ id: P.polo, shop_id: S.aoThun, category_id: id(211), name: 'Áo polo vải cá sấu', description: 'Sắp mở bán.', status: 'draft', image_keys: [], skus: [sku(K.poloM, 'M', 229_000, 10)], created_at: at(17) }),
  ];
  const address = { full_name: 'An Nguyễn', phone: '0901234567', line1: '12 Lý Tự Trọng', ward: 'Bến Nghé', district: 'Quận 1', province: 'TP. Hồ Chí Minh' };
  const orders: MockOrder[] = [
    {
      id: SEED.orders.pending, checkout_id: id(91), status: 'PENDING_CONFIRMATION', buyer_id: U.an, seller_user_id: U.chi,
      shop: { id: S.aoThun, name: 'Áo Thun Sài Gòn', slug: 'ao-thun-sai-gon' },
      items: [{ sku_id: K.teeM, product_id: P.tee, product_name: 'Áo thun cotton trơn cổ tròn', variant_name: 'M', image_url: '/mock-images/ao-thun-tron.svg', unit_price: 149_000, quantity: 1, line_total: 149_000 }],
      subtotal: 149_000, shipping_fee: 30_000, total: 179_000, shipping_address: address, payment_method: 'COD', cancel_reason: null,
      payment_status: 'NONE', payment_deadline: null, commission_rate_bps: 0, commission_amount: 0, delivered_at: null,
      history: [
        { from: null, to: 'PENDING', actor_role: 'system', note: null, created_at: at(30) },
        { from: 'PENDING', to: 'PENDING_CONFIRMATION', actor_role: 'system', note: null, created_at: at(30) },
      ],
      created_at: at(30), updated_at: at(30),
    },
    {
      id: SEED.orders.delivered, checkout_id: id(92), status: 'DELIVERED', buyer_id: U.an, seller_user_id: U.binh,
      shop: { id: S.gom, name: 'Gốm Bát Tràng Cô Bình', slug: 'gom-bat-trang' },
      items: [{ sku_id: K.bowl11, product_id: P.bowl, product_name: 'Bát ăn cơm men lam Bát Tràng', variant_name: 'Đường kính 11 cm', image_url: '/mock-images/bat-com.svg', unit_price: 45_000, quantity: 2, line_total: 90_000 }],
      subtotal: 90_000, shipping_fee: 30_000, total: 120_000, shipping_address: address, payment_method: 'COD', cancel_reason: null,
      payment_status: 'NONE', payment_deadline: null, commission_rate_bps: 0, commission_amount: 0, delivered_at: at(26),
      history: [
        { from: null, to: 'PENDING', actor_role: 'system', note: null, created_at: at(20) },
        { from: 'PENDING', to: 'PENDING_CONFIRMATION', actor_role: 'system', note: null, created_at: at(20) },
        { from: 'PENDING_CONFIRMATION', to: 'CONFIRMED', actor_role: 'seller', note: null, created_at: at(21) },
        { from: 'CONFIRMED', to: 'SHIPPING', actor_role: 'seller', note: null, created_at: at(22) },
        { from: 'SHIPPING', to: 'DELIVERED', actor_role: 'seller', note: null, created_at: at(26) },
      ],
      created_at: at(20), updated_at: at(26),
    },
    {
      id: SEED.orders.paidCompleted, checkout_id: id(93), status: 'COMPLETED', buyer_id: U.chi, seller_user_id: U.binh,
      shop: { id: S.gom, name: 'Gốm Bát Tràng Cô Bình', slug: 'gom-bat-trang' },
      items: [{ sku_id: K.teaSet, product_id: P.teaSet, product_name: 'Bộ ấm chén vẽ sen', variant_name: 'Bộ 1 ấm 6 chén', image_url: '/mock-images/am-chen.svg', unit_price: 450_000, quantity: 1, line_total: 450_000 }],
      subtotal: 450_000, shipping_fee: 30_000, total: 480_000, shipping_address: address, payment_method: 'VNPAY', cancel_reason: null,
      payment_status: 'PAID', payment_deadline: at(40), commission_rate_bps: 500, commission_amount: 22_500, delivered_at: at(44),
      history: [
        { from: null, to: 'PENDING', actor_role: 'system', note: null, created_at: at(39) },
        { from: 'PENDING', to: 'AWAITING_PAYMENT', actor_role: 'system', note: null, created_at: at(39) },
        { from: 'AWAITING_PAYMENT', to: 'PENDING_CONFIRMATION', actor_role: 'system', note: null, created_at: at(40) },
        { from: 'PENDING_CONFIRMATION', to: 'CONFIRMED', actor_role: 'seller', note: null, created_at: at(41) },
        { from: 'CONFIRMED', to: 'SHIPPING', actor_role: 'seller', note: null, created_at: at(42) },
        { from: 'SHIPPING', to: 'DELIVERED', actor_role: 'seller', note: null, created_at: at(44) },
        { from: 'DELIVERED', to: 'COMPLETED', actor_role: 'buyer', note: null, created_at: at(45) },
      ],
      created_at: at(39), updated_at: at(45),
    },
  ];
  const walletEntries: MockWalletEntry[] = [
    {
      id: id(101), transaction_id: id(201), kind: 'RELEASE', amount: 457_500, order_id: SEED.orders.paidCompleted, payout_id: null,
      seller_user_id: U.binh, created_at: at(45),
    },
  ];
  return {
    users, shops, categories: categories(), products, carts: {}, orders, idempotency: {},
    payments: [], walletEntries, payouts: [], keys: {},
    blobs: {}, tokens: {}, session: null,
  };
}
