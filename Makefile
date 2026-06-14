.PHONY: build test vet fmt android android-arm

build:
	go build -o dist/turn-proxy ./cmd/turn-proxy

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

android:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -trimpath -ldflags="-s -w -checklinkname=0" -o dist/turn-proxy-aarch64-android ./cmd/turn-proxy

android-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w -checklinkname=0" -o dist/turn-proxy-armv7-android ./cmd/turn-proxy
