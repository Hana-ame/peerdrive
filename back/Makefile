# Peerdrive Backend Makefile
# 主模块命令自动带 -tags nosqlite，避免 go-llsqlite 和 mattn/go-sqlite3 的 C 符号冲突

GO ?= go
TAGS = nosqlite
SUBMODULES = signalframe peerjs signalserver p2p_bt

.PHONY: run build build-server build-win build-win7 build-bt-dll build-win-bt test test-race test-submodules vet clean

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

# ---- BT 运行时：按需加载的动态库 ----------------------------------------
# torrent 栈不再静态链接进主二进制：它被编成 peerdrive_bt.dll，主程序仅在
# PEERDRIVE_BT_ENABLE=1 时 LoadLibrary 它（见 internal/btloader 与
# p2p_bt/cmd/btbridge）。默认 off 时进程里根本不存在 BT 运行时。
#
# 三个非显然的约束，改之前先读：
#   1. CXX 必须指向 mingw 的 C++ 前端。anacrolix/go-libutp 是 C++ 代码，只设
#      CC 会让 cgo 回落到宿主机 g++，报 "unrecognized command-line option
#      '-mthreads'"（这个错和源码无关，别去改 CGO_CFLAGS）。
#   2. CGO_LDFLAGS 必须带 -static。否则产物依赖 libwinpthread-1.dll /
#      libgcc_s_seh-1.dll / libstdc++-6.dll，宿主 LoadLibrary 时报
#      "The specified module could not be found"（看不出缺哪个）。
#   3. 主程序用 CGO_ENABLED=0 构建也没关系：载入 DLL 不需要 cgo。
BT_GOOS   ?= windows
BT_GOARCH ?= amd64
BT_CC     ?= x86_64-w64-mingw32-gcc
BT_CXX    ?= x86_64-w64-mingw32-g++
BT_DLL    ?= peerdrive_bt.dll

build-bt-dll:
	cd p2p_bt && CGO_ENABLED=1 GOOS=$(BT_GOOS) GOARCH=$(BT_GOARCH) CC=$(BT_CC) CXX=$(BT_CXX) CGO_LDFLAGS="-static -static-libgcc -static-libstdc++" $(GO) build -buildmode=c-shared -trimpath -buildvcs=false -ldflags="-s -w" -o ../$(BT_DLL) ./cmd/btbridge
	@ls -l $(BT_DLL)

# 发布用：exe + 同目录的 DLL（btloader 只找 exe 同级与 exe/dist，
# 不搜 CWD —— 见 resolveDLLPath 里的 DLL 劫持说明）。
build-win-bt: build-win build-bt-dll
	@echo "built peerdrive-win.exe + $(BT_DLL); ship them side by side"

# Windows 7 入口（托盘版）：用 XTLS/go-win7 补丁版 SDK 构建，产物兼容
# Win7~11（386 为主 + amd64）。官方 Go 1.21.5+ 运行时在 Win7 启动即崩
# （bcryptprimitives.dll!ProcessPrng 缺失，Exception 0xc0000005），补丁 SDK
# 打掉该依赖且 go.mod/依赖树不动。脚本自动下载 SDK 并锁 GOTOOLCHAIN=local，
# 产物落 back/dist/（详见 scripts/build-win7.sh 与 doc/MODULE-MAP.md）。
build-win7:
	bash ../scripts/build-win7.sh

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
	rm -f peerdrive peerdrive.exe server peerdrive-win.exe $(BT_DLL)
	rm -rf dist
