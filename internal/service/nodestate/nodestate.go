// Package nodestate 存储节点运行时身份状态。operator 通过 POST /p2p/node/register 设置。
package nodestate

import "sync"

var (
	mu       sync.Mutex
	operator string // 空 = 匿名
)

// SetOperator 设置节点运营者。
func SetOperator(username string) {
	mu.Lock()
	defer mu.Unlock()
	operator = username
}

// GetOperator 返回节点运营者，空字符串表示匿名。
func GetOperator() string {
	mu.Lock()
	defer mu.Unlock()
	return operator
}
