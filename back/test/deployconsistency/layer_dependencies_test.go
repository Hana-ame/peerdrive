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
		if hasMandatoryBuildTag(path) {
			// mandatory 类别的文件从不进入可部署二进制（mandatory 这个 build tag 只由
			// mandatory CI job 显式打开），所以它不在生产的依赖图里，不视为分层违规。
			// 但这类测试必须走真实装配（把 service.NodeShare 接到 transport 的
			// SetShareGate/SetShareProvider 上）才有意义——用 lambda 替身重实现一遍
			// 权限逻辑等于自证，钉不住任何东西。所以给 mandatory 留这个口子。
			continue
		}
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

// hasMandatoryBuildTag 报告文件是否带 //go:build mandatory 约束。
// 这类文件只在显式打 mandatory 标签时才会编译（mandatory CI job），
// 普通 go build / go test ./... 一律排除，因此不进入生产的依赖图。
func hasMandatoryBuildTag(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "//go:build") {
			return strings.Contains(line, "mandatory")
		}
	}
	return false
}
