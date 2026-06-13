# turn-proxy-go

UDP tunnel that disguises traffic as WebRTC media. The **client** relays it through VK Calls' TURN; the **server** is a plain public UDP listener. Go port of the original Rust turn-proxy.

## Layout

Module `github.com/turn-proxy/turn-proxy`, one binary (`cmd/turn-proxy`). The
broker is a **separate module/repo** — `github.com/turn-proxy/turn-broker`,
depended on as a normal remote module (see [Broker repo](#broker-repo)).

| Path | Purpose |
|---|---|
| `cmd/turn-proxy` | The actual UDP proxy. Commands: `run` (client, default), `serve` (server), `validate` |
| `internal/client` | `Run`: local UDP listener, shared resolver, N stream supervisors |
| `internal/server` | `Serve`: public UDP socket, `RelayDemux` by source address, per-peer session |
| `internal/relay` | `Allocate`: pion/turn v5 client with failover + read-timeout wrapper |
| `internal/obfs` | RFC 7983 first-byte demux, RFC 5764 key derivation, SRTP framing |
| `internal/dtls` | pion/dtls v3 handshake helpers (self-signed certs, SRTP profiles, MTU 1200) |

## Data flow

```
phone WireGuard
  │  UDP
  ▼
client turn-proxy (run)  ── one local UDP listener feeds a shared queue
  │  fanned across N parallel streams (turn.streams)
  │  each stream: DTLS-SRTP payload over TCP (turn.transport) through its own VK allocation
  ▼
client's TURN allocation (VK)   one per stream, on any advertised host
  │  ChannelData (pion binds per peer) → relayed to the server's public ip:port
  ▼
server turn-proxy (serve)  ── plain public UDP listener; no TURN, no VK, no broker
  │  demuxed by source address (the VK relay IP, distinct per stream)
  │  → one DTLS+SRTP session per source, each dialing its own upstream UDP socket
  ▼
server WireGuard / upstream
```

The client runs **many parallel streams** to beat VK's per-allocation bandwidth
cap (`turn.streams`, spread by rotating `session.urls` left by the stream index).
The server has **no allocation of its own** — VK call-TURN does not filter peer
source-IP, so a client can relay straight to the server's public IP; this removes
the server egress cap and any same-host constraint. The server `RelayDemux` keys
arriving streams apart by source address (each stream is a distinct VK relay IP).
WireGuard's crypto identity + endpoint roaming sort multiple clients and spread
download across streams. The client↔VK leg defaults to **TCP** (`turn.transport`).

## Tunnel wire shape

Real-WebRTC layout (RFC 7983 demux on a shared conn, `obfs/demux.go`):

- DTLS handshake records (first byte 20–63) — visible on the wire, no pre-shared secrets
- SRTP packets (first byte 128–191) — keys derived from DTLS keying material via RFC 5764 `EXTRACTOR-dtls_srtp` exporter (`obfs/derive.go`); payloads framed as RTP (PT 111 "opus", random SSRC/seq/timestamp, `obfs/srtp.go`)

VK's TURN filters by first byte and only forwards STUN / DTLS / RTP-shaped packets. Raw bytes are dropped silently. The tunnel always runs the full DTLS+SRTP shape — there is no plain-DTLS mode, since DTLS-only traffic would be dropped past the handshake.

<a name="broker-repo"></a>
## Broker repo

The broker is its own module, `github.com/turn-proxy/turn-broker`
(<https://github.com/turn-proxy/turn-broker>) — see that repo's `CLAUDE.md` for
VK auth, the `tls-client` fingerprint, and session refresh. It serves VK creds
only; there is no peer rendezvous (the client learns the server address from its
own `upstream` config).

The proxy depends on it for **one thing**: the `proto.Session` wire type
(`github.com/turn-proxy/turn-broker/proto`, a zero-import DTO so the heavy
`tls-client` tree never reaches the Android binary), pulled as a normal remote
module dependency in `go.mod`. The client↔broker contract:

- `GET /v1/session?join_link=…` → cached `Session` (creds + rotated URLs + `generation`). Idempotent; the client polls it on a 60s timer
- `GET /healthz`

The server never talks to the broker.

## Proxy

**Server (`serve`)** — a plain public UDP listener; no TURN, no VK, no broker.

1. Binds one public `UDPConn` on `cfg.listen` (e.g. `0.0.0.0:56000`)
2. `StartRelayDemux` (`server/peers.go`): one reader goroutine demuxes by source address into per-source `PeerConn`s (capped at 128 peers; closed peers are reaped lazily). Each client stream arrives from a distinct VK relay IP, so it becomes its own `PeerConn`
3. Each accepted peer runs its own DTLS+SRTP session forwarding to a **fresh upstream UDP socket** (the WG backend). DTLS handshake timeout 15s; idle eviction after `turn.peer_idle_timeout_secs` with no traffic in either direction (`PeerConn.WriteTo` also touches the idle clock, so an active download is never evicted)

**Client (`run`)**

1. Binds one local UDP listener; a reader pushes every datagram onto a shared queue (cap 2048, drop-on-full) and tracks the WG reply address (`replyAddr`, `client/pump.go`)
2. One shared **resolver** goroutine `GET`s `/v1/session` and re-resolves the server's address from `cfg.upstream` (DNS) every 60s, publishing into a `targetStore`. This is the only broker traffic — O(1) regardless of stream count
3. Spawns `turn.streams` stream supervisors (staggered 200ms apart), each self-healing with backoff 500ms→10s (reset after a ≥5s healthy session). Per stream: read the current `(session, serverAddr)`, `relay.Allocate` on the rotated URL list (`rotateLeft(urls, idx)`, failover down the list), DTLS handshake through the relay, derive SRTP keys
4. All streams drain the shared queue (work-stealing) and write decrypted replies back to the WG listener; on SIGINT/SIGTERM the context cancels, each stream's `Allocation.Close()` runs and pion deallocates (refresh lifetime 0)

## Key constraints

- **The server must be publicly reachable**: clients relay to its `public ip:port` via VK, so VK has to be able to send UDP there. A NAT'd/localhost server can't receive relayed packets — there is no local data-path e2e through VK; the real test is server-on-VPS + client. Open the server's UDP port in the firewall
- **No same-host constraint**: the server holds no allocation, so each stream picks any advertised VK host
- **Many peers, many streams**: `RelayDemux` keys peers by source address (cap 128 concurrent). WireGuard tells clients apart by crypto identity downstream, so no per-client routing in the proxy
- **Timeouts**: server — 15s DTLS handshake, `peer_idle_timeout_secs` (default 60s) idle eviction. Client — 10s TURN allocation timeout, 60s relay read timeout (`timeoutConn`) triggers stream reconnect
- **Client TURN allocations leak on dirty exit** (~10 min TURN lifetime) unless `Allocation.Close()` runs. Graceful shutdown (SIGINT/SIGTERM) deallocates; `kill -9` leaks
- **Permission/channel refresh is automatic** inside pion/turn's client — it creates permissions and binds a channel per peer on first write, refreshing allocation/permissions/channels itself. An earlier theory that VK expires permissions in ~30s was a **misdiagnosis**: the real cause of "tunnel dies after ~30s" was the WireGuard full-tunnel routing loop (see Client routing)
- **Client↔VK leg runs over TCP by default** (`turn.transport`). UDP-transported allocations were less reliable through the real phone→VPS path; TCP keeps the relay alive and a dead TCP transport surfaces immediately, so the supervisor re-allocates. VK accepts TCP on the same `turn:host:port` it advertises (bare URLs, no `?transport=`). TCP wiring: `net.Dial` + `turn.NewSTUNConn` (`relay/newWireConn`). The relay does not filter the advertised URL list by transport — it parses each URL with `stun.ParseURI` (skipping non-`turn:` schemes) and dials the configured `turn.transport` on every server, failing over down the list

## Client routing (the WireGuard loop)

The single most important client-side deployment gotcha. When the client app
(WireGuard) is a **full tunnel** (`AllowedIPs = 0.0.0.0/0`), the moment its
tunnel comes up it captures **turn-proxy's own packets to the VK TURN relay**
and routes them back into the tunnel → the proxy can no longer reach VK → the
relay dies. Signature: the tunnel handshakes and survives exactly **one
exchange** (the WG handshake, sent before WG's tunnel is up), then goes silent.

Fix: keep the proxy's traffic to the relay **off** the VPN.
- Carve the VK TURN host range out of `AllowedIPs` (e.g. `0.0.0.0/0` minus
  `91.231.135.0/24`, which covers the relays VK hands out). Use an AllowedIPs
  calculator to expand the complement.
- Or exclude the proxy process from the VPN. The reference (cacggghp/vk-turn-proxy)
  ships an Android app and its docs require excluding that app from WG routing.

Only the client side (the full-tunnel VPN) needs this. The server has no VK
traffic at all (it's a plain public listener), so nothing to carve out there.

## Build / run

```bash
make build      # → dist/turn-proxy
make test       # go test ./...
make vet
make android    # CGO_ENABLED=0 GOOS=android GOARCH=arm64 → dist/turn-proxy-aarch64-android

./dist/turn-proxy -config turn-proxy.json serve      # server (default cmd: run)
```

Config path also via `TURN_PROXY_CONFIG`. There is **no local data-path e2e**
through VK (see Key constraints). The broker lives in its own repo,
`github.com/turn-proxy/turn-broker`.

`make build` produces the binary; `docker build -t turn-proxy .` produces a
distroless image (run with `-config /etc/turn-proxy/turn-proxy.json [serve]`).
The `docker-publish` GitHub Action pushes multi-arch images to GHCR on pushes to
`master` and on `v*` tags.

## Config

`turn-proxy.json` (same shape for `serve` and `run`; `listen` and `upstream` mean different things per mode):
```json
{
  "listen": "0.0.0.0:56000",
  "upstream": "127.0.0.1:51820",
  "broker": {
    "url": "http://broker-host:8787",
    "join_link": "https://vk.com/call/join/<token>"
  },
  "turn": {
    "transport": "tcp",
    "streams": 10,
    "peer_idle_timeout_secs": 60
  }
}
```

- `listen` — **server**: the public UDP port clients relay to (must be open in the firewall). **client**: the local UDP entry point WireGuard sends to
- `upstream` — **server**: where it forwards decrypted bytes (server-side WireGuard). **client**: the server's reachable public `host:port` (host can be DNS; re-resolved every 60s)
- `broker` is required in the config (validated at load) but only the **client** uses it; the server does no broker traffic
- `turn` (defaults shown): `transport` (`tcp`|`udp`) is the client's wire transport to the relay; `streams` is the client's parallel-stream count (1–64); `peer_idle_timeout_secs` is the server's per-peer idle eviction
- **Unknown proxy-config keys are rejected** (`DisallowUnknownFields`)

## Code conventions

- **NEVER write comments.** Do not add any comment — not even a "why" one — unless the user explicitly asks for it. This includes doc comments. Write code that explains itself through naming and structure. If a reviewer asks why, that's a conversation, not a comment.
- Commit messages: terse, single-line subject in **imperative mood**, **no trailing period**. Name the headline change; omit secondary ones. No marketing, no body unless asked. Each commit must build on its own.

## Useful files

- `internal/obfs/inbox.go` — `Inbox`: bounded drop-on-full packet mailbox, blocking read unblocked by a `done` channel; shared by client `Endpoint` and server `PeerConn`
- `internal/obfs/demux.go` — RFC 7983 first-byte demux: `Mux` (read loop) + `Endpoint` (per-protocol `net.PacketConn` over an `Inbox` + a `PacketSink` write seam)
- `internal/obfs/derive.go` — SRTP key extraction from DTLS via pion's `ExtractSessionKeysFromDTLS` (`DerivePair`)
- `internal/obfs/srtp.go` — RTP framing + SRTP encrypt/decrypt + `SRTPConn`
- `internal/relay/relay.go` — `Allocate` failover, TCP/UDP wire conn, pion/turn client setup
- `internal/server/server.go` — `Serve`: public socket, per-peer DTLS+SRTP session
- `internal/server/peers.go` — `StartRelayDemux`/`PeerConn` source-address demux; `PeerConn` is the `PacketSink` for its two `obfs.Endpoint` ports, ctx-cancelled idle eviction
- `internal/client/client.go` — `Run`, `resolveTargetLoop`, `streamSupervisor`, `runOneStream`
- `internal/server/pump.go` — `relayToPeer` (server per-session pump)
- `internal/client/pump.go` — `relayStreamToLocal` (client work-stealing pump) + `replyAddr`