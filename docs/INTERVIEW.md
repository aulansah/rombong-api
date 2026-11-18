# Panduan Interview

Catatan ini menjelaskan *kenapa* tiap bagian dirancang seperti sekarang,
alternatifnya, bug yang umum terjadi, dan contoh pertanyaan interview beserta
jawabannya. Nama file dan fungsi disebut supaya kamu bisa langsung menunjuk
kodenya saat ditanya.

## 0. Arsitektur dan fondasi

**Yang dibangun.** Modular monolith: satu binary, satu database, tapi kode
dipisah per modul (`internal/modules/*`). Tiap modul punya handler → service →
repository. Wiring manual di `internal/app/app.go`.

**Keputusan penting**

- Interface didefinisikan di sisi pemakai. `order/service.go` mendeklarasikan
  `StockManager`, dan `product.GormRepository` memenuhinya. Arah dependensi
  jadi satu arah (`product ← cart ← order ← payment`) dan unit test cukup
  memakai fake in-memory.
- Batas transaksi ada di service lewat `database.TxManager`. Transaksinya
  dititipkan di `context.Context`, lalu repository mengambilnya dengan
  `database.FromContext`. Service menentukan atomisitas tanpa meng-import
  GORM.
- Satu jalur error. Repository mengembalikan sentinel (`apperror.ErrNotFound`),
  service mengubahnya jadi `*apperror.AppError`, dan `response.ErrorHandler`
  merender JSON. Error tak dikenal di-log beserta request ID dan klien hanya
  menerima 500 generik.
- Config divalidasi saat start (`config.validate`): `JWT_SECRET` kurang dari 32
  karakter membuat aplikasi menolak jalan.

**Alternatif.** Microservices (terlalu berat untuk satu orang dan butuh
distributed transaction untuk checkout), framework DI seperti Wire/Fx (tidak
perlu untuk ukuran ini), `net/http` + chi (lebih standar, lebih banyak kode
manual), sqlc/pgx tanpa ORM (query lebih eksplisit, CRUD lebih panjang).

**Bug dan isu produksi yang umum**

- Menyimpan `*fiber.Ctx` atau string darinya melewati umur handler. Fiber
  memakai ulang buffer (fasthttp), jadi nilainya bisa berubah.
- `Updates(struct)` di GORM melewatkan zero value: stok 0 tidak tersimpan.
  Di sini selalu memakai `Select(...)` eksplisit atau `map`.
- Lupa memakai `database.FromContext` di repository, sehingga query berjalan
  di luar transaksi tanpa error apa pun.
- Pool koneksi terlalu kecil atau terlalu besar dibanding `max_connections`
  PostgreSQL.

**Pertanyaan interview**

1. *Kenapa modular monolith, bukan microservices?*
   Domainnya masih kecil dan timnya satu orang. Checkout menyentuh cart, stok
   dan order sekaligus; di monolith itu satu transaksi database, di
   microservices butuh saga. Batas modul sudah jelas, jadi kalau nanti perlu
   dipecah, kandidat pertamanya payment dan katalog.
2. *Apa bedanya handler, service dan repository di project ini?*
   Handler hanya mengurus HTTP: parsing, validasi, status code. Service berisi
   aturan bisnis dan batas transaksi. Repository satu-satunya yang tahu GORM
   dan SQL. Kalau besok ganti Fiber atau ganti ORM, hanya satu lapisan yang
   berubah.
3. *Bagaimana service menjalankan transaksi tanpa bergantung ke GORM?*
   Lewat `TxManager.WithinTx`. Ia membuka transaksi, menaruhnya di context,
   lalu repository membaca context itu. Kalau fungsi mengembalikan error,
   semuanya rollback.
4. *Kenapa interface didefinisikan di service, bukan di repository?*
   Itu idiom Go: interface milik pemakainya. Interface-nya jadi kecil, sesuai
   kebutuhan, dan modul tidak saling import melingkar.
5. *Bagaimana memastikan error internal tidak bocor ke klien?*
   Semua error lewat satu error handler. Hanya `AppError` yang pesannya
   dikirim. Selain itu dibalas "Internal server error" dan detailnya masuk log
   dengan request ID, sehingga tetap bisa dilacak.

