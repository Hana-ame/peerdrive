package egress

// range_test.go: RFC 7233 Range 解析、请求构建与 HTTP 媒体播放器交互严苛测试。
// 覆盖封闭/开放/后缀全语法矩阵、Safari/Chrome <video> 探测与 206/416 协议断言。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParseRangeHeader_RFC7233_Matrix 发现背景：验证 RFC 7233 Range 语法矩阵，确保标准封闭、开放与倒数后缀区间解析准确无误。
func TestParseRangeHeader_RFC7233_Matrix(t *testing.T) {
	total := int64(1000)

	tests := []struct {
		name          string
		header        string
		wantOffset    int64
		wantSize      int64
		wantSatisfy   bool
		wantOk        bool
	}{
		{
			name:        "封闭区间 0-499",
			header:      "bytes=0-499",
			wantOffset:  0,
			wantSize:    500,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "中间封闭区间 100-299",
			header:      "bytes=100-299",
			wantOffset:  100,
			wantSize:    200,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "开放区间 500- 至文件末尾",
			header:      "bytes=500-",
			wantOffset:  500,
			wantSize:    500,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "开放区间 0- 全文件 Range",
			header:      "bytes=0-",
			wantOffset:  0,
			wantSize:    1000,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "后缀区间 -200（倒数 200 字节）",
			header:      "bytes=-200",
			wantOffset:  800,
			wantSize:    200,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "后缀区间超长 -2000（钳制至全文件）",
			header:      "bytes=-2000",
			wantOffset:  0,
			wantSize:    1000,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "结束位置超长 500-2000（钳制至文件末尾）",
			header:      "bytes=500-2000",
			wantOffset:  500,
			wantSize:    500,
			wantSatisfy: true,
			wantOk:      true,
		},
		{
			name:        "多段 Range 取首个范围 bytes=0-10,20-30",
			header:      "bytes=0-10,20-30",
			wantOffset:  0,
			wantSize:    11,
			wantSatisfy: true,
			wantOk:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			off, sz, satisfy, ok := ParseRangeHeader(tc.header, total)
			if ok != tc.wantOk {
				t.Fatalf("ok mismatch: got %v, want %v", ok, tc.wantOk)
			}
			if satisfy != tc.wantSatisfy {
				t.Fatalf("satisfy mismatch: got %v, want %v", satisfy, tc.wantSatisfy)
			}
			if off != tc.wantOffset {
				t.Errorf("offset mismatch: got %d, want %d", off, tc.wantOffset)
			}
			if sz != tc.wantSize {
				t.Errorf("size mismatch: got %d, want %d", sz, tc.wantSize)
			}
		})
	}
}

// TestParseRangeHeader_BoundaryAndErrors 发现背景：验证边界与非法输入防御（越界 416、0 字节文件、倒序范围、非数字错误）。
func TestParseRangeHeader_BoundaryAndErrors(t *testing.T) {
	total := int64(500)

	// 越界：起始位置 >= total
	off, sz, satisfy, ok := ParseRangeHeader("bytes=500-600", total)
	if !ok || satisfy {
		t.Errorf("expected ok=true, satisfy=false for start >= total, got ok=%v, satisfy=%v (off=%d, sz=%d)", ok, satisfy, off, sz)
	}

	// 倒序：end < start
	_, _, _, ok = ParseRangeHeader("bytes=400-300", total)
	if ok {
		t.Errorf("expected ok=false for inverted range")
	}

	// 非法前缀
	_, _, _, ok = ParseRangeHeader("characters=0-100", total)
	if ok {
		t.Errorf("expected ok=false for non-bytes unit")
	}

	// 非数字字符
	_, _, _, ok = ParseRangeHeader("bytes=abc-def", total)
	if ok {
		t.Errorf("expected ok=false for non-numeric range")
	}

	// 0 字节文件：任何 Range 均不可满足
	_, _, satisfy, ok = ParseRangeHeader("bytes=0-10", 0)
	if !ok || satisfy {
		t.Errorf("expected ok=true, satisfy=false for 0-byte file")
	}
}

// TestNewRequestFromHTTP_RangeScenarios 发现背景：验证 HTTP 请求上下文到 EgressRequest 模型的转换与 Range 标志位准确性。
func TestNewRequestFromHTTP_RangeScenarios(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	total := int64(1024)

	// 1. 普通 GET 无 Range 头部
	req1, _ := http.NewRequest("GET", "/test", nil)
	egReq1, satisfy1 := NewRequestFromHTTP(req1, hash, total)
	if !satisfy1 || egReq1.IsRange || egReq1.Offset != 0 || egReq1.Size != -1 {
		t.Errorf("expected full request: %+v", egReq1)
	}

	// 2. 带 Range 头部
	req2, _ := http.NewRequest("GET", "/test", nil)
	req2.Header.Set("Range", "bytes=100-199")
	egReq2, satisfy2 := NewRequestFromHTTP(req2, hash, total)
	if !satisfy2 || !egReq2.IsRange || egReq2.Offset != 100 || egReq2.Size != 100 {
		t.Errorf("expected range request: %+v", egReq2)
	}

	// 3. 越界 Range 头部
	req3, _ := http.NewRequest("GET", "/test", nil)
	req3.Header.Set("Range", "bytes=2000-")
	egReq3, satisfy3 := NewRequestFromHTTP(req3, hash, total)
	if satisfy3 || !egReq3.IsRange {
		t.Errorf("expected unsatisfiable range request: %+v", egReq3)
	}
}

