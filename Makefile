# Peerdrive Root Makefile — 统一前后端、核心包与架构门禁构建入口
# 后端所有主模块 Go 命令自动带 -tags nosqlite，避免 SQLite C 符号冲突

.PHONY: dev dev-back dev-front build test test-packages lint check clean

# 同时启动前后端开发服务器（带信号捕获，Ctrl+C 时一并清理后台 Go 进程，避免残留占用 :3000）
dev:
	@echo "==> 启动后端 (:3000) + 前端 (:5173)"
	@bash -c 'trap "kill 0" INT TERM EXIT; (cd back && $(MAKE) run) & (cd front && npm run dev); wait'

# 仅启动后端
dev-back:
	cd back && $(MAKE) run

# 仅启动前端
dev-front:
	cd front && npm run dev

# 构建全部（后端单二进制 + 公共面板校验 + 前端站点）
build:
	cd packages/peerdrive-client && npm run build:panel
	cd back && $(MAKE) build
	cd front && npm run build

# 运行 JS 核心与消费端包测试
test-packages:
	cd packages/peerdrive-core && npm test
	cd packages/peerdrive-client && npm test && npm run check:panel

# 代码静态检查与架构契约体检
lint:
	node scripts/check-doc-refs.mjs
	bash scripts/check-layer-deps.sh
	cd back && $(MAKE) vet
	cd front && npm run lint

# 运行全部测试（核心包 + 后端主模块与子模块 + 前端 Node 契约与单测）
test: test-packages
	cd back && $(MAKE) test
	cd front && npm run test:node && npm test

# 一键全量工程门禁（对齐 CI）
check: lint test build

# 清理编译产物
clean:
	cd back && $(MAKE) clean
	rm -rf front/dist
