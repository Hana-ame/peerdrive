// 匿名合集仓库 — 将 AnonCollection JSON 写入文件系统并注册到 files 表。
//
// StoreCollection(coll):
//   1. json.Marshal(coll) 得到 []byte
//   2. SHA256([]byte) 得到 cid
//   3. 写文件到 storage/anon/{cid[:2]}/{cid}
//   4. repository.InsertFile(&FileMetadata{
//        Hash: cid, ProviderType: "local",
//        Path: "anon/" + cid[:2] + "/" + cid,
//        Filename: cid + ".peerdrive-collection.json",
//      })
//   5. 如果 UNIQUE 冲突（文件已存在）则忽略，返回 cid
//
// storageDir 通过 SetAnonStorageDir(dir) 注入（由 controller 层在 InitFileController 后调用）
//
// 依赖：
//   - controller.InitFileController 已经创建了 storageDir
//   - files 表的 UNIQUE(hash) 约束保证幂等
package repository
