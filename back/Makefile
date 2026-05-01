# Peerdrive Backend Makefile
# 所有命令自动带 -tags nosqlite，避免 go-llsqlite 和 mattn/go-sqlite3 的 C 符号冲突

GO ?= go
TAGS = nosqlite

.PHONY: run build test vet clean

# 启动开发服务器
run:
	$(GO) run -tags $(TAGS) ./cmd/server/main.go

# 编译二进制
build:
	$(GO) build -tags $(TAGS) -o server ./cmd/server/

# 运行测试
test:
	$(GO) test -tags $(TAGS) ./...

# 静态检查
vet:
	$(GO) vet -tags $(TAGS) ./...

# 清理编译产物
clean:
	rm -f server
