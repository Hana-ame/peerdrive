# MEMO - 环境配置

## 当前环境

- **系统**: WSL (Windows Subsystem for Linux)
- **Shell**: bash

## 代理问题

**问题**: 系统配置了 Privoxy 代理，导致 `curl` 请求失败。

**现象**: 
```
<html>
<head>
 <title>500 Internal Privoxy Error</title>
 ...
</head>
```

**解决方案**: 使用 `-x ""` 绕过代理，或设置 `curl` 环境变量。

```bash
# 绕过代理
curl -x "" http://localhost:3000/ping

# 或设置环境变量
unset http_proxy
unset https_proxy
```

## API_BASE

- **默认**: `http://localhost:3000`
- **生产**: 通过 Cloudflare Tunnel `https://wsl-3000.moonchan.xyz`

## Cloudflare Tunnel

```bash
# 在宿主机运行
cloudflared tunnel --url http://localhost:3000
```

## 运行命令

```bash
# 启动服务器
go run ./cmd/server/main.go

# 测试脚本
bash test.sh
```