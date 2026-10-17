// Package version 提供 peerdrive 单二进制的版本号。
//
// 为什么单独一个包：v0.2.0 起三个服务合并成一个二进制，`peerdrive version`
// 是唯一的版本出口；版本号散落在各处会迟早不一致（release.yml 的 tag、
// README、面板页脚都需要同一个值）。
//
// 覆盖方式：release 时用 -ldflags "-X peerdrive/internal/version.Version=vX.Y.Z"
// 注入。默认值取自 go.mod module 路径所在仓的 tag 约定，手工构建时显示为
// "(dev)" 以免误导——没注入 ldflags 的二进制不是发布产物。
package version

// Version 由构建时 -ldflags -X 覆盖；手工构建时保持下面的值。
var Version = "(dev)"

// String 返回带前缀的版本串，便于直接打印。
func String() string { return "peerdrive " + Version }
