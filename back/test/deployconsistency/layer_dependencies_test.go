package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayerDependencies_ServiceDoesNotImportTransport 发现背景（Issue #211 / REFACTOR §8 规则 3）：
// §8 规则规定 service 层不直接 import transport，但历史重构中 node_directory.go、
// nodeshare.go 等文件曾多次发生静默越层引入 transport 的漂移。
// 本测试作为 CI 门禁，确保 service 包不直接 import transport 包（应通过 model 共享模型或在 assembly 处依赖注入）。
func TestLayerDependencies_ServiceDoesNotImportTransport(t *testing.T) {
	root := repoRoot(t)
	serviceDir := filepath.Join(root, "back", "internal", "service")

	files, err := os.ReadDir(serviceDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", serviceDir, err)
	}

	fset := token.NewFileSet()
	for _, fi := range files {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".go") {
			continue
		}
		path := filepath.Join(serviceDir, fi.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if impPath == "peerdrive/internal/transport" {
				t.Errorf("%s violates §8 rule 3: directly imports peerdrive/internal/transport", fi.Name())
			}
			if impPath == "peerdrive/internal/controller" {
				t.Errorf("%s violates layered rule: imports peerdrive/internal/controller", fi.Name())
			}
		}
	}
}

// TestLayerDependencies_TransportDoesNotImportServiceOrController 发现背景（LAYERS.md §6 Checklist）：
// transport 包为帧协议层，严禁直接依赖高层业务 service 或 controller，
// 控制面转交通过 Gin 引擎抽象，业务数据源通过 Provider/Gate 接口由 main 注入。
func TestLayerDependencies_TransportDoesNotImportServiceOrController(t *testing.T) {
	root := repoRoot(t)
	transportDir := filepath.Join(root, "back", "internal", "transport")

	files, err := os.ReadDir(transportDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", transportDir, err)
	}

	fset := token.NewFileSet()
	for _, fi := range files {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".go") {
			continue
		}
		path := filepath.Join(transportDir, fi.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if impPath == "peerdrive/internal/service" {
				t.Errorf("%s violates LAYERS.md: imports peerdrive/internal/service", fi.Name())
			}
			if impPath == "peerdrive/internal/controller" {
				t.Errorf("%s violates LAYERS.md: imports peerdrive/internal/controller", fi.Name())
			}
		}
	}
}

// TestLayerDependencies_RepositoryDoesNotImportHigherLayers 发现背景（LAYERS.md AOP 职责切面）：
// repository 是底层数据持久化层，严禁反向依赖 service、controller 或 transport。
func TestLayerDependencies_RepositoryDoesNotImportHigherLayers(t *testing.T) {
	root := repoRoot(t)
	repoDir := filepath.Join(root, "back", "internal", "repository")

	files, err := os.ReadDir(repoDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", repoDir, err)
	}

	fset := token.NewFileSet()
	for _, fi := range files {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".go") {
			continue
		}
		path := filepath.Join(repoDir, fi.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if impPath == "peerdrive/internal/service" ||
				impPath == "peerdrive/internal/controller" ||
				impPath == "peerdrive/internal/transport" {
				t.Errorf("%s violates layer rule: imports %s", fi.Name(), impPath)
			}
		}
	}
}
