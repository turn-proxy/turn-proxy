module github.com/turn-proxy/turn-proxy

go 1.26.4

require (
	github.com/pion/dtls/v3 v3.1.4
	github.com/pion/logging v0.2.4
	github.com/pion/rtp v1.10.2
	github.com/pion/srtp/v3 v3.0.11
	github.com/pion/turn/v5 v5.0.10
)

require github.com/turn-proxy/turn-broker v0.0.0-20260612223001-10ab9e0b1bbb

require (
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/stun/v3 v3.1.5
	github.com/pion/transport/v4 v4.0.2
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
