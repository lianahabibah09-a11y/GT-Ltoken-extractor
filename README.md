# Growtopia Google-OAuth Token Tool (clone)

Tool baris-perintah untuk mengambil **token login Growtopia (GGOV1)** lewat
alur OAuth Google, hasil reverse-engineering `growtopia-automation.exe`.
Dipisah jadi 2 tahap supaya **panen** dan **refresh** tidak saling
menyentuh browser: panen jarang, refresh murah.

> **Disclaimer.** Proyek edukasi / RE protokol. Memakai tool ini pada akun
> yang bukan milikmu melanggar ToS Growtopia dan bisa kena ban. Jangan
> pernah commit kredensial. Lihat bagian [Keamanan](#keamanan).

---

## Konsep: 3 jenis kredensial

| Aset | Umur | Dibuat oleh | Dipakai oleh |
|---|---|---|---|
| **SID Google** (cookie) | ±1 tahun; revoke jika ganti password / terdeteksi mencurigakan | `gt-login` | `gt-harvest`, fallback `gt-refresh` |
| **tokenUrl** (`/logon-name/<JWT>`) | **1x pakai** (sisi server GT) | `gt-harvest` | `gt-refresh` |
| **token GGOV1** | singkat (dipakai bot game) | `gt-refresh` | klien Growtopia |

Rantai: token GGOV hanya bisa lahir dari `tokenUrl + ?validate` →
`tokenUrl` hanya dicetak bila sesi Google restui OAuth → restu senyap
butuh SID di profil browser.

## Alur kerja

```
gt-login    accounts.txt          # 1x saat SID mati: login Google manual/auto -> profiles/<email>/
gt-harvest  accounts.txt --bank=N # panen N tokenUrl BELUM dipakai -> urls.txt (sentuh Google 1 rangkaian)
gt-refresh  emails.txt            # replay urls.txt via HTTP murni (NOL browser, NOL Google) -> tokens.txt
```

`gt-refresh` memakai bank sampai habis → kalau kosong/expired, fallback
ke browser SID silent-flow; kalau SID pun mati → `SID_MATI <email>`
(fast-fail, tidak timeout 7 menit).

## Arsitektur

```
main.go                 state machine browser (chromedp) + bank urls.txt + CLI
pkg/growtopia/login.go   handler HTTP murni:
                         server_data.php (UA UbiServices) -> meta|valKey
                         POST dashboard (packet wk/klv/meta/rid/mac, UA iPhone)
                           -> href tokenUrl (google/redirect)
pkg/growtopia/validate_test.go ParseValidate fixture (regex "token":"GGOV1...")
build_split.bat          1 codebase -> 3 exe, mode di-set via -ldflags defaultMode
config.json              {"login_mode":"auto"|"manual"}
```

Mode browser (`main.go: processAccount`) = loop max 100 langkah, tiap
iterasi cek URL + cookie, branch:

- `chooser/oauth/id` → klik akun (pointer-event CDP, SPA Google abaikan `.click()` sintetis)
- `signin/identifier` → isi email (native setter `HTMLInputElement` + dispatch `input`) → **nativeNext** (klik koordinat CDP asli; JS click ditolak form v3 → bug lama "password diam")
- `challenge/pwd` → isi password → nativeNext
- `consent/generic`, `services/oauth` → klik Continue
- `captcha|sorry` → fast-fail (Google flag akun/IP; perlu headless=false)
- `refresh` + halaman form → `SID_MATI`
- mode `harvest`: tiap URL `logon-name/` disimpan ke `urls.txt`, minta loginURL baru (SID membuat Google silent-approve lagi)
- mode `refresh`: URL `logon-name/<JWT>?validate` → `ParseValidate` → `tokens.txt`

## Build

Syarat: Go ≥ 1.22 (dev pakai 1.27), Chrome atau Edge (untuk chromedp).

```bat
build_split.bat           :: -> gt-login.exe gt-harvest.exe gt-refresh.exe
go vet ./...              :: clean
go test ./...             :: unit: bank roundtrip, loadConfig, tryValidate, fixture ParseValidate
```

Binary tunggal + flag manual juga jalan:

```bat
go build -o clone.exe .
clone.exe harvest accounts.txt --bank=5 --headless
clone.exe refresh emails.txt --headless
clone.exe login accounts.txt            :: default mode=login
```

Opsi: `--headless`, `--bank=N` (default 3), `--auto` / `--manual`
(override `config.json`). Manual = jendela selalu visible, bot tidak
menyentuh form — user isi email/sandi/2FA/captcha sendiri, bot lanjut
otomatis begitu cookie `SID` + `__Secure-1PSID` terdeteksi.

## File data

| File | Format | Catatan |
|---|---|---|
| `accounts.txt` | `email\|password` (password opsional utk refresh) | input panen |
| `profiles/<email>/` | user-data-dir Chrome | tempat SID nempel permanen |
| `urls.txt` | `email\|url\|mac\|rid\|wk`, awalan `!` = consumed | bank tokenUrl |
| `tokens.txt` | `email\|ltoken\|mac\|rid\|wk` | hasil akhir, siap dipakai bot game |
| `config.json` | `{"login_mode":"auto"}` | default mode form |

`mac/rid/wk` wajib sepasang dengan tokenUrl-nya — refresh menulis
kembali pasangan dari baris bank, bukan dari sesi terkini.

## Umur & kematian SID

- Cookie `SID/__Secure-1PSID/HSID/SSID` = 13 bulan (terbaca di `sid.json`); `NID` ±6 bulan.
- Kematian server-side (file tetap ada, validasi mati): ganti password, "sign out of all sessions", banned, login dari IP/geo berbeda (pemicu paling sering — jaga 1 IP per akun), challenge/2FA baru.
- Gejala di tool: `refresh` gagal dengan `SID_MATI` atau nyangkut `/signin/v2/...` → panen ulang `gt-login`.

## Troubleshooting

| Gejala | Sebab | Aksi |
|---|---|---|
| `captcha detected` saat login | flag Google (headless/IP/proxy) | `gt-login` tanpa `--headless`, atau `--manual`; IP tetap |
| `SID_MATI` | sesi Google dicabut | `gt-login accounts.txt` |
| `banked URL invalid` semua | bank habis/termakan | `gt-harvest --bank=N` |
| `Token is invalid` | replay tokenUrl ke-2 (single-use) | normal — gunakan bank berikutnya |
| `too many people` | rate-limit GT (~30s) | tool auto-tunggu 35s |
| `net::ERR_NAME_NOT_RESOLVED` / 403 | DNS/proxy (mis. WARP vs Akamai) | matikan VPN, coba `growtopia1/2` |
| Chrome websocket timeout | `--user-data-dir` relatif | sudah difix: path absolut wajib |

## Keamanan (kenapa banyak file di-.gitignore)

`accounts.txt` (kredensial Google), `tokens.txt`, `urls.txt`, dan
`profiles/` (cookie sesi Google = pembobol akun penuh) **tidak boleh
ke git**. Repo hanya berisi kode; semua file data dibuat lokal.