## 1. Authentication dan authorization

**Yang dibangun.** Register, login, access token JWT (15 menit), refresh token
acak dengan rotasi, logout, middleware `Auth` dan `RequireRole`.

**Keputusan penting**

- Access token stateless, refresh token stateful. Access token tidak dicek ke
  database tiap request, tapi umurnya pendek. Refresh token disimpan
  (hash-nya) supaya bisa dicabut.
- Refresh token bukan JWT. Ia 32 byte acak, dan yang disimpan hanya SHA-256.
  SHA-256 cukup karena tokennya sudah acak 256 bit; bcrypt hanya perlu untuk
  password yang bisa ditebak.
- Rotasi dan deteksi pemakaian ulang (`auth.Service.Refresh`). `Revoke`
  memakai `WHERE revoked_at IS NULL`. Kalau token lama dipakai lagi, semua
  sesi user dicabut.
- Algoritma JWT dikunci ke HS256 (`jwt.WithValidMethods`) untuk menolak
  `alg: none`.
- Role tidak pernah diambil dari input. Admin hanya dibuat lewat seed
  (`EnsureAdmin`).
- Pesan login sama untuk email tidak dikenal dan password salah.

**Alternatif.** Session di Redis (mudah dicabut, tapi stateful), RS256
(cocok kalau service lain perlu memverifikasi token tanpa memegang secret),
OAuth2/OIDC lewat penyedia identitas.

**Bug dan isu produksi yang umum**

- Cek duplikat email dengan `SELECT` dulu lalu `INSERT`: dua request bersamaan
  sama-sama lolos. Di sini unique index yang menjadi penjaga, dan error
  duplikat diterjemahkan jadi 409.
- bcrypt memotong input di 72 byte. Validasi menolak password yang lebih
  panjang.
- Access token tidak bisa dicabut sebelum kedaluwarsa. Ini trade-off yang
  disengaja; solusinya deny-list di Redis.
- Selisih waktu respons login masih bisa membedakan email terdaftar atau
  tidak (bcrypt hanya dijalankan kalau user ada).

**Pertanyaan interview**

1. *Kenapa butuh refresh token kalau sudah ada JWT?*
   Supaya access token bisa berumur pendek. Kalau access token bocor,
   jendelanya hanya 15 menit. Refresh token disimpan di server sehingga bisa
   dicabut saat logout atau ganti password.
2. *Apa itu refresh token rotation dan kenapa penting?*
   Setiap refresh, token lama dicabut dan diganti baru. Kalau token dicuri dan
   dipakai dua pihak, salah satunya pasti memakai token yang sudah dicabut.
   Saat itu terdeteksi, semua sesi user saya cabut.
3. *Bedanya 401 dan 403?*
   401 berarti identitasnya tidak valid: token tidak ada, rusak, atau
   kedaluwarsa. 403 berarti identitasnya valid tapi role-nya tidak cukup. Di
   test middleware, route admin tanpa token menghasilkan 401, bukan 403.
4. *Kenapa refresh token di-hash dengan SHA-256, bukan bcrypt?*
   bcrypt sengaja lambat untuk melawan tebakan password. Refresh token adalah
   256 bit acak yang tidak mungkin ditebak, jadi hash cepat sudah aman dan
   lookup-nya bisa memakai index.
5. *Serangan apa yang dicegah dengan mengunci algoritma JWT?*
   Serangan `alg: none` dan algorithm confusion, di mana penyerang mengganti
   header algoritma supaya tanda tangan tidak diverifikasi dengan benar. Ada
   unit test untuk kasus ini di `pkg/token`.

## 2. Category dan product

**Yang dibangun.** CRUD kategori dan produk, pencarian, filter, sorting,
pagination, soft delete, manajemen stok.

**Keputusan penting**

- Kolom sort di-whitelist (`allowedSorts`). Nama kolom tidak bisa dijadikan
  parameter query, jadi ini satu-satunya tempat input bisa menjadi SQL
  injection.
- Wildcard `LIKE` di-escape (`escapeLike`), sehingga pencarian `%` tidak
  mengembalikan semua data.
