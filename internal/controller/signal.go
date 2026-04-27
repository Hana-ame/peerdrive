package controller

import "peerdrive/internal/service"

var signalHub *service.SignalingHub

func InitSignalHub(hub *service.SignalingHub) {
	signalHub = hub
}

func GetSignalHub() *service.SignalingHub {
	return signalHub
}
