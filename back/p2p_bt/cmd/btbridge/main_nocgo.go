//go:build !cgo

// Command btbridge builds the Peerdrive BitTorrent runtime as a C-shared
// library. See main_cgo.go for the exported ABI and the rationale.
//
// This file exists only so the package stays buildable when cgo is disabled
// (the repo builds every submodule with CGO_ENABLED=0 in CI): without it,
// `go build ./...` in the p2p_bt module fails with "build constraints exclude
// all Go files". It produces an empty binary that exports nothing — the real
// artifact requires cgo.
package main

func main() {}
