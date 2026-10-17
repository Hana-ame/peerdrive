# Peerdrive Root Makefile — 统一前后端构建入口
# 后端所有 Go 命令自动带 -tags nosqlite，避免 SQLite C 符号冲突

.PHONY: dev dev-back dev-front build test clean

# 同时启动前后端开发服务器
dev:
	@echo "==> 启动后端 (:3000) + 前端 (:5173)"
	cd back && $(MAKE) run &
	cd front && npm run dev

# 仅启动后端
dev-back:
	cd back && $(MAKE) run

# 仅启动前端
dev-front:
	cd front && npm run dev

# 构建全部
build:
	cd back && $(MAKE) build
	cd front && npm run build

# 运行全部测试
test:
	cd back && $(MAKE) test
	cd front && npm test

# 清理编译产物
clean:
	cd back && $(MAKE) clean
	rm -rf front/dist