- `limit` dibatasi maksimal 100 dan ada tie-breaker `ORDER BY id` supaya
  pagination stabil.
- Harga `BIGINT` dalam rupiah.
- Unique index parsial pada slug (`WHERE deleted_at IS NULL`).
- Field `stock` di request berupa pointer supaya 0 bisa dibedakan dari "tidak
  dikirim".
- Kategori yang masih punya produk tidak bisa dihapus (409).

**Alternatif.** Cursor/keyset pagination (lebih cepat untuk halaman dalam dan
stabil saat data berubah), full-text search atau `pg_trgm`, Elasticsearch
untuk katalog besar.

**Bug dan isu produksi yang umum**

- Offset pagination melambat di halaman jauh dan bisa melewatkan atau
  menggandakan baris saat data berubah.
- `ILIKE '%kata%'` tidak memakai B-tree index, jadi full scan di tabel besar.
- `PUT` produk menimpa stok secara absolut dan bisa balapan dengan checkout
  (tercatat di README sebagai keterbatasan).

**Pertanyaan interview**

1. *Bagaimana mencegah SQL injection di fitur sorting?*
   Nilai biasa selalu lewat parameter binding GORM. Nama kolom tidak bisa
   di-bind, jadi saya whitelist: hanya `created_at`, `price`, `name`. Input
   lain jatuh ke default.
2. *Kenapa harga tidak pakai float?*
   Float tidak bisa merepresentasikan pecahan desimal secara tepat, sehingga
   penjumlahan bisa selisih. Uang disimpan sebagai integer satuan terkecil.
3. *Apa kekurangan offset pagination?*
   Database tetap membaca semua baris yang dilewati, jadi makin dalam makin
   lambat. Kalau ada data baru masuk di antara dua request, baris bisa
   terlewat atau muncul dua kali. Solusinya keyset pagination.
4. *Kenapa soft delete, dan apa konsekuensinya?*
   Supaya produk bisa dipulihkan dan referensi lama tetap ada. Konsekuensinya
   setiap query harus menyaring `deleted_at`, dan unique index harus parsial
   supaya slug bisa dipakai lagi.
5. *Kenapa `stock` di DTO berupa pointer?*
   Supaya validator bisa membedakan stok 0 yang sah dari field yang tidak
   dikirim. Dengan `int` biasa keduanya sama-sama 0.

## 3. Shopping cart

**Yang dibangun.** Satu cart per user, tambah, ubah, hapus, kosongkan, total
dihitung saat dibaca.

**Keputusan penting**

- Cart tidak menyimpan harga maupun total. `cart.ToResponse` menghitungnya
  dari harga produk saat ini, sehingga tidak ada data turunan yang basi.
- Unique `(cart_id, product_id)`. Menambah produk yang sama menaikkan
  quantity lewat upsert.
- Cart dibuat saat pertama dipakai dengan `ON CONFLICT DO NOTHING`, aman dari
  dua request bersamaan.
- Kepemilikan dicek lewat cart milik pemanggil (`ownedItem`). ID item milik
  orang lain menghasilkan 404.
- Cart membaca produk langsung dari database, bukan dari cache.

**Alternatif.** Cart di Redis (cepat, cocok untuk cart anonim, tapi bisa
hilang), cart di sisi klien, atau reservasi stok saat masuk cart dengan batas
waktu.

**Bug dan isu produksi yang umum**

- Menganggap validasi stok di cart sebagai pengaman oversell. Itu hanya untuk
  kenyamanan user; penjaga sebenarnya ada di checkout.
- Produk dihapus atau stoknya turun setelah masuk cart. Field `available` di
  respons memberi tahu klien, dan checkout menolaknya.

**Pertanyaan interview**

1. *Kenapa total cart tidak disimpan di database?*
   Karena itu data turunan. Kalau disimpan, setiap perubahan item atau harga
   harus ikut memperbaruinya dan suatu saat pasti tidak sinkron. Dihitung
   saat dibaca selalu benar.
2. *Kalau stok sudah divalidasi di cart, kenapa divalidasi lagi saat
   checkout?*
   Antara add-to-cart dan checkout stok bisa berubah. Validasi di cart tidak
   mengunci apa pun. Yang menjamin adalah UPDATE bersyarat saat checkout.
