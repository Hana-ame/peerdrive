// Package docs provides Swagger/OpenAPI documentation stubs.
// Generated docs are produced by `swag init` during release builds.
package docs

import "github.com/swaggo/swag"

//go:generate swag init -g cmd/server/main.go -o docs

var SwaggerInfo = &swag.Spec{
	Version:     "0.0.0",
	Host:        "localhost:3000",
	BasePath:    "/",
	Schemes:     []string{},
	Title:       "Peerdrive API",
	Description: "Peerdrive P2P file sharing API",
}
