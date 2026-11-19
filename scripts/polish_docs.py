"""Make the Rombong docs read like professional technical writing.

Removes decorative emoji, tidies em dashes, and swaps casual or chatty
phrasing for neutral Indonesian. Arrows used as flow notation (A → B) stay.
Usage: python3 polish_copy.py file.html [...]
"""
import re
import sys

# Specific phrases first (order matters).
PHRASES = [
    ("<td>🔒 admin</td>", "<td>admin</td>"),
    ("<td>🔒</td>", "<td>login</td>"),
    ("Tanpa Bisa Ngoding", "untuk Non-Programmer"),
    ("tanpa harus bisa ngoding", "tanpa latar belakang pemrograman"),
    ("Urutan ngoding dari nol", "Urutan pengerjaan dari nol"),
    ("Urutan ngoding", "Urutan pengerjaan"),
    ("sebelum ngoding", "sebelum menulis kode"),
    ("ngoding", "menulis kode"),
    ("Gambaran besar: anggap saja restoran", "Gambaran besar: analogi restoran"),
    ("anggap saja", "dapat dianalogikan dengan"),
    ("waktu kamu menekan", "saat pengguna menekan"),
    ("Middleware (satpam)", "Middleware"),
    ("Kamu punya daftar", "Daftar"),
    ("<h2>🎉 Selesai!</h2>", "<h2>Selesai</h2>"),
    ("Kamu sudah membangun", "Sampai di sini, Anda telah membangun"),
    ("di laptopmu sampai", "di laptop sampai"),
    ("progresmu disimpan", "progres disimpan"),
    ("Belanja dengan akunmu", "Masuk untuk lanjut belanja produk lokal favoritmu."),
    ("(seperti contohmu)", ""),
    ('"passwordmu"', '"password_anda"'),
    ("paling gampang", "paling mudah"),
    ("(bikin file setelan)", "(buat file setelan)"),
    ("jalan kok", "berjalan normal"),
    ("Tidak ketemu", "Tidak ditemukan"),
    ("Selamat!", "Selesai."),
    # Analogy markers become an explicit label instead of an emoji.
    ("🍽️ ", "Analogi: "),
]

# Headings and labels reworded into neutral documentation language.
PHRASES += [
    ("<h1>Kenapa kodenya <span>ditulis begini?</span></h1>", "<h1>rombong-api: penjelasan kode</h1>"),
    ("<h1>Flutter + <span>Clean Architecture + Riverpod</span></h1>", "<h1>rombong-app: penjelasan kode</h1>"),
    ('data-view="kenapa">Kenapa begini?', 'data-view="kenapa">Keputusan desain'),
    ("Kenapa API ini ada?", "Latar belakang API"),
    ("Kenapa aplikasi ini ada?", "Latar belakang aplikasi"),
    ("Kenapa tiap modul ada?", "Peran setiap modul"),
    ("Kenapa tiap bagian ada?", "Peran setiap bagian"),
    ("Kenapa pakai Docker?", "Alasan memakai Docker"),
    ("Kenapa Dockerfile-nya multi-stage?", "Dockerfile multi-stage"),
    ("Kenapa teknologi ini?", "Pilihan teknologi"),
    ("Kenapa package ini?", "Pilihan package"),
    ("Kenapa Clean Architecture?", "Alasan memakai Clean Architecture"),
    ("Kenapa dipakai di sini?", "Alasan pemakaian"),
    ("Kenapa dipakai?", "Alasan pemakaian"),
    ("Kenapa begini:", "Alasan desain:"),
    ("Kenapa Riverpod, bukan Bloc?", "Riverpod dibanding Bloc"),
    ("FRD · FSD · System Design · Sequence &amp; Flow · Unit Test · Integration Test · Stress Test",
     "Mencakup FRD, FSD, system design, sequence dan flow, unit test, integration test, serta stress test."),
    ("Keranjang: terasa instan", "Keranjang dengan optimistic update"),
    ("Katalog: cari, filter, scroll tanpa ujung", "Katalog: pencarian, filter, infinite scroll"),
    ("Router dengan penjaga", "Router dengan route guard"),
    ("Bungkus dengan Docker", "Kemas dengan Docker"),
    ("Peralatan bersama", "Paket utilitas bersama"),
    ("perintah yang tinggal disalin", "perintah yang dapat disalin"),
    ("Docker &amp; Makefile, pelan-pelan", "Docker dan Makefile"),
    ("Docker & Makefile, pelan-pelan", "Docker dan Makefile"),
    ("Makefile = tombol remote untuk perintah panjang", "Makefile: pintasan untuk perintah panjang"),
    ('File-file "aneh" di folder paling luar', "File konfigurasi di folder utama"),
    ("File-file &quot;aneh&quot; di folder paling luar", "File konfigurasi di folder utama"),
    ("Peta folder: klik untuk tahu isinya", "Peta folder"),
    ("Ikuti satu klik:", "Alur satu aksi:"),
    ("Ikuti satu klik", "Alur satu aksi"),
    ("File-file aneh", "File konfigurasi"),
    (':"ecommerce")+" — Langkah "', ':"rombong")+" | Langkah "'),
    ("Cara menjalankan, versi singkat", "Cara menjalankan (ringkas)"),
    ("Gambaran: apa yang akan menyala", "Komponen yang dijalankan"),
    ('<span class="n">!</span>Kalau ada masalah', '<span class="n">6</span>Pemecahan masalah'),
    ("Nyalakan backend", "Jalankan backend"),
    ("Coba alur lengkapnya", "Uji alur lengkap"),
    ("Coba alur lengkap", "Uji alur lengkap"),
]

