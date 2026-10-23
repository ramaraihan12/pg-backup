package demo

// Word lists for the made-up shop. Nothing here refers to a real person,
// street or brand.

var firstNames = []string{
	"Budi", "Siti", "Agus", "Dewi", "Eko", "Rina", "Joko", "Sri", "Andi", "Wulan",
	"Dedi", "Ratna", "Hendra", "Lestari", "Bayu", "Ayu", "Rudi", "Maya", "Fajar", "Indah",
	"Teguh", "Putri", "Yoga", "Nia", "Arif", "Fitri", "Dimas", "Rani", "Galih", "Tari",
	"Wahyu", "Citra", "Ilham", "Sari", "Rizki", "Anisa", "Bambang", "Kartika", "Hadi", "Mega",
}

var lastNames = []string{
	"Santoso", "Wijaya", "Pratama", "Saputra", "Lestari", "Hidayat", "Kusuma", "Susanto", "Rahayu", "Nugroho",
	"Setiawan", "Wibowo", "Purnomo", "Utami", "Hartono", "Firmansyah", "Permana", "Handayani", "Yulianto", "Maulana",
	"Suryadi", "Budiman", "Gunawan", "Sulistyo", "Ramadhan", "Anggraini", "Prasetyo", "Kurniawan", "Cahyono", "Wardani",
}

type place struct{ name, province string }

var cities = []place{
	{"Jakarta Selatan", "DKI Jakarta"}, {"Bandung", "Jawa Barat"}, {"Bekasi", "Jawa Barat"}, {"Depok", "Jawa Barat"},
	{"Semarang", "Jawa Tengah"}, {"Salatiga", "Jawa Tengah"}, {"Surakarta", "Jawa Tengah"}, {"Magelang", "Jawa Tengah"},
	{"Yogyakarta", "DI Yogyakarta"}, {"Surabaya", "Jawa Timur"}, {"Malang", "Jawa Timur"}, {"Kediri", "Jawa Timur"},
	{"Medan", "Sumatera Utara"}, {"Palembang", "Sumatera Selatan"}, {"Pekanbaru", "Riau"}, {"Makassar", "Sulawesi Selatan"},
	{"Balikpapan", "Kalimantan Timur"}, {"Denpasar", "Bali"}, {"Manado", "Sulawesi Utara"}, {"Pontianak", "Kalimantan Barat"},
}

var streets = []string{
	"Merdeka", "Melati", "Mawar", "Kenanga", "Diponegoro", "Pahlawan", "Sudirman", "Cendana", "Anggrek", "Veteran",
	"Pemuda", "Kemuning", "Flamboyan", "Kasuari", "Cempaka", "Teratai", "Bhayangkara", "Gajah Mada", "Imam Bonjol", "Ahmad Yani",
}

var villages = []string{
	"Sidorejo", "Kalibening", "Mangunsari", "Tegalrejo", "Cebongan", "Kutowinangun", "Randuacir", "Ngawen",
	"Bugel", "Dukuh", "Gendongan", "Kumpulrejo", "Pulutan", "Blotongan", "Noborejo", "Tingkir",
}

var payments = []string{"transfer bank", "qris", "cod", "ewallet", "kartu kredit", "virtual account"}

type weighted struct {
	value  string
	weight int
}

var statuses = []weighted{
	{"selesai", 80}, {"dikirim", 8}, {"diproses", 5}, {"dibatalkan", 5}, {"dikembalikan", 2},
}

var notes = []string{
	"tolong dikirim sore hari", "titip ke satpam kalau tidak ada orang", "bungkus kardus tebal ya",
	"minta nota terpisah untuk kantor", "hubungi dulu sebelum sampai", "pagar warna hijau, rumah paling ujung",
	"jangan lupa sambal sachet", "kirim hari sabtu saja", "alamat kantor, jam kerja 08.00 sampai 16.00",
	"untuk acara pengajian, mohon tepat waktu",
}

type productBase struct {
	name     string
	category string
	price    int64
	sizes    []string
}

var productBases = []productBase{
	{"Beras pulen", "sembako", 14000, []string{"1 kg", "2 kg", "5 kg", "10 kg"}},
	{"Minyak goreng", "sembako", 18000, []string{"500 ml", "1 liter", "2 liter"}},
	{"Gula pasir", "sembako", 16000, []string{"500 g", "1 kg"}},
	{"Tepung terigu", "sembako", 12000, []string{"500 g", "1 kg"}},
	{"Garam dapur", "sembako", 5000, []string{"250 g", "500 g"}},
	{"Telur ayam", "sembako", 28000, []string{"10 butir", "1 kg", "1 papan"}},
	{"Mi instan goreng", "makanan instan", 3200, []string{"1 bungkus", "isi 5", "dus isi 40"}},
	{"Bubur instan", "makanan instan", 9000, []string{"sachet", "isi 6"}},
	{"Kecap manis", "bumbu", 15000, []string{"135 ml", "275 ml", "600 ml"}},
	{"Sambal botol", "bumbu", 13000, []string{"135 ml", "275 ml"}},
	{"Santan instan", "bumbu", 7500, []string{"65 ml", "200 ml"}},
	{"Kopi bubuk", "minuman", 11000, []string{"100 g", "200 g", "400 g"}},
	{"Teh celup", "minuman", 9500, []string{"isi 25", "isi 50"}},
	{"Susu kental manis", "minuman", 12500, []string{"kaleng 370 g", "sachet"}},
	{"Air mineral", "minuman", 4500, []string{"600 ml", "1,5 liter", "galon 19 liter"}},
	{"Keripik singkong", "camilan", 8500, []string{"100 g", "250 g"}},
	{"Kerupuk udang", "camilan", 10000, []string{"200 g", "500 g"}},
	{"Biskuit kelapa", "camilan", 7000, []string{"150 g", "300 g"}},
	{"Sabun cuci piring", "kebersihan", 12000, []string{"400 ml", "800 ml"}},
	{"Detergen bubuk", "kebersihan", 22000, []string{"800 g", "1,8 kg"}},
	{"Pembersih lantai", "kebersihan", 15000, []string{"800 ml", "1,5 liter"}},
	{"Sabun mandi", "perawatan", 4500, []string{"batang", "isi 4"}},
	{"Sampo", "perawatan", 17000, []string{"170 ml", "340 ml"}},
	{"Pasta gigi", "perawatan", 11000, []string{"75 g", "190 g"}},
}

var variants = []string{"original", "hemat", "keluarga", "premium", "lokal", "baru"}
