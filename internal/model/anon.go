// Package model 定义匿名合集元数据 JSON 的数据结构。
// PeerdriveType 固定为 "anonymous_collection_v1"。
// Entries 在序列化前必须按 Path 字典序排序，以保证相同内容产出相同 CID。
// CID = SHA256(canonical JSON) = 匿名 Collection 的唯一标识符。
//
// 使用方式：
//   coll := &AnonCollection{...}
//   sort.Slice(coll.Entries, func(i, j int) bool { return coll.Entries[i].Path < coll.Entries[j].Path })
//   data, _ := json.Marshal(coll)
//   cid := sha256Hex(data)
//   // 然后写入 storage/ 并注册到 files 表
package model
