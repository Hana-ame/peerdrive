package egress

// range.go: RFC 7233 HTTP Range 规范解析与请求构建。
// 统一收敛所有消费端 Range 语法（封闭、开放、后缀区间），消除各控制器分散手写解析逻辑。

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ParseRangeHeader 按照 RFC 7233 解析 HTTP Range 请求头。
//
// 规范语法：
//  1. 封闭区间：bytes=100-200 -> offset=100, size=101
//  2. 开放区间：bytes=100-    -> offset=100, size=total-100
//  3. 后缀区间：bytes=-500    -> offset=total-500, size=500（倒数 500 字节）
//
// 返回值：
//  - offset: 起始偏移（从 0 开始）
//  - size: 读取长度
//  - isSatisfiable: 范围是否可满足（若 start >= total 或 end < start 则为 false）
//  - ok: 头部语法是否合法
func ParseRangeHeader(rangeVal string, totalSize int64) (offset, size int64, isSatisfiable, ok bool) {
	if totalSize <= 0 && strings.HasPrefix(rangeVal, "bytes=") {
		// 0 字节文件：任何 Range 均无法满足（RFC 7233 §4.4）
		return 0, 0, false, true
	}
	if !strings.HasPrefix(rangeVal, "bytes=") {
		return 0, 0, false, false
	}
	raw := strings.TrimPrefix(rangeVal, "bytes=")
	// 若包含多段范围（逗号分隔），遵循单一流下发原则取首个范围
	if idx := strings.IndexByte(raw, ','); idx != -1 {
		raw = raw[:idx]
	}
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	// 1. 后缀区间："bytes=-500"（读取最后 500 字节）
	if startStr == "" {
		if endStr == "" {
			return 0, 0, false, false
		}
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false, false
		}
		if totalSize <= 0 {
			return 0, 0, false, true
		}
		if suffix > totalSize {
			suffix = totalSize
		}
		return totalSize - suffix, suffix, true, true
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, false
	}

	// 起始点超过文件总长度：不可满足（应返回 416）
	if totalSize > 0 && start >= totalSize {
		return start, 0, false, true
	}

	// 2. 开放区间："bytes=100-"（从 100 开始至文件末尾）
	if endStr == "" {
		if totalSize >= 0 {
			return start, totalSize - start, true, true
		}
		return start, -1, true, true
	}

	// 3. 封闭区间："bytes=100-200"
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, false, false
	}
	if totalSize >= 0 && end >= totalSize {
		end = totalSize - 1
	}
	return start, end - start + 1, true, true
}

// NewRequestFromHTTP 根据 HTTP 请求自动解析 Range 头与上下文构造标准 EgressRequest。
// 返回的 satisfiable 标识 Range 是否在合法区间内（若为 false，调用方应返回 416）。
func NewRequestFromHTTP(r *http.Request, hash string, totalSize int64) (req EgressRequest, satisfiable bool) {
	req = EgressRequest{
		Hash:     hash,
		Offset:   0,
		Size:     -1,
		ClientID: r.RemoteAddr,
		Context:  r.Context(),
	}

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		return req, true
	}

	offset, size, isSatisfiable, ok := ParseRangeHeader(rangeHeader, totalSize)
	if !ok {
		// 非 bytes= 语法，根据 RFC 7233 忽略非法 Range 头并回退全量
		return req, true
	}
	if !isSatisfiable {
		// 范围不可满足，标记 IsRange 供 416 处理
		req.IsRange = true
		req.Offset = offset
		req.Size = 0
		return req, false
	}

	req.IsRange = true
	req.Offset = offset
	req.Size = size
	return req, true
}

// FormatContentRange 格式化 RFC 7233 Content-Range 响应头。
func FormatContentRange(offset, size, total int64) string {
	if total <= 0 || size <= 0 {
		return fmt.Sprintf("bytes */%d", total)
	}
	return fmt.Sprintf("bytes %d-%d/%d", offset, offset+size-1, total)
}