3. *Bagaimana mencegah user mengubah cart orang lain?*
   Item selalu dicari dengan `cart_id` milik user yang login. Kalau ID item
   bukan miliknya, hasilnya 404, sama seperti ID yang tidak ada.
4. *Kenapa cart di PostgreSQL, bukan Redis?*
   Cart user login perlu awet dan konsisten dengan stok serta checkout yang
   transaksional. Redis lebih cocok untuk cart anonim berumur pendek.
5. *Apa yang terjadi kalau dua request menambah produk yang sama bersamaan?*
   Barisnya tetap satu karena ada unique constraint dan upsert. Quantity
   akhirnya mengikuti request yang terakhir menulis; untuk cart ini bisa
   diterima.

## 4. Order

**Yang dibangun.** Checkout transaksional, riwayat, cancel dengan pengembalian
stok, state machine status.

**Keputusan penting**

- Pengurangan stok atomik dalam satu statement
  (`product.GormRepository.DecreaseStock`):
  `UPDATE ... SET stock = stock - qty WHERE id = ? AND stock >= qty RETURNING *`.
  Pengecekan dan pengurangan tidak bisa disela request lain.
- Harga dan nama diambil dari `RETURNING`, yaitu baris yang sama dengan yang
  stoknya dikurangi. `POST /orders` tidak punya body.
- Baris cart dikunci dengan `SELECT ... FOR UPDATE` (`GetForCheckout`).
  Double-click checkout tidak menghasilkan dua order.
- Item diproses berurutan menurut `product_id` supaya dua checkout tidak
  saling deadlock.
- Perubahan status memakai compare-and-swap (`TransitionStatus`). Cancel yang
  dipanggil dua kali hanya mengembalikan stok sekali.
- Invalidasi cache dilakukan setelah commit.
- Order milik orang lain menghasilkan 404, bukan 403.

**Alternatif.** `SELECT ... FOR UPDATE` pada produk (lebih fleksibel, lock
lebih lama), optimistic locking dengan kolom `version` (perlu retry), isolasi
SERIALIZABLE (perlu retry dan lebih mahal), reservasi stok dengan batas
waktu.

**Bug dan isu produksi yang umum**

- Pola baca stok, cek di kode, lalu tulis: dua request sama-sama melihat stok
  1 dan sama-sama berhasil.
- Mengunci baris dengan urutan berbeda menyebabkan deadlock.
- Order PENDING yang tidak pernah dibayar menahan stok selamanya. Perlu
  worker yang membatalkannya otomatis.
- Menginvalidasi cache di dalam transaksi: pembaca lain bisa mengisi ulang
  cache dengan data lama sebelum commit.

**Pertanyaan interview**

1. *Bagaimana kamu mencegah overselling?*
   Stok dikurangi dengan satu UPDATE bersyarat `stock >= qty`. PostgreSQL
   mengunci baris itu selama UPDATE, jadi request kedua menunggu lalu
   mengevaluasi ulang syaratnya terhadap stok terbaru. Kalau tidak ada baris
   yang terpengaruh, checkout di-rollback. Ada test 20 pembeli berebut 5 unit:
   tepat 5 order dan stok berakhir 0.
2. *Kenapa tidak cukup dengan transaksi saja?*
   Transaksi menjamin atomisitas, bukan mencegah lost update. Di READ
   COMMITTED dua transaksi bisa sama-sama membaca stok 1. Yang mencegahnya
   adalah syarat di dalam UPDATE itu sendiri, atau lock eksplisit.
3. *Kenapa nama dan harga produk disalin ke `order_items`?*
   Order adalah catatan historis. Kalau harga produk berubah atau produknya
   dihapus, order lama tidak boleh ikut berubah. Ini denormalisasi yang
   disengaja.
4. *Bagaimana menghindari deadlock saat checkout banyak item?*
   Semua checkout mengunci baris produk dalam urutan yang sama, diurutkan
   menurut ID. Deadlock butuh dua transaksi yang mengunci dengan urutan
   berlawanan.