WORDS = [
    (r"\bKamu\b", "Anda"),
    (r"\bkamu\b", "Anda"),
    (r"\bcuma\b", "hanya"),
    (r"\bCuma\b", "Hanya"),
    (r"\bgampang\b", "mudah"),
    (r"\bbikin\b", "buat"),
]

CIRCLED = {chr(0x2460 + i): str(i + 1) for i in range(20)}

# Pictographic emoji and decorative symbols. Typographic marks that carry
# meaning in UI mock-ups (✓ ✗ ✕ ☰ and arrows) are deliberately not listed.
EMOJI = re.compile(
    "(?:[\U0001F300-\U0001FAFF☀-☯☱-⛿✀-✒"
    "✔✖✘-➿⭐⏰-⏺▶↺↻↩➜]"
    "[️‍]?)+[  ]?"
)


def polish(s: str, keep_ui_glyphs: bool = False) -> str:
    s = s.replace(".pimg{height:86px;display:flex;align-items:center;justify-content:center;font-size:34px;",
                  ".pimg{height:86px;display:flex;align-items:center;justify-content:center;font-size:30px;font-weight:800;color:#7A2E0E;font-family:'Plus Jakarta Sans',sans-serif;")
    s = s.replace('class="pimg" style="height:180px;font-size:72px"', 'class="pimg" style="height:180px;font-size:56px"')
    # Showcase phone mock: product "photos" become monograms, not emoji.
    s = s.replace('"Kopi":"☕","Teh":"🍵","Cokelat":"🍫","Keripik":"🥔","Kacang":"🥜","French":"🫖","Edisi":"✨"',
                  '"Kopi":"K","Teh":"T","Cokelat":"C","Keripik":"K","Kacang":"K","French":"F","Edisi":"E"')
    s = s.replace('<div style="font-size:64px">✅</div>',
                  '<div style="width:64px;height:64px;margin:0 auto;border-radius:50%;background:#1F6F5C;color:#fff;display:flex;align-items:center;justify-content:center;font-size:32px;font-weight:800">✓</div>')
    for a, b in PHRASES:
        s = s.replace(a, b)
    for a, b in WORDS:
        s = re.sub(a, b, s)

    # Circled step numbers: "① Nyalakan" -> "1. Nyalakan", lone "①" -> "1".
    s = re.sub("([①-⑳]) ", lambda m: CIRCLED[m.group(1)] + ". ", s)
    s = re.sub("[①-⑳]", lambda m: CIRCLED[m.group(0)], s)

    if keep_ui_glyphs:  # VS Code edition: its ▶ run button is editor chrome.
        s = re.sub(EMOJI, lambda m: m.group(0) if m.group(0).startswith("▶") else "", s)
    else:
        s = EMOJI.sub("", s)

    # Arrows appended to buttons/links ("Lanjut →", "file lengkap ↗", "← Kembali").
    s = re.sub(r"[  ]*[→↗](?=\s*(</button>|</a>|`?\s*</button>|\"\s*:\s*\"|\"\}))", "", s)
    s = re.sub(r"\s*→(?=</button>)", "", s)
    s = re.sub(r"(?<=>)[  ]*←[  ]+(?=\w)", "", s)
    s = s.replace("SWIPE NEXT →", "SWIPE NEXT")

    # Em dashes.
    s = re.sub(r"(<title>[^<]*?) — ", r"\1: ", s)
    s = re.sub(r"(<h[1-4][^>]*>(?:(?!</h).)*?) — ", r"\1: ", s)
    s = re.sub(r"(Gambar \d+(?:\.\d+)*) — ", r"\1. ", s)
    s = re.sub(r"(</code>|</b>|</strong>|</a>)\s+— ", r"\1: ", s)
    s = s.replace(" — ", ", ")

    return s


if __name__ == "__main__":
    for path in sys.argv[1:]:
        text = open(path, encoding="utf-8").read()
        out = polish(text, keep_ui_glyphs="vscode" in path)
        open(path, "w", encoding="utf-8").write(out)
        print(f"{path}: {len(text)} -> {len(out)}")
