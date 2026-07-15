# cando1 example configurations

Every tunnel is a pair: one **server** config and one **client** config that
share the same `token`. Copy each file to the matching machine and run:

```bash
cando1 server -c <server-file>.toml     # on the SERVER machine
cando1 client -c <client-file>.toml     # on the CLIENT machine
```

## Which direction do I want?

There are two forwarding directions. Both can carry the same end result
(Iranian users reach a service that exits abroad); they differ in **which side
runs the public port** and **which side dials out**.

### FORWARD — client in Iran chooses the ports
```
users ─▶ [ IRAN = client ] ══ tunnel ══▶ [ FOREIGN = server ] ─▶ target service
         listens :8443                     dials 127.0.0.1:8000
```
The client (Iran) opens local ports and forwards them out to the foreign
server. Use when the foreign server is your exit and you decide the ports.

### REVERSE — services abroad, Iran only relays  ← "put users/services abroad"
```
users ─▶ [ IRAN = server ] ══ tunnel ══▶ [ FOREIGN = client ] ─▶ 127.0.0.1 service
         public :8443                      (dials out to Iran)   the real service
```
The server (Iran) exposes public ports; the client (foreign) holds the real
service and dials **out** to Iran. Iran runs **no** service — it is pure relay.
This is the "my users/services live abroad, Iran is only a tunnel" setup.

> Yes — reverse tunneling is fully supported. Pick the pair below that matches.

## Files in this folder

| File | Role | Topology |
|------|------|----------|
| `scenario1-foreign-server.toml` / `scenario1-iran-client.toml` | generic | FORWARD (TLS) |
| `scenario2-iran-server.toml` / `scenario2-foreign-client.toml` | generic | REVERSE (WSS) |
| `testbed-reverse-iran-server.toml` / `testbed-reverse-germany-client.toml` | **ready-to-test** | REVERSE (TLS, real IPs) |
| `testbed-reverse-iran-server-kcp.toml` / `testbed-reverse-germany-client-kcp.toml` | **speed mode** | REVERSE (KCP/UDP+FEC) |
| `testbed-forward-germany-server.toml` / `testbed-forward-iran-client.toml` | **ready-to-test** | FORWARD (TLS, real IPs) |
| `cloudflare-foreign-server.toml` / `cloudflare-iran-client.toml` | template | FORWARD behind Cloudflare (ws/wss) |

## Ready-to-test pair (bare IPs, no domain needed)

The `testbed-*` files are filled in for two real servers:

- **Iran:** `78.39.40.30`
- **Germany:** `91.107.226.85`

They use the `tls` transport, which works directly with an IP — no domain
required. To verify a tunnel end to end:

```bash
# 1) On Germany, start any test service:
python3 -m http.server 8000

# 2a) REVERSE test:
#     Iran:     cando1 server -c testbed-reverse-iran-server.toml
#     Germany:  cando1 client -c testbed-reverse-germany-client.toml
# 2b) FORWARD test:
#     Germany:  cando1 server -c testbed-forward-germany-server.toml
#     Iran:     cando1 client -c testbed-forward-iran-client.toml

# 3) From anywhere:
curl http://78.39.40.30:8443
#    -> you should get Germany's directory listing through the tunnel.
```

## Cloudflare-fronted (ws / wss)

Fronting the **foreign** server behind Cloudflare hides its IP and makes the
tunnel look like ordinary HTTPS traffic to Cloudflare's edge — strong
camouflage. It needs a domain on Cloudflare (any free plan).

1. Add your domain to Cloudflare.
2. DNS: `A  tunnel.YOURDOMAIN.com -> <foreign-server-IP>`, **Proxied (orange cloud)**.
3. SSL/TLS mode **Flexible** (user↔CF = HTTPS, CF↔origin = HTTP:80) → origin runs
   `transport = "ws"` on port 80 (`cloudflare-foreign-server.toml`).
   Or **Full** → origin runs `transport = "wss"` on 443 with a cert.
4. WebSockets are on by default. The client (`cloudflare-iran-client.toml`)
   connects to `tunnel.YOURDOMAIN.com:443` with `transport = "wss"` and
   `insecure = false` (Cloudflare has a valid certificate).
5. Recommended: firewall the origin so the proxied port only accepts
   [Cloudflare IP ranges](https://www.cloudflare.com/ips/).

## Speed mode (KCP)

For the fastest tunnel on a lossy Iran↔Europe link, use the `*-kcp.toml` pair
(`transport = "kcp"`). KCP runs over UDP with forward error correction, avoiding
the TCP-over-TCP meltdown that slows plain-TLS tunnels. Requirements:

- Open the UDP port in the firewall (e.g. `ufw allow 443/udp`).
- Keep `token`, `fec_data` and `fec_parity` **identical** on both ends.
- Also run `../scripts/tune-bbr.sh` on both servers for another big boost.

Trade-off: KCP is encrypted random UDP — less protocol-camouflaged than
`tls`/`wss` and needs UDP open. If your network blocks/throttles UDP, stay on
`tls`/`wss`.

## Security reminder

- Change every `token` to your own (`cando1 gen-token`). The tokens in these
  files are examples — rotate them.
- For the strongest anti-DPI camouflage on the direct (non-Cloudflare)
  transports, use a **real certificate** for a domain you control
  (`[server.tls] cert`/`key`) instead of the self-signed default.
