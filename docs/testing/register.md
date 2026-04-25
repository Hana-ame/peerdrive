# 注册与下载功能测试说明

本文档描述了针对本地文件注册 (`RegisterLocal`) 和基于哈希下载 (`DownloadBySHA256`) 的集成测试用例。

## 测试目的
验证系统能够正确注册本地文件系统中的绝对路径文件，并且能够通过计算出的 SHA256 哈希值正确地将其下载回。

## 测试场景

### 场景 1: 单文件注册与下载
1. **准备**: 在 `/tmp/peerdrive_test/` 创建一个名为 `single.txt` 的测试文件。
2. **操作**: 
   - 调用 `POST /files/register_local` 接口，传入该文件的绝对路径。
   - 从响应中提取文件的 `hash`。
   - 调用 `GET /sha256sum/{hash}` 接口下载该文件。
3. **预期**: 下载的文件内容与原文件完全一致。

### 场景 2: 文件夹批量注册与下载
1. **准备**: 在 `/tmp/peerdrive_test/folder/` 创建多个测试文件。
2. **操作**: 
   - 调用 `POST /files/register_folder` 接口，传入文件夹的绝对路径。
   - 遍历响应中的所有文件哈希值。
   - 对每个哈希值调用 `GET /sha256sum/{hash}` 接口下载。
3. **预期**: 每个下载的文件都能在原文件夹中找到对应的匹配项。

## 如何运行测试
1. 启动 Peerdrive 服务 (`go run main.go`)。
2. 运行测试脚本：
   ```bash
   chmod +x test_register_download.sh
   ./test_register_download.sh
   ```

## 关键验证点
- **路径适配**: 验证 `LocalProvider` 能正确处理绝对路径（不与 `storageDir` 拼接）。
- **数据一致性**: 验证注册 $\rightarrow$ 存储 $\rightarrow$ 下载 的全链路数据一致性。
