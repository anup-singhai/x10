BINARY   := x10
TAGS     := sqlite_fts5
BUILD    := go build -tags "$(TAGS)"
INSTALL  := go install -tags "$(TAGS)"

.PHONY: build install clean test

build:
	$(BUILD) -o $(BINARY) ./cmd/x10

install:
	$(INSTALL) ./cmd/x10

clean:
	rm -f $(BINARY)
	rm -rf .x10/

test:
	go test -tags "$(TAGS)" ./...
