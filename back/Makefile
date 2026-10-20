# Peerdrive Backend Makefile
# 主模块命令自动带 -tags nosqlite，避免 go-llsqlite 和 mattn/go-sqlite3 的 C 符号冲突

GO ?= go
TAGS = nosqlite
SUBMODULES = signalframe peerjs signalserver p2p_bt

.PHONY: run build build-server test test-race test-submodules vet clean

# 默认运行单二进制 serve 子命令（等价于旧 cmd/server）
run:
	$(GO) run -tags $(TAGS) ./cmd/peerdrive/ serve

# 构建统一单二进制 peerdrive（涵盖 serve / signal / reg / all / hub / tun / mcp）
build:
	$(GO) build -tags $(TAGS) -buildvcs=false -ldflags="-s -w" -o peerdrive ./cmd/peerdrive/

# 向后兼容：构建旧独立入口 server
build-server:
	$(GO) build -tags $(TAGS) -o server ./cmd/server/

# 运行主模块与全部独立 go.mod 子模块测试
test:
	$(GO) test -tags $(TAGS) ./... -count=1
	$(MAKE) test-submodules

# 运行四个独立 go.mod 子模块的 vet 与单元测试
test-submodules:
	@for mod in $(SUBMODULES); do \
		echo "==> Testing submodule: $$mod"; \
		(cd $$mod && $(GO) vet ./... && $(GO) test -count=1 ./...) || exit 1; \
	done

# 竞态检测（主模块 + peerjs 核心并发层）
test-race:
	$(GO) test -tags $(TAGS) -race ./internal/transport/... ./internal/source/... -count=1
	cd peerjs && $(GO) test -race ./... -count=1

vet:
	$(GO) vet -tags $(TAGS) ./...
	@for mod in $(SUBMODULES); do \
		(cd $$mod && $(GO) vet ./...) || exit 1; \
	done

clean:
	rm -f peerdrive peerdrive.exe server
