// WebRTC 信令控制器 — 初始化 SignalingHub 单例并提供访问器。
package controller

import "peerdrive/internal/service"

var signalHub *service.SignalingHub

// InitSignalHub 注入 SignalingHub 实例供 WebRTC 信令使用。
func InitSignalHub(hub *service.SignalingHub) {
	signalHub = hub
}

// GetSignalHub 返回全局 SignalingHub 单例。
func GetSignalHub() *service.SignalingHub {
	return signalHub
}