5. *Apa yang terjadi kalau satu dari tiga item ternyata habis?*
   Seluruh checkout gagal dengan 409, stok dua item lain dikembalikan oleh
   rollback, dan cart tidak berubah. Ada test untuk skenario ini.

## 5. Payment

**Yang dibangun.** Simulasi gateway: buat payment, sukses, gagal, kedaluwarsa,
dengan idempotency.

**Keputusan penting**

- `Idempotency-Key` wajib di `POST /payments`. Key yang sama mengembalikan
  payment yang sama (200, bukan 201). Unique index pada key yang menjaminnya
  saat request bersamaan; lookup sebelum insert hanya jalur cepat.
- Satu payment aktif per order, dijaga partial unique index
  `uq_payments_pending_order`.
- Transisi status dengan compare-and-swap (`Transition`,
  `WHERE status = 'PENDING'`). `simulate-success` yang dipanggil berkali-kali
  hanya berefek sekali.
- Payment SUCCESS dan order PAID berada dalam satu transaksi. Kalau order
  ternyata sudah dibatalkan, update payment ikut di-rollback.
- Jumlah bayar diambil dari order, bukan dari request.

**Alternatif.** Tabel idempotency terpisah yang menyimpan seluruh respons
(dipakai Stripe), outbox pattern untuk event setelah pembayaran, webhook
bertanda tangan dari gateway sungguhan.

**Bug dan isu produksi yang umum**

- Pola cek lalu insert tanpa unique constraint: dua request dengan key sama
  membuat dua payment. Test konkurensi di project ini menemukan variasi bug
  tersebut saat pengembangan: request kedua lolos pengecekan key, lalu
  menemukan payment PENDING milik request pertama dan membalas 409.
  Perbaikannya ada di `activePayment`: payment PENDING dengan key yang sama
  dianggap replay.
- Webhook gateway bisa datang dua kali atau tidak berurutan.
- Key yang sama dipakai untuk order berbeda; di sini ditolak dengan 409.

**Pertanyaan interview**

1. *Apa itu idempotency dan kenapa penting di payment?*
   Operasi yang dijalankan berkali-kali hasilnya sama dengan dijalankan
   sekali. Di jaringan, klien sering retry karena timeout padahal request
   pertama berhasil. Tanpa idempotency, user bisa tertagih dua kali.
2. *Bagaimana implementasinya di sini?*
   Dua lapis. Pembuatan payment memakai `Idempotency-Key` dengan unique index.
   Pemrosesan memakai UPDATE bersyarat `WHERE status = 'PENDING'`, sehingga
   hanya satu request yang benar-benar mengubah status.
3. *Kenapa pengecekan di kode saja tidak cukup?*
   Karena ada jeda antara cek dan insert. Dua request bisa sama-sama melihat
   "belum ada". Constraint di database yang menjadi sumber kebenaran, dan
   error duplikat ditangani sebagai replay.
4. *Bagaimana kalau payment sukses tapi update order gagal?*
   Tidak bisa terjadi sebagian, karena keduanya satu transaksi. Kalau order
   tidak lagi PENDING, transaksi di-rollback dan payment kembali PENDING.
5. *Apa bedanya dengan gateway sungguhan?*
   Hasil pembayaran datang lewat webhook yang harus diverifikasi tanda
   tangannya, bisa terlambat, terkirim ulang, atau tidak berurutan. Handler
   webhook harus idempotent dengan cara yang sama seperti `simulate-success`.

## 6. Redis: cache dan rate limiting

**Yang dibangun.** Cache detail produk, cache list produk berversi, rate
limiter per IP.

**Keputusan penting**

- Cache-aside (`product.Service.Get`): baca Redis; kalau miss baca
  PostgreSQL lalu isi Redis dengan TTL. Saat ada perubahan, key dihapus.
- Cache list berversi. Key list memuat nomor versi, dan setiap penulisan
  menjalankan `INCR`, sehingga semua key lama tidak terpakai lagi dan habis
  sendiri lewat TTL. Tidak perlu `KEYS` atau `SCAN`.
- Redis boleh mati. Error Redis hanya di-log, request lanjut ke database,
  rate limiter meloloskan request (fail-open), dan `/health` melaporkan
  `degraded`. Timeout klien 500 ms.
