# Peerdrive Backend Makefile
# 主模块命令自动带 -tags nosqlite，避免 go-llsqlite 和 mattn/go-sqlite3 的 C 符号冲突

GO ?= go
TAGS = nosqlite
SUBMODULES = signalframe peerjs signalserver p2p_bt

.PHONY: run build build-server build-win build-win7 test test-race test-submodules vet clean

# 默认运行单二进制 serve 子命令（等价于旧 cmd/server）
run:
	$(GO) run -tags $(TAGS) ./cmd/peerdrive/ serve

# 构建统一单二进制 peerdrive（涵盖 serve / signal / reg / all / hub / tun / mcp）
build:
	$(GO) build -tags $(TAGS) -buildvcs=false -ldflags="-s -w" -o peerdrive ./cmd/peerdrive/

# 向后兼容：构建旧独立入口 server
build-server:
	$(GO) build -tags $(TAGS) -o server ./cmd/server/

# Windows 桌面入口（带系统托盘）：与 ./cmd/peerdrive 共用同一份 internal/cli
# 调度，差别只在 Options.Tray=true——serve/all 起来之后挂托盘
# （打开面板/网盘、检查更新、退出）。运行期可用 PEERDRIVE_TRAY=0 强制关闭。
# 默认 amd64；32 位老机器（含 Win7 x86）用 build-win7。
build-win:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -tags $(TAGS) -buildvcs=false -ldflags="-s -w" -o peerdrive-win.exe ./cmd/peerdrive-win/

# Windows 7 最小入口：只 import gin + serverapp + version（与 cmd/server 同级），
# 托盘/自动更新/tun/mcp/demo 全不进 import 图——老机器要的只是「起服务 + 面板」。
# ⚠️ 工具链：Go 1.22+ 运行时不再支持 Win7（最后支持 1.21）。当前 go.mod 为
# go 1.26.2，要在 Win7 上跑需先 `go mod edit -go=1.21` 再用 Go ≤1.21 构建
# （详见 doc/MODULE-MAP.md「平台入口分化」一节）。
build-win7:
	CGO_ENABLED=0 GOOS=windows GOARCH=386 $(GO) build -tags $(TAGS) -buildvcs=false -ldflags="-s -w" -o peerdrive-win7.exe ./cmd/peerdrive-win7/

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
	rm -f peerdrive peerdrive.exe server peerdrive-win.exe peerdrive-win7.exe