// TestHTTPEgressSink_RFC7233_VideoStreaming 发现背景：高保真模拟 Safari/Chrome 浏览器 <video> 标签播放媒体文件的连续 Range 探测与流式读取。
func TestHTTPEgressSink_RFC7233_VideoStreaming(t *testing.T) {
	content := bytes.Repeat([]byte("M"), 2048) // 2048 字节模拟媒体文件
	fp := newFakeProvider()
	hash := fp.add(content)
	pipeline := NewPipeline(fp, "video-node")

	// Step 1: 浏览器探测 <video> 支持 bytes=0-1
	{
		rec := httptest.NewRecorder()
		sink := NewHTTPEgressSink(rec)
		req := EgressRequest{
			Hash:     hash,
			Offset:   0,
			Size:     2,
			IsRange:  true,
			Filename: "sample.mp4",
		}
		if err := pipeline.Serve(req, sink); err != nil {
			t.Fatalf("probe Serve failed: %v", err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 for video probe, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes 0-1/2048" {
			t.Errorf("Content-Range mismatch: %s", rec.Header().Get("Content-Range"))
		}
		if rec.Header().Get("Content-Length") != "2" {
			t.Errorf("Content-Length mismatch: %s", rec.Header().Get("Content-Length"))
		}
		if rec.Header().Get("Accept-Ranges") != "bytes" {
			t.Errorf("missing Accept-Ranges: bytes")
		}
	}

	// Step 2: 浏览器请求开放区间 bytes=0-（全量 Range，必须严格返回 206 Partial Content 而非 200 OK）
	{
		rec := httptest.NewRecorder()
		sink := NewHTTPEgressSink(rec)
		req := EgressRequest{
			Hash:     hash,
			Offset:   0,
			Size:     2048,
			IsRange:  true, // 显式出示了 Range 标头
			Filename: "sample.mp4",
		}
		if err := pipeline.Serve(req, sink); err != nil {
			t.Fatalf("open range Serve failed: %v", err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 for bytes=0- Range, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes 0-2047/2048" {
			t.Errorf("Content-Range mismatch: %s", rec.Header().Get("Content-Range"))
		}
		if rec.Body.Len() != 2048 {
			t.Errorf("body length mismatch: got %d", rec.Body.Len())
		}
	}

	// Step 3: 浏览器读取倒数 512 字节（读取 MP4 moov atom）bytes=-512
	{
		rec := httptest.NewRecorder()
		sink := NewHTTPEgressSink(rec)
		req := EgressRequest{
			Hash:     hash,
			Offset:   2048 - 512,
			Size:     512,
			IsRange:  true,
			Filename: "sample.mp4",
		}
		if err := pipeline.Serve(req, sink); err != nil {
			t.Fatalf("suffix range Serve failed: %v", err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 for suffix Range, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes 1536-2047/2048" {
			t.Errorf("Content-Range mismatch: %s", rec.Header().Get("Content-Range"))
		}
		if rec.Body.Len() != 512 {
			t.Errorf("body length mismatch: got %d", rec.Body.Len())
		}
	}

	// Step 4: 浏览器越界请求 bytes=3000- -> 必须返回 416 并携带 Content-Range: bytes */2048
	{
		rec := httptest.NewRecorder()
		sink := NewHTTPEgressSink(rec)
		req := EgressRequest{
			Hash:    hash,
			Offset:  3000,
			Size:    100,
			IsRange: true,
		}
		err := pipeline.Serve(req, sink)
		if err == nil {
			t.Fatalf("expected 416 error for out-of-range request")
		}
		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("expected 416, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes */2048" {
			t.Errorf("expected 'bytes */2048' in 416 Content-Range, got %q", rec.Header().Get("Content-Range"))
		}
	}
}

// TestFormatContentRange 发现背景：验证 RFC 7233 Content-Range 格式化辅助函数的正确性。
func TestFormatContentRange(t *testing.T) {
	if s := FormatContentRange(0, 100, 1000); s != "bytes 0-99/1000" {
		t.Errorf("FormatContentRange mismatch: %s", s)
	}
	if s := FormatContentRange(100, 0, 1000); s != "bytes */1000" {
		t.Errorf("FormatContentRange empty size mismatch: %s", s)
	}
	if s := FormatContentRange(0, 100, 0); s != "bytes */0" {
		t.Errorf("FormatContentRange zero total mismatch: %s", s)
	}
}
