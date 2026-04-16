package main

import (
	_ "peerdrive/docs" // Import generated docs
	"peerdrive/router"
)

// @title Peerdrive API
// @version 1.0
// @description This is a sample server.
// @host localhost:8080
// @BasePath /

func main() {
	r := router.SetupRouter()
	r.Run(":8081")
}
