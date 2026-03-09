BINARY  = vedcode
OUTDIR  = dist
OUT     = $(OUTDIR)/$(BINARY)

.PHONY: build test vet lint fmt tidy check clean

build: check
	mkdir -p $(OUTDIR)
	rm $(OUT)
	go build -o $(OUT) ./cmd/vedcode/

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
