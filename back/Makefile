# Peerdrive Backend Makefile
# 所有命令自动带 -tags nosqlite，避免 go-llsqlite 和 mattn/go-sqlite3 的 C 符号冲突

GO ?= go
TAGS = nosqlite

.PHONY: run build test vet clean

run:
	$(GO) run -tags $(TAGS) ./cmd/server/main.go

build:
	$(GO) build -tags $(TAGS) -o server ./cmd/server/

test:
	$(GO) test -tags $(TAGS) ./...

vet:
	$(GO) vet -tags $(TAGS) ./...

clean:
	rm -f server
