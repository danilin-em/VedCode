BINARY  = vedcode
OUTDIR  = dist
OUT     = $(OUTDIR)/$(BINARY)

.PHONY: build build-all test vet lint fmt tidy check clean

build: check
	mkdir -p $(OUTDIR)
	rm -f $(OUT)
	go build -o $(OUT) ./cmd/vedcode/

build-all: check build-linux-amd64 build-linux-arm64 build-darwin-arm64

build-linux-amd64:
	mkdir -p $(OUTDIR)
	GOOS=linux GOARCH=amd64 go build -o $(OUTDIR)/$(BINARY)-linux-amd64 ./cmd/vedcode/

build-linux-arm64:
	mkdir -p $(OUTDIR)
	GOOS=linux GOARCH=arm64 go build -o $(OUTDIR)/$(BINARY)-linux-arm64 ./cmd/vedcode/

build-darwin-arm64:
	mkdir -p $(OUTDIR)
	GOOS=darwin GOARCH=arm64 go build -o $(OUTDIR)/$(BINARY)-darwin-arm64 ./cmd/vedcode/

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

check: vet test

clean:
	rm -rf $(OUTDIR)
