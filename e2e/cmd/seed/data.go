package main

import "github.com/ngoxuanchien/e-commerce/e2e/client"

// password is shared by every synthetic demo account.
const password = "matkhau123"

type account struct {
	Email    string
	FullName string
}

type shopData struct {
	Name, Slug, Description string
}

type productData struct {
	Name        string
	Description string
	Category    string // child category slug seeded by catalog migration 000003
	Image       string // file in e2e/fixtures/images
	Skus        []client.SkuInput
}

type sellerData struct {
	Account  account
	Shop     shopData
	Products []productData
}

// orderPlan places one single-line order and walks it to Want. Steps are
// seller actions (confirm, ship, deliver) or buyer actions (complete, cancel).
type orderPlan struct {
	Buyer, Shop, Product int
	Steps                []string
	Want                 string
}

// onlinePlan places one single-line online order (with payment) and walks it to Want.
type onlinePlan struct {
	Buyer, Shop, Product int
	Method               string
	Pay                  bool
	Steps                []string
	Want, WantPayment    string
}

func sku(name string, price int64, stock int) client.SkuInput {
	return client.SkuInput{Name: name, Price: price, Stock: stock, Active: true}
}

var buyers = []account{
	{Email: "buyer1@example.test", FullName: "An Nguyễn"},
	{Email: "buyer2@example.test", FullName: "Bình Hoàng"},
}

