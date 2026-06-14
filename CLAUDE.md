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
- SRTP packets (first byte 128–191) — keys derived from DTLS keying material via RFC 5764 `EXTRACTOR-dtls_srtp` exporter (`obfs/derive.go`); payloads framed as RTP (random SSRC/seq/timestamp, `obfs/srtp.go`). Two `obfs.FrameKind`s share the SRTP stream, told apart by RTP payload type: `FrameData` (PT 111 "opus", the tunnelled WG bytes) and `FrameHeartbeat` (PT 110, a 1-byte liveness probe). Both look like RTP media on the wire; the receiver demuxes on PT, never handing a heartbeat to WG (see Stream liveness)

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
- **Timeouts**: server — 15s DTLS handshake, `peer_idle_timeout_secs` (default 60s) idle eviction. Client — 10s TURN allocation timeout. There is **no relay read-timeout** (an earlier `relay.timeoutConn`/`clientReadTimeout` was removed entirely): a blanket 60s read deadline tore down **idle-but-healthy** streams and re-allocated them, and since work-stealing concentrates WG traffic on 1–2 streams the other ~6 sat idle and churned a fresh VK allocation every 60s → after ~2 generations the per-credential quota (486) was exhausted. Liveness is instead done by an **active heartbeat** that only reconnects genuinely dead streams (see Stream liveness), so idle streams never churn
- **Client TURN allocations leak on dirty exit** (~10 min TURN lifetime) unless `Allocation.Close()` runs. Deallocation is the synchronous `REFRESH lifetime=0` that `rawConn.Close()` (pion `UDPConn.Close`) writes before the wire closes — `client.Close()` alone does **not** deallocate (it only drops transactions). Graceful shutdown (SIGINT/SIGTERM) cancels ctx → each stream's `defer allocated.Close()` runs and frees the relay; `kill -9`/SIGKILL leaks. **This is the usual cause of `486` on a quick restart**: the Android service used to stop the binary with `Process.destroy()`/`destroyForcibly()`, which on Android both send **SIGKILL**, so all `turn.streams` allocations leaked for ~10 min and the next start hit the per-credential quota. The app now sends SIGTERM first (grace, then SIGKILL fallback) so the binary deallocates. `relay.allocateWithTimeout` also drains+closes a conn that arrives after its 10s timeout (otherwise that late allocation leaks)
- **VK rejects permission/channel *refresh* (400), so an allocation goes silently dead at ~5 min.** pion creates a permission and binds a channel per peer on first write, then refreshes them on timers (permission 120s, channel-bind 5 min). VK answers every refresh with `400 Bad Request` (`Fail to refresh permissions` / `Failed to bind channel`). The *initial* permission/bind works; only the refresh fails, so VK lets the relay run until its permission lifetime (~5 min) lapses and then **silently drops relayed packets** — the client↔VK TCP leg stays up, `WriteTo` keeps succeeding, `ReadFrom` just goes quiet. (This **disproves** the old "permissions don't expire / the ~30s death was only the WG routing loop" note: the routing loop is a *separate* client-side bug; permission expiry is real and server-path-side.) We handle it two ways, both in `relay.go`/`client`: (1) **suppress the doomed permission refresh** by setting `ClientConfig.PermissionRefreshInterval = permRefreshInterval` (30 min, far past a stream's life) so pion never sends the 120s permission-refresh that would 400 — the 5-min channel-bind interval is unexported and unreachable, so it's instead dodged by reconnecting before it fires; (2) **active liveness + proactive reconnect** (see Stream liveness). The reference's plain UDP mode (`oneTurnConnection`) has the *same* silent-stall hole — only its KCP/VLESS mode (`maintainVLESSSession`) self-heals, via KCP keepalive → stall-detect → reconnect; our heartbeat is that mechanism without KCP
- **Stream liveness (client): heartbeat + stall-detect + proactive reconnect** (`obfs/srtp.go`, `client/stream_pool.go`). Each stream's `relayStreamToLocal` runs a watcher goroutine that every `heartbeatInterval` (5s) sends a `FrameHeartbeat` SRTP packet (PT 110, 1-byte payload) and checks `tunnel.LastInbound()`; if nothing (data *or* heartbeat echo) has arrived for `stallTimeout` (15s) it closes the tunnel → the supervisor re-allocates. The **server echoes** heartbeats: `NewSRTPConn(..., echo=true)` makes `SRTPConn.Read` bounce a `FrameHeartbeat` straight back instead of forwarding it to WG; the client constructs with `echo=false`. The echo rides the **same allocation** (point-to-point per VK relay IP), so it returns on the sending stream regardless of how work-stealing spreads WG payload. `SRTPConn` tracks `lastInbound` (atomic, set on every decrypted frame) and serializes all sends through a mutex (data writes and heartbeat echoes race otherwise). On top of the reactive heartbeat, each stream has a **proactive lifetime** `streamLifetime` (3 min + up to 60s jitter, always < VK's 5-min permission expiry and < the 5-min channel-bind refresh) after which it reconnects unconditionally — this is what keeps the channel-bind 400 from ever being sent and pre-empts the predictable 5-min stall; the heartbeat catches anything faster. Closing the old allocation deallocates it (graceful `Close`), so concurrent allocations stay ≈`turn.streams` and don't pile into the 486 quota
- **Custom `transport.Net` (`relay/directnet.go`, `directNet{}`)** passed to pion/turn's `ClientConfig.Net`. The default (`stdnet.NewNet()`) enumerates interfaces via `wlynxg/anet`, which on Android without cgo can't detect the API level and falls back to netlink — denied on Android 11+ (`netlinkrib: permission denied`). `directNet` delegates dial/listen/resolve to std `net` and returns `ErrNotSupported` from `Interfaces()` (pion's client path never needs the list), so no netlink on any platform, no cgo, no `anet` at runtime. This is how the reference vk-turn-proxy core does it
- **Force IPv4 allocations** (`RequestedAddressFamily: turn.RequestedAddressFamilyIPv4` in `relay.go`). VK's TURN does not implement RFC 6156 and rejects any `REQUESTED-ADDRESS-FAMILY` attribute with `error 440 Unsupported address family`. pion/turn v5.0.10 infers the family from the wire conn's local addr and **sends the attribute whenever it infers IPv6** (e.g. an unspecified/`nil` local IP → `To4()==nil`); setting IPv4 explicitly makes pion omit the attribute (helper only appends it for IPv6), so VK allocates a plain IPv4 relay
- **VK quota is per-credential** (`error 486 Allocation Quota Reached`). The broker hands one credential set per session, so too many concurrent streams on it hit the cap. The supervisor classifies this via `relay.IsQuotaReached` (`errors.As` on `*stun.TurnError`, code 486) and on a quota error backs off `quotaBackoff` (30s) instead of the fast exponential retry — so streams that can't get a slot slow-poll rather than retry-storm, and the client **self-tunes to `min(streams, quota)` active allocations** without hammering VK. All reconnect waits get ±50% `jitter` so the N streams don't synchronize. A quota error logs a speaking `Warn` ("turn allocation quota reached, slowing reconnects; lower turn.streams if this persists") — bounded to once per `quotaBackoff` per stream, not a storm. Still lower `turn.streams` if it persists; spreading across credentials would need broker-side multi-cred
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
  },
  "dns": { "servers": ["8.8.8.8", "1.1.1.1:53"] }
}
```

- `listen` — **server**: the public UDP port clients relay to (must be open in the firewall). **client**: the local UDP entry point WireGuard sends to
- `upstream` — **server**: where it forwards decrypted bytes (server-side WireGuard). **client**: the server's reachable public `host:port` (host can be DNS; re-resolved every 60s)
- `broker` is required in the config (validated at load) but only the **client** uses it; the server does no broker traffic
- `turn` (defaults shown): `transport` (`tcp`|`udp`) is the client's wire transport to the relay; `streams` is the client's parallel-stream count (1–64); `peer_idle_timeout_secs` is the server's per-peer idle eviction
- `dns.servers` (optional, **client-only**) sets the DNS servers the client resolves names through. When non-empty, `newResolver` (`internal/client/dns.go`) builds a **scoped** `*net.Resolver` (`PreferGo: true`, dialing the listed servers round-robin; bare host gets `:53`) that is threaded explicitly to the only two name-resolution sites — the broker `*http.Client` (`newBrokerClient`, via `net.Dialer{Resolver}`) and the upstream address (`resolveUDPAddr` → `LookupNetIP`). It does **not** touch the global `net.DefaultResolver` — this mirrors sing-box, which never assigns `DefaultResolver` and instead queries explicit servers over its own dialer (verified by reading `sing-box/dns/transport/local`). Empty/absent → `net.DefaultResolver` (the OS resolver, correct on a normal server). VK relay URLs are literal IPs so pion needs no DNS; the `serve` side is unaffected. **Needed on Android** — there is no `/etc/resolv.conf`, so a pure-Go lookup otherwise hits `localhost:53` and fails. **No hardcoded public fallback** — the platform supplies the servers (the Android app injects the active network's `LinkProperties.dnsServers`, mirroring sing-box-for-android)
- **Unknown proxy-config keys are rejected** (`DisallowUnknownFields`)

## Code conventions

- **NEVER write comments.** Do not add any comment — not even a "why" one — unless the user explicitly asks for it. This includes doc comments. Write code that explains itself through naming and structure. If a reviewer asks why, that's a conversation, not a comment.
- Commit messages: terse, single-line subject in **imperative mood**, **no trailing period**. Name the headline change; omit secondary ones. No marketing, no body unless asked. Each commit must build on its own.

## Useful files

- `internal/obfs/inbox.go` — `Inbox`: bounded drop-on-full packet mailbox, blocking read unblocked by a `done` channel; shared by client `Endpoint` and server `PeerConn`
- `internal/obfs/demux.go` — RFC 7983 first-byte demux: `Mux` (read loop) + `Endpoint` (per-protocol `net.PacketConn` over an `Inbox` + a `PacketSink` write seam)
- `internal/obfs/derive.go` — SRTP key extraction from DTLS via pion's `ExtractSessionKeysFromDTLS` (`DerivePair`)
- `internal/obfs/srtp.go` — RTP framing + SRTP encrypt/decrypt + `SRTPConn`. `FrameKind` enum is the literal RTP payload type (`FrameData`=111, `FrameHeartbeat`=110); `Frame`/`Parse` carry it. `SRTPConn` adds `WriteHeartbeat`, `LastInbound` (atomic), a send `mu`, and an `echo` flag (server echoes heartbeats; client drops them) — see Stream liveness
- `internal/relay/relay.go` — `Allocate` failover, TCP/UDP wire conn, pion/turn client setup
- `internal/server/server.go` — `Serve`: public socket, per-peer DTLS+SRTP session
- `internal/server/peers.go` — `StartRelayDemux`/`PeerConn` source-address demux; `PeerConn` is the `PacketSink` for its two `obfs.Endpoint` ports, ctx-cancelled idle eviction
- `internal/client/client.go` — `Run` (binds the local socket, builds the `targetResolver` + `streamPool`, spawns supervisors) + stateless helpers (`buildTunnel`, `srtpTunnel`, `growBackoff`, `jitter`, `rotateLeft`)
- `internal/client/target_resolver.go` — `targetResolver`: owns the DNS resolver + broker client, re-resolves upstream/session on the 60s timer (`run`/`resolve`), and publishes the current `streamTarget` into an atomic store that streams read via `wait(ctx)`. `newTargetResolver(resolver, cfg.Broker, upstream)` takes the shared `*net.Resolver` (built in `Run`) and the `config.BrokerSettings`, and builds its broker client internally
- `internal/client/stream_pool.go` — `streamPool`: the shared per-run stream state (local conn, queue, `targetResolver` handle, transport). `newStreamPool(local, targets, transport)` — no `config.Config`. Per-stream methods `supervise`/`runStream` take just `(ctx, idx)`; also `readLocal` and the `relayStreamToLocal` work-stealing pump, whose watcher goroutine drives the heartbeat send + stall-detect + `streamLifetimeDeadline()` proactive reconnect (see Stream liveness; consts in `client.go`)
- `internal/obfs/endpoint.go` / `inbox.go` — `Endpoint` (per-protocol `net.PacketConn` over an `Inbox`). **`SetReadDeadline` must interrupt an already-blocked `ReadFrom`** — pion/dtls aborts a blocked read by calling `SetReadDeadline(past)` (via `netctx.ReadFromContext`) both to time out the handshake and to drive ClientHello **retransmission**. Backed by pion's `transport/v4/deadline.Deadline` (`readDL`): `ReadFrom` selects on `readDL.Done()`, `SetReadDeadline` calls `readDL.Set(t)` which closes the in-flight done channel. An earlier impl stored the deadline in an `atomic.Pointer[time.Time]` captured at `Pull` entry, so a later `SetReadDeadline` could not unblock the read — DTLS handshakes then couldn't retransmit and failed only at the relay's 60s read timeout instead of recovering (the cause of "dtls handshake: context deadline exceeded" on a lossy relay leg)
- `internal/server/pump.go` — `relayToPeer` (server per-session pump)