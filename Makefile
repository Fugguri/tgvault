BINARY := tgvault

.PHONY: build run test vet tidy cross clean

build:
	go build -o $(BINARY) ./cmd/tgvault

vet:
	go vet ./...

tidy:
	go mod tidy

test:
	go test ./...

cross:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o dist/$(BINARY)_linux_amd64  ./cmd/tgvault
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o dist/$(BINARY)_linux_arm64  ./cmd/tgvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/$(BINARY)_darwin_amd64 ./cmd/tgvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/$(BINARY)_darwin_arm64 ./cmd/tgvault
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/$(BINARY)_windows_amd64.exe ./cmd/tgvault

clean:
	rm -rf dist $(BINARY)
