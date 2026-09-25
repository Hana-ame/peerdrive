// index.js — peerdrive-client 的公开入口。
//
// 两件事：把帧协议的纯函数（protocol.js）和客户端状态机（client.js）都导出。
// 用 `export *` 而不是逐个列举，是为了让"新增一个协议常量"不必改两处
// （协议实现里大多数新东西都应该对使用方可见——他们要用它做 UI 判断）。

export * from './protocol.js'
export * from './client.js'
