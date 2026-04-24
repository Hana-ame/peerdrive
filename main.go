package main

import (
	_ "peerdrive/docs" // Import generated docs
	"peerdrive/router"
)

func main() {
	r := router.SetupRouter()
	r.Run(":8081")
}