var sellers = []sellerData{
	{
		Account: account{Email: "seller1@example.test", FullName: "Lan Trần"},
		Shop:    shopData{Name: "Thời Trang Phố Cổ", Slug: "thoi-trang-pho-co", Description: "Quần áo cotton may tại Hà Nội, giao toàn quốc."},
		Products: []productData{
			{"Áo thun cotton trơn cổ tròn", "Cotton 100%, thấm hút mồ hôi, form regular.", "ao-thun-nam", "fashion-01.jpg",
				[]client.SkuInput{sku("S", 149_000, 50), sku("M", 149_000, 50), sku("L", 149_000, 40), sku("XL", 159_000, 30)}},
			{"Áo sơ mi linen tay dài", "Vải linen thoáng mát, phù hợp đi làm.", "ao-so-mi-nam", "fashion-02.jpg",
				[]client.SkuInput{sku("M", 329_000, 30), sku("L", 329_000, 25)}},
			{"Quần jeans nam ống đứng", "Denim co giãn nhẹ, wash xanh đậm.", "quan-jeans-nam", "fashion-03.jpg",
				[]client.SkuInput{sku("29", 459_000, 20), sku("30", 459_000, 20), sku("31", 459_000, 15), sku("32", 459_000, 10)}},
			{"Váy hoa nhí dáng xòe", "Voan hai lớp, dây kéo sau lưng.", "vay-dam", "fashion-04.jpg",
				[]client.SkuInput{sku("S", 389_000, 25), sku("M", 389_000, 25)}},
			{"Áo len nữ cổ lọ", "Len dệt kim mềm, giữ ấm mùa đông.", "ao-nu", "fashion-05.jpg",
				[]client.SkuInput{sku("Kem", 279_000, 15), sku("Xám", 279_000, 15)}},
			{"Quần culottes nữ lưng cao", "Ống rộng, vải tuyết mưa không nhăn.", "quan-nu", "fashion-06.jpg",
				[]client.SkuInput{sku("S", 259_000, 40), sku("M", 259_000, 40), sku("L", 259_000, 30)}},
			{"Áo polo nam phối viền", "Cá sấu cotton, cổ bẻ phối viền.", "ao-thun-nam", "fashion-07.jpg",
				[]client.SkuInput{sku("M", 219_000, 35), sku("L", 219_000, 35)}},
		},
	},
	{
		Account: account{Email: "seller2@example.test", FullName: "Hùng Lê"},
		Shop:    shopData{Name: "Điện Máy Sài Gòn", Slug: "dien-may-sai-gon", Description: "Phụ kiện và thiết bị điện tử chính hãng, bảo hành 12 tháng."},
		Products: []productData{
			{"Tai nghe bluetooth chống ồn", "Chống ồn chủ động, pin 30 giờ.", "tai-nghe", "electronics-01.jpg",
				[]client.SkuInput{sku("Đen", 1_290_000, 30), sku("Trắng", 1_290_000, 20)}},
			{"Ốp lưng silicon trong suốt", "Chống ố vàng, viền bảo vệ camera.", "op-lung", "electronics-02.jpg",
				[]client.SkuInput{sku("iPhone 16", 89_000, 100), sku("Galaxy S26", 89_000, 80)}},
			{"Củ sạc nhanh GaN 65W", "Hai cổng USB-C, một cổng USB-A.", "sac-cap", "electronics-03.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 590_000, 60)}},
			{"Chuột không dây công thái học", "Kết nối 2.4 GHz và bluetooth.", "chuot-ban-phim", "electronics-04.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 450_000, 35)}},
			{"Bàn phím cơ 87 phím", "Hot-swap, đèn nền RGB.", "chuot-ban-phim", "electronics-05.jpg",
				[]client.SkuInput{sku("Switch đỏ", 1_150_000, 20), sku("Switch nâu", 1_150_000, 20)}},
			{"Loa bluetooth mini chống nước", "Chuẩn IPX7, pin 12 giờ.", "loa", "electronics-06.jpg",
				[]client.SkuInput{sku("Xanh", 690_000, 25), sku("Đỏ", 690_000, 25), sku("Đen", 690_000, 25)}},
			{"Đồng hồ thông minh theo dõi sức khỏe", "Đo nhịp tim, SpO2, GPS tích hợp.", "dong-ho-thong-minh", "electronics-07.jpg",
				[]client.SkuInput{sku("41mm", 2_490_000, 10), sku("45mm", 2_690_000, 10)}},
			{"Màn hình 24 inch IPS 75Hz", "Tấm nền IPS, viền mỏng ba cạnh.", "man-hinh", "electronics-08.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 2_890_000, 8)}},
		},
	},
	{
		Account: account{Email: "seller3@example.test", FullName: "Mai Phạm"},
		Shop:    shopData{Name: "Nhà Xinh Đà Lạt", Slug: "nha-xinh-da-lat", Description: "Đồ gia dụng và trang trí thủ công từ Đà Lạt."},
		Products: []productData{
			{"Bát ăn cơm men lam Bát Tràng", "Gốm Bát Tràng vẽ tay men lam.", "do-dung-nha-bep", "home-01.jpg",
				[]client.SkuInput{sku("Bộ 6 chiếc", 180_000, 40), sku("Bộ 10 chiếc", 280_000, 20)}},
			{"Chảo chống dính đáy từ", "Dùng được bếp từ, phủ ceramic.", "do-dung-nha-bep", "home-02.jpg",
				[]client.SkuInput{sku("26cm", 350_000, 30), sku("28cm", 390_000, 20)}},
			{"Bình hoa gốm men rạn cổ", "Men rạn thủ công, cao 30 cm.", "trang-tri-nha-cua", "home-03.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 420_000, 12)}},
			{"Nến thơm tinh dầu sả chanh", "Sáp đậu nành, tinh dầu thiên nhiên.", "trang-tri-nha-cua", "home-04.jpg",
				[]client.SkuInput{sku("120g", 95_000, 50), sku("200g", 145_000, 40)}},
			{"Bộ chăn ga cotton lụa", "Cotton lụa mát, bốn món.", "chan-ga-goi", "home-05.jpg",
				[]client.SkuInput{sku("1m6", 890_000, 15), sku("1m8", 990_000, 15)}},
			{"Gối tựa lưng sofa vải bố", "Vỏ tháo rời, ruột bông gòn.", "trang-tri-nha-cua", "home-06.jpg",
				[]client.SkuInput{sku("Be", 159_000, 30), sku("Xanh rêu", 159_000, 30)}},
			{"Thớt gỗ nghiến tự nhiên", "Gỗ nghiến nguyên khối, không nứt.", "do-dung-nha-bep", "home-07.jpg",
				[]client.SkuInput{sku("Nhỏ", 120_000, 25), sku("Vừa", 180_000, 25), sku("Lớn", 250_000, 15)}},
		},
	},
}

var orderPlans = []orderPlan{
	{Buyer: 0, Shop: 0, Product: 0, Want: "PENDING_CONFIRMATION"},
	{Buyer: 0, Shop: 1, Product: 0, Steps: []string{"confirm"}, Want: "CONFIRMED"},
	{Buyer: 1, Shop: 2, Product: 0, Steps: []string{"confirm", "ship"}, Want: "SHIPPING"},
	{Buyer: 1, Shop: 0, Product: 1, Steps: []string{"confirm", "ship", "deliver", "complete"}, Want: "COMPLETED"},
	{Buyer: 0, Shop: 2, Product: 1, Steps: []string{"cancel"}, Want: "CANCELLED"},
}

var onlinePlans = []onlinePlan{
	{Buyer: 1, Shop: 0, Product: 2, Method: "VNPAY", Pay: false, Steps: []string{}, Want: "AWAITING_PAYMENT", WantPayment: "UNPAID"},
	{Buyer: 1, Shop: 0, Product: 3, Method: "VNPAY", Pay: true, Steps: []string{}, Want: "PENDING_CONFIRMATION", WantPayment: "PAID"},
	{Buyer: 1, Shop: 1, Product: 1, Method: "STRIPE", Pay: true, Steps: []string{"confirm", "ship"}, Want: "SHIPPING", WantPayment: "PAID"},
	{Buyer: 1, Shop: 0, Product: 4, Method: "VNPAY", Pay: true, Steps: []string{"confirm", "ship", "deliver", "complete"}, Want: "COMPLETED", WantPayment: "PAID"},
	{Buyer: 0, Shop: 0, Product: 5, Method: "STRIPE", Pay: true, Steps: []string{"confirm", "ship", "deliver", "complete"}, Want: "COMPLETED", WantPayment: "PAID"},
	{Buyer: 0, Shop: 0, Product: 6, Method: "VNPAY", Pay: true, Steps: []string{"confirm", "cancel"}, Want: "CANCELLED", WantPayment: "REFUNDED"},
}
