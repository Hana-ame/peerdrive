package model

// share_level.go：共享级别（doc/NETDISK.md §12.6）。
//
// 一条共享声明（目录 / 单个文件 / 合集）除了「要不要共享」，还要回答「给谁」：
//
//	· public   —— 出现在共享清单里，连得上（过得了 PSK）的人都能下载
//	· unlisted —— 不出现在清单里，但知道 hash 的人可以下载（"链接分享"）
//	· private  —— 不出现在清单里，只有自己和好友能下载
//
// 一句话记法：**public = 列出来也给；unlisted = 不列出来但给；private = 只给认识的人**。
//
// 空值等价于 public：历史落盘的共享范围只有 id 没有级别（本次升级前写的
// share_scope.json），按 public 处理才不会让一次升级把运营者已经共享出去的东西
// 悄悄收回来——那种"我没动过，别人却说取不到了"最难自查。

import "strings"

const (
	LevelPublic   = "public"
	LevelUnlisted = "unlisted"
	LevelPrivate  = "private"
)

// IsValidLevel 校验级别取值（空串视为未设置，合法，等价于 public）。
func IsValidLevel(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", LevelPublic, LevelUnlisted, LevelPrivate:
		return true
	}
	return false
}

// NormalizeLevel 归一化：空 → public；非法值 → 空串（调用方据此报 400）。
//
// 为什么不把非法值也兜成 public：级别写错（比如拼成 "pubilc"）如果悄悄按
// public 生效，等于把本想限制的内容公开出去——宁可整批拒绝让运营者重填。
func NormalizeLevel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return LevelPublic
	}
	if !IsValidLevel(v) {
		return ""
	}
	return v
}

// LevelRank 宽松度排序（public 最宽松）。
//
// 空串/非法值为 **0**，不要理解成 public：LoosestLevel 要靠 0 表达"这条来源
// 没有意见"，否则"目录 unlisted + 文件没级别"会被算成 public——把本该不列出
// 的内容列了出去。
//
// 同一内容被多条来源命中时（目录 + 单文件勾选）取**最宽松**的那条：目录设成
// unlisted、里面某个文件单独设成 public，那这个文件就该是 public。取最严会让
// "我特意放宽了这一个"静默失效——用户在界面上看到的是"我改了，但没变化"。
func LevelRank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case LevelPublic:
		return 3
	case LevelUnlisted:
		return 2
	case LevelPrivate:
		return 1
	}
	return 0
}

// LoosestLevel 取两条级别里更宽松的一条。
//
// 空串 = "这条来源没意见"，所以两条都空时返回空（而不是 public）——调用方靠
// 空串判断"这条内容根本没被共享"。想拿"默认值"请用 NormalizeLevel。
func LoosestLevel(a, b string) string {
	ra, rb := LevelRank(a), LevelRank(b)
	if ra == 0 && rb == 0 {
		return ""
	}
	if ra >= rb {
		return NormalizeLevel(a)
	}
	return NormalizeLevel(b)
}
