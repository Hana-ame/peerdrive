// 匿名合集控制器 — 创建、读取、Fork 匿名合集。
// 匿名合集是不可变的内容寻址 JSON，CID = SHA256(规范化 JSON)。
//
// 路由：
//   POST   /anon/collections                    — 创建匿名合集
//     body: {"name":"...", "entries":[{"path":"...","hash":"...","size":123}]}
//     处理：entries 按 path 排序 → json.Marshal → SHA256 → 通过 AnonRepo 存到 storage/ + files 表
//     返回: {"cid": "sha256hex..."}
//
//   GET    /anon/collections/:cid               — 读取匿名合集
//     处理：通过 Downloader.GetFileStream(cid) 获取文件流 → 解析 JSON → 校验 peerdrive_type
//     返回: AnonCollection JSON（同创建时结构）
//
//   POST   /anon/collections/fork               — Fork 匿名合集
//     body: {"source_cid":"...", "add_entries":[...], "remove_paths":[...]}
//     处理：读取源 JSON → entries 增删 → 重新排序 → 生成新 CID → 存储
//     返回: {"cid": "new_sha256hex..."}
//
//   GET    /anon/collections/:cid/entries/*path  — 获取匿名合集内某个文件的下载地址
//     处理：解析 JSON → 按 path 找到对应 hash → 302 重定向到 /sha256sum/:hash
//     返回: 302 Redirect
//
// 注意：匿名合集一旦创建不可修改；Fork 本质是创建新合集。
// entries 的 path 在 URL 中以 *path 参数传递，需要 strings.TrimPrefix(c.Param("path"), "/")
package controller