- TTL: detail 10 menit, list 2 menit.

**Alternatif.** Write-through (cache diperbarui saat menulis), read-through,
cache in-memory per instance, CDN untuk katalog publik, sliding window atau
token bucket untuk rate limit.

**Bug dan isu produksi yang umum**

- Cache stampede: banyak request miss bersamaan saat key populer habis.
  Solusinya singleflight atau TTL dengan jitter.
- Data basi kalau invalidasi gagal; di sini dibatasi oleh TTL.
- Rate limit per IP di belakang proxy akan melihat IP proxy kalau header
  tidak dikonfigurasi.
- Fixed window memungkinkan burst dua kali batas di perbatasan jendela.

**Pertanyaan interview**

1. *Jelaskan cache-aside.*
   Aplikasi yang mengatur cache. Saat membaca: cek cache, kalau tidak ada baca
   database lalu simpan ke cache. Saat menulis: tulis database lalu hapus key
   cache. Database tetap sumber kebenaran.
2. *Kenapa key dihapus, bukan diperbarui, saat ada perubahan?*
   Menghapus lebih aman terhadap balapan. Kalau dua penulisan memperbarui
   cache dalam urutan yang berbeda dari database, cache bisa berisi nilai
   lama selamanya. Dengan menghapus, pembaca berikutnya mengambil ulang dari
   database.
3. *Bagaimana menginvalidasi cache list yang key-nya banyak?*
   Dengan versi. Semua key list memuat nomor versi. Satu `INCR` membuat semua
   key lama tidak pernah dibaca lagi.
4. *Apa yang terjadi kalau Redis mati?*
   API tetap jalan, hanya lebih lambat. Ada unit test yang mematikan Redis
   dan memastikan baca dan tulis tetap sukses, dan saya sudah mencobanya pada
   server yang berjalan.
5. *Kenapa rate limiter fail-open?*
   Supaya gangguan Redis tidak menjatuhkan seluruh API. Untuk endpoint yang
   sangat sensitif seperti login, pilihan fail-closed bisa dipertimbangkan.

## 7. Testing dan deployment

**Yang dibangun.** Unit test dengan fake dan miniredis, integration test
dengan PostgreSQL sungguhan, Dockerfile multi-stage, Docker Compose dengan
health check, graceful shutdown.

**Keputusan penting**

- Hal yang bergantung pada perilaku database (lock, constraint, transaksi)
  diuji di database sungguhan, bukan di-mock.
- Test konkurensi memakai goroutine dan dijalankan dengan `-race`.
- Integration test dipisah dengan build tag `integration`, sehingga
  `go test ./...` tetap cepat tanpa layanan eksternal.
- Image runtime hanya berisi binary statis, berjalan sebagai user non-root.
- Migration di-embed ke binary dan bisa dijalankan terpisah
  (`-migrate-only`).
- Graceful shutdown menunggu request yang sedang berjalan.

**Pertanyaan interview**

1. *Kenapa tidak semua test memakai mock?*
   Mock hanya membuktikan kode memanggil fungsi yang saya harapkan. Jaminan
   seperti "stok tidak pernah minus" bergantung pada perilaku PostgreSQL, jadi
   harus diuji di PostgreSQL.
2. *Bagaimana menguji race condition?*
   Menjalankan banyak goroutine yang dilepas bersamaan, lalu memeriksa
   invarian di akhir: jumlah order sukses sama dengan stok, stok akhir 0.
   Ditambah flag `-race` dan dijalankan berulang.
3. *Kenapa multi-stage build?*
   Stage build berisi toolchain Go, stage runtime hanya binary. Image jadi
   kecil dan permukaan serangannya sempit.
4. *Apa gunanya graceful shutdown?*
   Saat deploy, proses menerima SIGTERM. Server berhenti menerima koneksi baru
   dan menyelesaikan request yang sedang berjalan, sehingga checkout tidak
   terputus di tengah.
5. *Kenapa migration SQL, bukan `AutoMigrate` GORM?*
   Skema jadi eksplisit, ter-versioning dan bisa di-review. `AutoMigrate`
   tidak menghapus kolom dan tidak bisa membuat partial index atau CHECK
   constraint yang dipakai project ini.
