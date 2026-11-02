# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## &#x20;hash to files

## interface

### File

| Field | Type |
| --- | --- |
| `SHA256` | string |
| `Filename` | string |
| `FullPath` | string |
| `MimeType` | string |
| `Size` | int64 (bytes) |
| `File` | *os.File (from `os.Open`) |
