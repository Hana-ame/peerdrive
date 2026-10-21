package peerjs

// xor.go: DataChannel 数据面的 XOR 混淆（轻量保护，不是强加密）。
//
// 设计契约：
//   - 作用范围：连接级**全帧**（text + binary）——DataChannel 上传输的一切
//     （req/meta/data/done/err/psk-auth/fwd/admin 等业务帧）在发送前
//     （Send/SendText/SendFrame）与回调前（attach 的 OnMessage）做对称 XOR。
//     信令面（signalframe OFFER/ANSWER/CANDIDATE）不碰——XOR 只保护数据面。
//   - 密钥：每连接派生 key = SHA-256(secret || ":" || connID)。connID 是
//     connectionId（信令路由键）：offerer 生成、经 OFFER 传给 answerer 并复用，
//     两端在 newConnection 时都能拿到同一 connID → 派生同一把 key，**无需额外
//     握手轮次**。secret 来自本地配置（Options.XORKey），不走信令交换。
//   - 开关：Options.XOREnable + Options.XORKey，**默认关**（XOREnable=false）=
//     恒等变换，线缆字节与现状完全一致（go↔浏览器 peerjs 互操作不受影响）。
//     开启要求两端同版本同 secret；错 key 解出垃圾 → 文本帧 JSON 解析失败被
//     丢弃 → 取文件确定性失败（不会静默损坏）。
//   - 流式边界：**帧级独立加密**——每帧从头按 key[i%len(key)] 异或，无跨帧
//     状态。serveFile 的 64KB 分块各自独立 → 块边界天然对齐；丢块/乱序不
//     影响其它块。代价是"同明文同 key 产出同密文"（模式可被统计），这正是
//     XOR 混淆的定位边界：防明文嗅探/偶然窥视，不防定向破解。
//   - 正确性：XOR 自逆（两次应用还原），编解码同一函数；加密在传输层，
//     业务层（transport）的哈希/长度校验在解密后计算，取文件链路不可见加密。

import "crypto/sha256"

// deriveXORKey 派生每连接 XOR 密钥（32 字节）。
// 为什么用 connID 参与派生：不同连接不同密钥流，同一文件在两条连接上不产生
// 相同密文；即使某条连接的密钥泄露（如被对端记录），其它连接不受影响——
// 前提是 secret 本身不泄露。connID 可见于信令面，但缺 secret 无法反推密钥。
func deriveXORKey(secret, connID string) []byte {
	h := sha256.Sum256([]byte(secret + ":" + connID))
	return h[:]
}

// xorApply 对 data 做对称 XOR（自逆：两次应用还原为原文）。key 为空或
// data 为空时恒等（关闭态 / 空帧不分配）。
//
// 为什么新分配而不是就地改写：调用方（serveFile）的 body 来自池化复用
// （getChunk/putChunk），SendFrame 的契约是"同步复制后归还"——加密必须复制，
// 不能改写调用方 buffer（否则并发请求共享池块会互相污染）。
func xorApply(data, key []byte) []byte {
	if len(key) == 0 || len(data) == 0 {
		return data
	}
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ key[i%len(key)]
	}
	return out
}
