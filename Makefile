.PHONY: build test vet fmt android

build:
	go build -o dist/turn-proxy ./cmd/turn-proxy

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

android:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags=-checklinkname=0 -o dist/turn-proxy-aarch64-android ./cmd/turn-proxy
