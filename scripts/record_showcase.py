"""Runs a real user journey against the API and records every request/response."""
import json
import time
import urllib.error
import urllib.request

BASE = "http://localhost:8081"
REC = []


def call(name, method, path, body=None, token=None, headers=None, note="", keep=True):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    h.update(headers or {})
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
    t0 = time.perf_counter()
    try:
        res = urllib.request.urlopen(req)
        status, raw, rh = res.status, res.read(), res.headers
    except urllib.error.HTTPError as err:
        status, raw, rh = err.code, err.read(), err.headers
    ms = (time.perf_counter() - t0) * 1000
    try:
        parsed = json.loads(raw) if raw else None
    except ValueError:
        parsed = raw.decode()
    if keep:
        shown_h = {k: v for k, v in h.items() if k != "Content-Type" or data}
        REC.append(dict(id=name, method=method, path=path, req_headers=shown_h, req_body=body, status=status, ms=round(ms, 1),
                        res_headers={k: rh.get(k) for k in ("Content-Type", "X-Request-Id", "X-Ratelimit-Remaining", "Retry-After") if rh.get(k)},
                        res_body=parsed, note=note))
    print(f"{status} {method} {path} {ms:.1f}ms")
    return status, parsed


def data(r):
    return r[1]["data"]


# --- seed catalog as admin (not shown) ---
adm = data(call("seed-admin", "POST", "/api/v1/auth/login", {"email": "admin@example.com", "password": "Admin12345"}, keep=False))
A = adm["tokens"]["access_token"]
cats = {}
for c in ["Minuman", "Makanan Ringan", "Perlengkapan Dapur"]:
    cats[c] = data(call("seed", "POST", "/api/v1/categories", {"name": c}, A, keep=False))["id"]
seed = [
    ("Kopi Arabika Gayo 250g", "Biji kopi single origin dari dataran tinggi Gayo, Aceh. Sangrai medium, rasa fruity dengan aftertaste cokelat.", 85000, 24, "Minuman"),
    ("Teh Hijau Melati 100g", "Teh hijau dengan bunga melati asli, cocok diseduh panas atau dingin.", 32000, 40, "Minuman"),
    ("Cokelat Bubuk Premium 200g", "Kakao murni dari Jembrana, Bali. Tanpa gula tambahan.", 54000, 15, "Minuman"),
    ("Keripik Singkong Balado", "Keripik singkong renyah dengan bumbu balado khas Padang.", 18000, 60, "Makanan Ringan"),
    ("Kacang Mete Panggang 250g", "Mete Wonogiri dipanggang tanpa minyak.", 76000, 12, "Makanan Ringan"),
    ("French Press 600ml", "Kaca borosilikat dengan saringan stainless tiga lapis.", 149000, 8, "Perlengkapan Dapur"),
    ("Edisi Terbatas: Kopi Luwak 100g", "Stok sangat terbatas.", 450000, 1, "Minuman"),
]
pid = {}
for n, d, p, s, c in seed:
    pid[n] = data(call("seed", "POST", "/api/v1/products", {"name": n, "description": d, "price": p, "stock": s, "category_id": cats[c]}, A, keep=False))["id"]

