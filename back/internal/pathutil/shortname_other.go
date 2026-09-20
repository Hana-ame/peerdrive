//go:build !windows

package pathutil

// 非 Windows 上不存在 8.3 短名，这些函数退化成恒等/空结果。
//
// 为什么不是"只在 Windows 侧引用它们"：normalize / pickRoot 在两个平台上都要
// 编译，调用点写出来了就得有定义。留空实现好过用 build tag 把 normalize 拆成
// 两份——那才是真正的维护灾难（改一处忘另一处）。

// ExpandShortNames 非 Windows 上无短名可还原：原样返回。
func ExpandShortNames(p string) string { return p }

// ShortNameOf 非 Windows 上没有短名。
func ShortNameOf(string) string { return "" }
