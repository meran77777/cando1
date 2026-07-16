<p align="center">
  <img src="assets/logo.svg" alt="cando1" width="180">
</p>

<h1 align="center">cando1</h1>

<p align="center">
  <b>A high-performance, DPI-resistant, multiplexed TCP tunnel — written from scratch in Go.</b>
</p>

<p align="center">
  <a href="https://t.me/cando1tunnel">📣 Telegram: t.me/cando1tunnel</a> ·
  <a href="#license">MIT License</a> ·
  <a href="https://go.dev/dl/">Go 1.21+</a>
</p>

<p align="center">
  <a href="#english"><b>English</b></a> · <a href="#فارسی">فارسی</a>
</p>

---

<a id="english"></a>

## ⚡ One-command install

On any Ubuntu/Debian server (any CPU — amd64, arm64, armv7, …), run:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/meran77777/cando1/main/scripts/install.sh)
```

That single command detects your OS and CPU, installs the few packages it needs,
and downloads a **prebuilt binary straight from the GitHub release** for your
architecture — so on a Linux server **you never need Go installed**. Every Linux
CPU the installer recognises (amd64, arm64, armv7, armv6, 386, riscv64, ppc64le,
s390x) has a published binary, and its SHA-256 is verified before install. It
then enables **BBR** + tuned network buffers for speed, optionally installs a
**systemd** service, and drops you into the interactive setup wizard. Every
feature is included — all transports (`tls`/`wss`/`ws`/`tcp`/`kcp`), smux
multiplexing, the connection pool, and auto-reconnect. Re-running it upgrades in
place.

> Building from source is only a fallback (for an unusual arch or a network that
> can't reach the release). Set `CANDO1_METHOD=release` to install from the
> release **only** and fail loudly rather than ever build — guaranteeing Go is
> never touched.

<details>
<summary>Install options (environment variables)</summary>

```bash
# Skip the BBR/sysctl tuning:
CANDO1_BBR=0 bash <(curl -fsSL https://raw.githubusercontent.com/meran77777/cando1/main/scripts/install.sh)

# Install the systemd service without being asked:
CANDO1_SERVICE=1 bash <(curl -fsSL .../install.sh)

# Build from a different fork/branch:
CANDO1_REPO=youruser/cando1 CANDO1_BRANCH=main bash <(curl -fsSL .../install.sh)

# Install ONLY from the release, never build from source (no Go, ever):
CANDO1_METHOD=release bash <(curl -fsSL .../install.sh)

# Pin a specific release tag instead of the latest:
CANDO1_RELEASE=v1 bash <(curl -fsSL .../install.sh)
```
`CANDO1_METHOD` is `auto` (release, then source fallback), `release` (release
only), or `source` (always build). Other knobs: `CANDO1_BIN_DIR`,
`CANDO1_GO_VERSION`, `CANDO1_RELEASE`, `GOPROXY`, `GOSUMDB`.
</details>

## Why cando1

cando1 links two machines with a single encrypted, multiplexed carrier connection
and moves arbitrary TCP ports across it. It is built for hostile networks (e.g.
reaching the open internet from Iran): low ping, browser-indistinguishable TLS,
WebSocket/CDN camouflage, and rock-solid auto-reconnect.

| Capability | cando1 |
|---|---|
| Browser-fingerprinted TLS (uTLS: Chrome/Firefox/Safari/Edge/randomized) | ✅ |
| WebSocket-over-TLS carrier that looks like HTTPS/CDN traffic | ✅ |
| KCP (UDP + Reed-Solomon FEC) speed transport — beats TCP-over-TCP meltdown | ✅ |
| Stream multiplexing (smux) — one handshake, thousands of streams | ✅ |
| Parallel connection **pool** with least-loaded stream placement | ✅ |
| `TCP_NODELAY` everywhere + tuned smux buffers for low ping & high throughput | ✅ |
| chacha20 obfuscation for the raw-TCP transport | ✅ |
| Both **forward** (client-exposes-port) and **reverse** (server-exposes-port) tunnels | ✅ |
| Auto-reconnect with exponential backoff, keepalive, graceful shutdown | ✅ |
| Client-first, silent, replay-resistant handshake (defeats active probing) | ✅ |
| Guided wizard that writes a matching server+client config pair | ✅ |
| One-command installer + BBR tuning + systemd service | ✅ |
| Single static binary, zero runtime deps, cross-platform | ✅ |

The multiplexer is the key to latency: a new user connection costs **one stream
open** over the already-warm TLS session — not a fresh TCP + TLS handshake. The
pool spreads streams across several sessions so one slow stream never stalls the
others.

## The two topologies

cando1 has two roles — **server** (accepts the tunnel) and **client** (dials out
and keeps it alive) — and two forwarding directions.

**Scenario 1 — Client in Iran, forward chosen ports to a foreign server**

```
 users ──▶ [ IRAN client ] ══ encrypted mux tunnel ══▶ [ FOREIGN server ] ──▶ target
           listens :1194                                dials 127.0.0.1:1194
```
Iran runs the **client**; foreign runs the **server** (`allow_forward = true`).

**Scenario 2 — Client abroad, Iran is only the relay**

```
 users ──▶ [ IRAN server ] ══ encrypted mux tunnel ══▶ [ FOREIGN client ] ──▶ 127.0.0.1:8388
           public :8388                                  dials local service
```
Iran runs the **server** (public ports); foreign runs the **client** (real service).

Ready-made pairs for both directions, a Cloudflare-fronted `ws`/`wss` template,
and a copy-paste test pair live in [`examples/`](examples/).

## 🛡️ Avoid getting your IP filtered

> **If a fresh tunnel IP gets filtered within a day, it is almost always the
> setup — not the code.** The tunnel’s wire protocol is already hard to
> fingerprint (browser-cloned uTLS ClientHello, client-speaks-first silent
> handshake, no cleartext markers). What gets an IP burned is the **certificate
> and the endpoint**.

The default self-signed certificate + a **bare IP** + a made-up SNI like
`www.example.com` is the classic tell: an active prober connects, sees a cert
that chains to **no public CA** (or an SNI that doesn’t match the IP), and blocks
the address. Do one of these instead:

1. **Front the foreign server behind Cloudflare (recommended).** Use `ws`/`wss`,
   point a proxied (orange-cloud) DNS record at the server, and the tunnel looks
   like ordinary HTTPS to Cloudflare’s edge — and your origin IP is hidden. See
   [`examples/`](examples/) `cloudflare-*.toml`.
2. **Use a real domain + a real certificate.** Put a Let’s Encrypt cert in
   `[server.tls] cert`/`key` and set `insecure = false` on the client. Set `sni`
   to a real, resolvable domain — never a placeholder.
3. **Rotate a burned IP.** Once an IP is filtered it usually stays filtered;
   move to a fresh IP *and* fix the setup above, or it will be burned again.

Also enable BBR on both ends (the installer does this) and prefer `wss` behind a
CDN over a bare-IP `tls` endpoint on the most hostile networks.

## Transports — pick per censorship conditions

| Transport | Looks like | Use when |
|---|---|---|
| `tls`  | A browser opening HTTPS (uTLS ClientHello) | **Default.** Best all-round DPI resistance. Pair with a real cert/CDN. |
| `wss`  | A browser talking to a CDN over HTTPS/WebSocket | Maximum camouflage; ideal fronted through Cloudflare. |
| `ws`   | Plain HTTP WebSocket | Behind a TLS-terminating CDN / reverse proxy. |
| `tcp`  | High-entropy noise (`obfs=true`) or raw bytes | Simple setups or chaining behind another encrypted layer. |
| `kcp`  | Encrypted random UDP | **Speed mode.** Fastest on lossy links (UDP + FEC). Needs UDP open; less camouflaged. |

## Manual install / build

**From source (Go 1.21+):**

```bash
git clone https://github.com/meran77777/cando1
cd cando1
go build -o cando1 .        # or: make build
```

> Requires Go **1.21+** (uTLS uses `crypto/ecdh`, added in Go 1.20). On a
> restricted network, set `GOPROXY=https://goproxy.cn,direct` and `GOSUMDB=off`.

**With `go install`:** `go install github.com/meran77777/cando1@latest`

**Docker:** `docker build -t cando1 .` then
`docker run -v $PWD/cfg.toml:/cfg.toml -p 443:443 cando1 -c /cfg.toml`

Prebuilt binaries are attached to each GitHub release, each with a `.sha256`
checksum: **Linux** for amd64, arm64, armv7, armv6, 386, riscv64, ppc64le and
s390x, plus **Windows** (amd64) and **macOS** (amd64/arm64). The one-command
installer downloads the right one automatically — no Go required on the server.

## Quick start

Run with no arguments for the interactive wizard:

```bash
cando1
```

It asks a handful of questions, then writes `cando1-server.toml` and
`cando1-client.toml` — put each on the matching machine. Non-interactive:

```bash
cando1 server -c server.toml     # on the foreign server
cando1 client -c client.toml     # on the Iran client
cando1 gen-token                 # print a fresh token
```

## Maximizing speed

1. **Enable BBR on both ends** (the installer does this; or run
   [`scripts/tune-bbr.sh`](scripts/tune-bbr.sh)). The default CUBIC collapses
   under the packet loss typical of Iran↔Europe links; BBR keeps the pipe full.
2. **Use the `kcp` transport on lossy links.** A TCP tunnel puts TCP inside TCP;
   one lost packet triggers retransmit/backoff on *both* layers. `kcp` runs over
   UDP with Reed-Solomon FEC and reconstructs losses instead of stalling. Keep
   `token`/`fec_data`/`fec_parity` identical on both ends.
3. Raise `pool_size` for many concurrent connections, and `mux.*` buffers on
   very high bandwidth-delay links.

## Configuration reference

A config file has exactly one of `[server]` or `[client]`. Key fields:

- **server:** `bind_addr`, `transport`, `token`, `allow_forward`,
  `forward_whitelist`, `[server.tls] cert/key/self_name`, `[server.kcp]`,
  `[server.mux]`, `[[server.services]]`.
- **client:** `server_addr`, `transport`, `token`, `sni`, `host`, `fingerprint`,
  `insecure`, `pool_size`, `[client.kcp]`, `[client.mux]`,
  `[client.reconnect]`, `[[client.services]]`, `[[client.forwards]]`.

See [`examples/`](examples/) for fully-commented, ready-to-run files.

## Security & anti-detection notes

- **Client speaks first, server stays silent.** An active prober that connects
  gets nothing back — the port behaves like a black hole.
- **No cleartext markers.** Every handshake byte is a random nonce or an HMAC tag.
- **Mutual, replay-resistant authentication** (HMAC-SHA256, constant-time,
  nonce + timestamp bound).
- `tls`/`wss` give authenticated encryption end-to-end. The `tcp` `obfs` layer is
  confidentiality/camouflage only (unauthenticated XChaCha20) — prefer TLS when
  tampering is a concern.
- The server refuses link-local, cloud-metadata (`169.254.169.254`), multicast
  and unspecified forward targets; set `forward_whitelist` to lock it down.
- Use a long random `token` (`cando1 gen-token`) and keep configs `chmod 600`.

## Development

```bash
make test     # unit + end-to-end tests over every transport
make vet
make build
make release  # cross-compile into dist/
```

## License

MIT — see [LICENSE](LICENSE).

<br>

---

<a id="فارسی"></a>

<div dir="rtl" align="right">

# cando1 — تونل ضدّ DPI پرسرعت

**یک تونل TCP پرکارایی، مقاوم در برابر DPI و مالتی‌پلکس‌شده که کاملاً از صفر با Go نوشته شده.**

📣 کانال تلگرام: [t.me/cando1tunnel](https://t.me/cando1tunnel) — [بازگشت به English](#english)

cando1 دو ماشین را با یک اتصالِ حاملِ رمزنگاری‌شده و مالتی‌پلکس به هم وصل می‌کند و پورت‌های دلخواه TCP را از رویش عبور می‌دهد. برای شبکه‌های خصمانه (مثل رسیدن به اینترنت آزاد از ایران) ساخته شده: پینگ پایین، TLS غیرقابل‌تشخیص از مرورگر، استتار WebSocket/CDN و اتصال‌مجددِ خودکارِ مطمئن.

## ⚡ نصب با یک دستور

روی هر سرور Ubuntu/Debian (با هر CPU — amd64، arm64، armv7 و …) این را اجرا کنید:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/meran77777/cando1/main/scripts/install.sh)
```

همین یک دستور، سیستم‌عامل و معماری CPU شما را تشخیص می‌دهد، بسته‌های موردنیاز را نصب می‌کند، و **باینریِ آماده را مستقیماً از ریلیزِ گیت‌هاب** برای معماری شما دانلود می‌کند — پس روی یک سرور لینوکسی **هیچ‌وقت به نصب Go نیاز ندارید**. برای همه‌ی معماری‌های لینوکسی که نصّاب می‌شناسد (amd64، arm64، armv7، armv6، 386، riscv64، ppc64le، s390x) باینری منتشر شده و checksum (SHA-256) آن پیش از نصب بررسی می‌شود. سپس **BBR** و بافرهای شبکه را برای سرعت فعال می‌کند، در صورت تمایل یک سرویس **systemd** نصب می‌کند و شما را به ویزارد راه‌اندازی تعاملی می‌برد. همه‌ی قابلیت‌ها حاضرند — تمام ترنسپورت‌ها (`tls`/`wss`/`ws`/`tcp`/`kcp`)، مالتی‌پلکس smux، استخر اتصال (pool) و اتصال‌مجدد خودکار. اجرای دوباره‌ی دستور، نصب را به‌روزرسانی می‌کند.

> ساخت از سورس فقط یک fallback است (برای معماریِ غیرمعمول یا شبکه‌ای که به ریلیز دسترسی ندارد). با `CANDO1_METHOD=release` نصب فقط از ریلیز انجام می‌شود و در نبودِ باینری به‌جای ساخت، با خطا متوقف می‌شود — تضمین اینکه هرگز سراغ Go نرود.

<details>
<summary>گزینه‌های نصب (متغیرهای محیطی)</summary>

```bash
# بدون تنظیم BBR:
CANDO1_BBR=0 bash <(curl -fsSL .../install.sh)

# نصب سرویس systemd بدون پرسش:
CANDO1_SERVICE=1 bash <(curl -fsSL .../install.sh)

# ساخت از فورک/برنچ دیگر:
CANDO1_REPO=youruser/cando1 CANDO1_BRANCH=main bash <(curl -fsSL .../install.sh)

# نصب فقط از ریلیز، بدون ساخت از سورس (بدون Go):
CANDO1_METHOD=release bash <(curl -fsSL .../install.sh)

# پین‌کردن یک تگِ ریلیزِ مشخص به‌جای آخرین نسخه:
CANDO1_RELEASE=v1 bash <(curl -fsSL .../install.sh)
```
`CANDO1_METHOD` یکی از `auto` (ریلیز، سپس ساخت از سورس)، `release` (فقط ریلیز) یا `source` (همیشه ساخت) است. سایر تنظیم‌ها: `CANDO1_BIN_DIR`، `CANDO1_GO_VERSION`، `CANDO1_RELEASE`، `GOPROXY`، `GOSUMDB`.
</details>

## چرا cando1

مالتی‌پلکسر کلید پینگ پایین است: هر اتصال جدیدِ کاربر فقط هزینه‌ی **باز کردن یک استریم** روی نشستِ TLSِ ازقبل‌گرم‌شده را دارد، نه یک دست‌دهیِ کاملِ TCP + TLS. استخر، استریم‌ها را روی چند نشست پخش می‌کند تا یک استریم کند، بقیه را متوقف نکند.

| قابلیت | cando1 |
|---|---|
| TLS با اثرانگشت مرورگر (uTLS: کروم/فایرفاکس/سافاری/اج/تصادفی) | ✅ |
| حاملِ WebSocket-روی-TLS که مثل ترافیک HTTPS/CDN دیده می‌شود | ✅ |
| ترنسپورت سرعتیِ KCP (UDP + تصحیح خطای Reed-Solomon) | ✅ |
| مالتی‌پلکس استریم (smux) — یک دست‌دهی، هزاران استریم | ✅ |
| استخر اتصالِ موازی با توزیع کم‌بارترین استریم | ✅ |
| `TCP_NODELAY` همه‌جا + بافرهای تنظیم‌شده برای پینگ کم و توان بالا | ✅ |
| استتار chacha20 برای ترنسپورت TCP خام | ✅ |
| هر دو حالت **forward** و **reverse** | ✅ |
| اتصال‌مجدد خودکار با backoff نمایی، keepalive و خاموشی تمیز | ✅ |
| دست‌دهیِ کلاینت‌اول، خاموش و مقاوم در برابر replay (شکست پروبِ فعال) | ✅ |
| ویزاردِ راهنما که یک جفت کانفیگ سرور+کلاینت هماهنگ می‌سازد | ✅ |
| نصّابِ یک‌دستوری + تنظیم BBR + سرویس systemd | ✅ |
| باینریِ ایستای واحد، بدون وابستگیِ زمان‌اجرا، چندسکویی | ✅ |

## دو توپولوژی

cando1 دو نقش دارد — **سرور** (که تونل را می‌پذیرد) و **کلاینت** (که به بیرون وصل می‌شود و تونل را زنده نگه می‌دارد) — و دو جهتِ فورواردینگ.

**سناریو ۱ — کلاینت در ایران، فوروارد پورت‌های انتخابی به سرور خارج**

```
 کاربران ──▶ [ کلاینت ایران ] ══ تونل رمزنگاری‌شده ══▶ [ سرور خارج ] ──▶ سرویس مقصد
             پورت :1194                                  dials 127.0.0.1:1194
```
ایران **کلاینت** را اجرا می‌کند؛ خارج **سرور** را (`allow_forward = true`).

**سناریو ۲ — کلاینت در خارج، ایران فقط رله است**

```
 کاربران ──▶ [ سرور ایران ] ══ تونل رمزنگاری‌شده ══▶ [ کلاینت خارج ] ──▶ 127.0.0.1:8388
             پورت عمومی :8388                            سرویس واقعی محلی
```
ایران **سرور** را اجرا می‌کند (پورت‌های عمومی)؛ خارج **کلاینت** را (سرویس واقعی).

جفت‌های آماده برای هر دو جهت، یک قالبِ `ws`/`wss` پشت Cloudflare و یک جفت تست کپی-پیستی در پوشه‌ی [`examples/`](examples/) هست.

## 🛡️ جلوگیری از فیلتر شدن آی‌پی

> **اگر یک آی‌پیِ تازه ظرف یک روز فیلتر می‌شود، تقریباً همیشه مقصر «تنظیمات» است، نه کد.** پروتکلِ روی سیمِ تونل ازقبل سخت‌قابل‌تشخیص است (ClientHello کلون‌شده از مرورگر، دست‌دهیِ خاموشِ کلاینت‌اول، بدون نشانه‌ی متن‌ساده). چیزی که آی‌پی را می‌سوزاند، **گواهی و نقطه‌ی اتصال** است.

ترکیبِ گواهیِ self-signed پیش‌فرض + یک **آی‌پیِ خام** + یک SNI ساختگی مثل `www.example.com` نشانه‌ی کلاسیک است: پروبِ فعال وصل می‌شود، گواهی‌ای می‌بیند که به **هیچ CA عمومی** زنجیر نمی‌شود (یا SNIای که با آی‌پی نمی‌خواند) و آدرس را مسدود می‌کند. به‌جایش یکی از این‌ها را انجام دهید:

1. **سرور خارج را پشت Cloudflare بگذارید (پیشنهادی).** از `ws`/`wss` استفاده کنید، یک رکورد DNSِ پروکسی‌شده (ابر نارنجی) به سرور اشاره دهید؛ آنگاه تونل برای لبه‌ی Cloudflare مثل HTTPS معمولی دیده می‌شود و آی‌پیِ اصلی شما هم پنهان می‌ماند. نمونه‌ها: `cloudflare-*.toml` در [`examples/`](examples/).
2. **دامنه‌ی واقعی + گواهی واقعی.** یک گواهی Let’s Encrypt در `[server.tls] cert`/`key` بگذارید و روی کلاینت `insecure = false` کنید. `sni` را یک دامنه‌ی واقعی و قابل‌resolve بگذارید، نه placeholder.
3. **آی‌پیِ سوخته را عوض کنید.** آی‌پیِ فیلترشده معمولاً فیلتر می‌ماند؛ به یک آی‌پیِ تازه بروید **و** تنظیمات بالا را درست کنید، وگرنه دوباره می‌سوزد.

همچنین BBR را روی هر دو طرف فعال کنید (نصّاب این کار را می‌کند) و روی خصمانه‌ترین شبکه‌ها `wss` پشت CDN را به `tls` روی آی‌پیِ خام ترجیح دهید.

## ترنسپورت‌ها — بر اساس شرایط سانسور انتخاب کنید

| ترنسپورت | شبیه چیست | چه زمانی |
|---|---|---|
| `tls`  | مرورگری که HTTPS باز می‌کند | **پیش‌فرض.** بهترین مقاومت کلی؛ با گواهی واقعی/CDN جفت کنید. |
| `wss`  | مرورگری که با CDN روی HTTPS/WebSocket حرف می‌زند | بیشترین استتار؛ ایده‌آل پشت Cloudflare. |
| `ws`   | WebSocket سادهٔ HTTP | پشت CDN/پراکسیِ خاتمه‌دهنده‌ی TLS. |
| `tcp`  | نویزِ پرآنتروپی (`obfs=true`) یا بایت خام | راه‌اندازی‌های ساده یا زنجیره پشت لایه‌ی رمز دیگر. |
| `kcp`  | UDP تصادفیِ رمزنگاری‌شده | **حالت سرعت.** سریع‌ترین روی لینک‌های پرافت؛ نیاز به UDP باز؛ کم‌استتارتر. |

## نصب دستی / ساخت

**از سورس (Go نسخه ۱.۲۱ به بالا):**

```bash
git clone https://github.com/meran77777/cando1
cd cando1
go build -o cando1 .        # یا: make build
```

> به Go **۱.۲۱+** نیاز است. روی شبکه‌ی محدود:
> `GOPROXY=https://goproxy.cn,direct` و `GOSUMDB=off` را ست کنید.

**با `go install`:** `go install github.com/meran77777/cando1@latest`

**داکر:** `docker build -t cando1 .` سپس
`docker run -v $PWD/cfg.toml:/cfg.toml -p 443:443 cando1 -c /cfg.toml`

باینری‌های آماده (هرکدام با فایل `.sha256`) در هر ریلیز گیت‌هاب پیوست شده‌اند: **Linux** برای amd64، arm64، armv7، armv6، 386، riscv64، ppc64le و s390x، به‌علاوه‌ی **Windows** (amd64) و **macOS** (amd64/arm64). نصّابِ یک‌دستوری به‌صورت خودکار باینریِ درست را دانلود می‌کند — بدون نیاز به Go روی سرور.

## شروع سریع

بدون آرگومان اجرا کنید تا ویزارد تعاملی بالا بیاید:

```bash
cando1
```

چند سؤال می‌پرسد و بعد `cando1-server.toml` و `cando1-client.toml` را می‌نویسد — هرکدام را روی ماشین متناظرش بگذارید. حالت غیرتعاملی:

```bash
cando1 server -c server.toml     # روی سرور خارج
cando1 client -c client.toml     # روی کلاینت ایران
cando1 gen-token                 # چاپ یک توکن تازه
```

## بیشینه‌کردن سرعت

1. **BBR را روی هر دو طرف فعال کنید** (نصّاب انجام می‌دهد؛ یا [`scripts/tune-bbr.sh`](scripts/tune-bbr.sh) را اجرا کنید). CUBIC پیش‌فرض زیر افتِ بسته‌ی لینک‌های ایران↔اروپا فرومی‌پاشد؛ BBR لوله را پر نگه می‌دارد.
2. **روی لینک‌های پرافت از ترنسپورت `kcp` استفاده کنید.** تونلِ TCP یعنی TCP داخل TCP؛ یک بسته‌ی گم‌شده روی *هر دو* لایه backoff می‌سازد. `kcp` روی UDP با FEC اجرا می‌شود و گم‌شده‌ها را بازسازی می‌کند. `token`/`fec_data`/`fec_parity` را روی دو طرف یکسان نگه دارید.
3. برای اتصال‌های همزمانِ زیاد `pool_size` را بالا ببرید و روی لینک‌های پرتأخیر-پرپهنا بافرهای `mux.*` را.

## مرجع پیکربندی

هر فایل کانفیگ دقیقاً یکی از `[server]` یا `[client]` را دارد. فیلدهای کلیدی و فایل‌های نمونه‌ی کامل و آماده‌ی اجرا در [`examples/`](examples/) هستند (جدول کامل در بخش انگلیسی).

## نکات امنیت و ضدتشخیص

- **کلاینت اول حرف می‌زند، سرور ساکت می‌ماند.** پروبِ فعالی که وصل شود چیزی دریافت نمی‌کند — پورت مثل سیاه‌چاله رفتار می‌کند.
- **بدون نشانه‌ی متن‌ساده.** هر بایتِ دست‌دهی یک nonce تصادفی یا تگ HMAC است.
- **احراز هویت دوطرفه و مقاوم در برابر replay** (HMAC-SHA256، زمان‌ثابت، مقیدشده به nonce + timestamp).
- `tls`/`wss` رمزنگاریِ احرازشده‌ی سرتاسری می‌دهند. لایه‌ی `obfs` روی `tcp` فقط محرمانگی/استتار است (XChaCha20 بدون احراز) — وقتی دستکاری مهم است TLS را ترجیح دهید.
- سرور مقاصدِ link-local، متادیتای ابری (`169.254.169.254`)، multicast و نامشخص را رد می‌کند؛ برای محدودسازی `forward_whitelist` بگذارید.
- توکنِ بلندِ تصادفی (`cando1 gen-token`) و کانفیگ‌ها را `chmod 600` نگه دارید.

## لایسنس

MIT — فایل [LICENSE](LICENSE).

</div>