# --- customer journey (recorded) ---
call("register", "POST", "/api/v1/auth/register", {"name": "Auldy", "email": "auldy@example.com", "password": "Rahasia123"}, note="Password di-hash bcrypt; respons tidak pernah memuat password.")
call("register-dup", "POST", "/api/v1/auth/register", {"name": "Auldy", "email": "auldy@example.com", "password": "Rahasia123"}, note="Email sudah dipakai → 409.")
call("register-weak", "POST", "/api/v1/auth/register", {"name": "A", "email": "bukan-email", "password": "123"}, note="Validasi per field → 422 dengan detail.")
call("login-wrong", "POST", "/api/v1/auth/login", {"email": "auldy@example.com", "password": "salah"}, note="Pesan sama untuk email/password salah, supaya penyerang tidak tahu email mana yang terdaftar.")
lg = call("login", "POST", "/api/v1/auth/login", {"email": "auldy@example.com", "password": "Rahasia123"}, note="Access token 15 menit + refresh token 7 hari.")
T = data(lg)["tokens"]["access_token"]
R = data(lg)["tokens"]["refresh_token"]
call("me", "GET", "/api/v1/users/me", token=T)
call("categories", "GET", "/api/v1/categories")
call("products", "GET", "/api/v1/products?page=1&limit=6", note="Request pertama: dibaca dari PostgreSQL lalu disimpan ke Redis.")
call("products-cached", "GET", "/api/v1/products?page=1&limit=6", note="Request kedua yang sama: diambil dari cache Redis.")
call("search", "GET", "/api/v1/products?search=kopi&limit=6", note="Dikirim app 400 ms setelah user berhenti mengetik (debounce).")
call("filter", "GET", f"/api/v1/products?category_id={cats['Makanan Ringan']}&limit=6")
kopi = pid["Kopi Arabika Gayo 250g"]
call("detail", "GET", f"/api/v1/products/{kopi}", note="Cache-aside: cek Redis dulu.")
call("detail-cached", "GET", f"/api/v1/products/{kopi}")
call("cart-add", "POST", "/api/v1/cart/items", {"product_id": kopi, "quantity": 2}, T, note="Total dihitung server dari harga terkini.")
call("cart-add-2", "POST", "/api/v1/cart/items", {"product_id": pid["Keripik Singkong Balado"], "quantity": 1}, T)
cart = data(call("cart", "GET", "/api/v1/cart", token=T))
item_kopi = [i for i in cart["items"] if i["product_id"] == kopi][0]["id"]
call("cart-update", "PUT", f"/api/v1/cart/items/{item_kopi}", {"quantity": 3}, T, note="Di app angka sudah berubah sebelum respons ini datang (optimistic update).")
call("cart-over", "PUT", f"/api/v1/cart/items/{item_kopi}", {"quantity": 99}, T, note="Melebihi stok → 409; app mengembalikan angka ke data server.")
call("cart-nologin", "GET", "/api/v1/cart", note="Tanpa token → 401.")
od = call("checkout", "POST", "/api/v1/orders", token=T, note="Satu transaksi: kunci keranjang, kurangi stok bersyarat, snapshot harga, kosongkan keranjang.")
order = data(od)
call("checkout-again", "POST", "/api/v1/orders", token=T, note="Tap kedua: keranjang sudah kosong → 400. Tidak ada pesanan ganda.")
call("cart-empty", "GET", "/api/v1/cart", token=T)
call("product-after", "GET", f"/api/v1/products/{kopi}", note="Stok berkurang 3 (24 → 21); cache produk sudah di-invalidate.")
key = "7f3a2c1e-5b8d-4e6f-9a0b-1c2d3e4f5a6b"
call("pay", "POST", "/api/v1/payments", {"order_id": order["id"]}, T, {"Idempotency-Key": key}, note="201: pembayaran baru dibuat.")
p2 = call("pay-replay", "POST", "/api/v1/payments", {"order_id": order["id"]}, T, {"Idempotency-Key": key}, note="Key sama dikirim ulang (mis. setelah timeout) → 200, ID pembayaran SAMA. Tidak ada tagihan ganda.")
pay = data(p2)
call("pay-other-key", "POST", "/api/v1/payments", {"order_id": order["id"]}, T, {"Idempotency-Key": "kunci-lain-123"}, note="Key berbeda saat masih ada pembayaran aktif → 409.")
call("pay-success", "POST", f"/api/v1/payments/{pay['id']}/simulate-success", token=T, note="Simulasi bank: pembayaran SUCCESS dan pesanan PAID dalam satu transaksi.")
call("pay-success-again", "POST", f"/api/v1/payments/{pay['id']}/simulate-success", token=T, note="Dikonfirmasi lagi → tetap SUCCESS, tidak diproses dua kali.")
call("order-paid", "GET", f"/api/v1/orders/{order['id']}", token=T)
call("orders", "GET", "/api/v1/orders?page=1&limit=10", token=T)

# --- race for the last item ---
u2 = data(call("seed", "POST", "/api/v1/auth/register", {"name": "Budi", "email": "budi@example.com", "password": "Rahasia123"}, keep=False))
T2 = data(call("seed", "POST", "/api/v1/auth/login", {"email": "budi@example.com", "password": "Rahasia123"}, keep=False))["tokens"]["access_token"]
luwak = pid["Edisi Terbatas: Kopi Luwak 100g"]
call("race-cart-a", "POST", "/api/v1/cart/items", {"product_id": luwak, "quantity": 1}, T, keep=False)
call("race-cart-b", "POST", "/api/v1/cart/items", {"product_id": luwak, "quantity": 1}, T2, keep=False)
call("race-a", "POST", "/api/v1/orders", token=T, note="Auldy checkout duluan: dapat stok terakhir.")
call("race-b", "POST", "/api/v1/orders", token=T2, note="Budi checkout setelahnya: stok 0 → 409, transaksi dibatalkan, tidak ada yang berubah.")
call("race-product", "GET", f"/api/v1/products/{luwak}", note="Stok 0, tidak pernah minus.")

# --- session: refresh rotation and reuse detection ---
call("expired", "GET", "/api/v1/cart", token=T[:-6] + "AAAAAA", note="Token rusak/kedaluwarsa → 401. Di app, interceptor menangkap ini…")
rf = call("refresh", "POST", "/api/v1/auth/refresh", {"refresh_token": R}, note="…lalu meminta pasangan token baru. Refresh token lama langsung dicabut (rotasi).")
call("refresh-reuse", "POST", "/api/v1/auth/refresh", {"refresh_token": R}, note="Refresh token lama dipakai lagi (tanda dicuri) → 401 dan SEMUA sesi user dicabut.")

# --- admin ---
call("admin-forbidden", "PATCH", f"/api/v1/orders/{order['id']}/status", {"status": "PROCESSING"}, data(rf)["access_token"] if isinstance(data(rf), dict) and "access_token" in data(rf) else T, note="Customer mencoba endpoint admin → 403.")
call("admin-process", "PATCH", f"/api/v1/orders/{order['id']}/status", {"status": "PROCESSING"}, A, note="Admin: PAID → PROCESSING.")
call("admin-invalid", "PATCH", f"/api/v1/orders/{order['id']}/status", {"status": "COMPLETED"}, A, note="Lompat status (PROCESSING → COMPLETED) ditolak 409.")
call("admin-stock", "PATCH", f"/api/v1/products/{luwak}/stock", {"stock": 5}, A, note="Restock; cache produk ikut diperbarui.")
call("health", "GET", "/health")

json.dump(REC, open("/home/claude/showcase/recordings.json", "w"), indent=1, ensure_ascii=False)
print(len(REC), "recorded")
