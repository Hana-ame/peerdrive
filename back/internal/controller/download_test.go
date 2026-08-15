package controller

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupRangeTest() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

func rangeStatusCode(c *gin.Context) int {
	return c.Writer.Status()
}

func TestHandleRangeRequest_StandardRange(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("hello world, this is a test file with some content")

	handled := handleRangeRequest(c, data, "bytes=0-4")
	if !handled {
		t.Fatal("expected range to be handled")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	if w.Body.String() != "hello" {
		t.Errorf("expected 'hello', got %q", w.Body.String())
	}
	if w.Header().Get("Content-Range") == "" {
		t.Error("expected Content-Range header")
	}
}

func TestHandleRangeRequest_MidRange(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("hello world, this is a test file with some content")

	handled := handleRangeRequest(c, data, "bytes=6-10")
	if !handled {
		t.Fatal("expected range to be handled")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	if w.Body.String() != "world" {
		t.Errorf("expected 'world', got %q", w.Body.String())
	}
}

func TestHandleRangeRequest_SuffixRange(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("hello world, this is a test file with some content")

	handled := handleRangeRequest(c, data, "bytes=-7")
	if !handled {
		t.Fatal("expected suffix range to be handled")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	if w.Body.String() != "content" {
		t.Errorf("expected 'content', got %q", w.Body.String())
	}
}

func TestHandleRangeRequest_OpenEndedRange(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("hello world, this is a test file with some content")

	handled := handleRangeRequest(c, data, "bytes=6-")
	if !handled {
		t.Fatal("expected open-ended range to be handled")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	expected := "world, this is a test file with some content"
	if w.Body.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.Body.String())
	}
}

func TestHandleRangeRequest_ZeroByteFile(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte{} // 0-byte file

	handled := handleRangeRequest(c, data, "bytes=0-10")
	if handled {
		t.Error("expected range NOT to be handled for 0-byte file")
	}
	// No status should have been written
	if w.Code != 200 {
		t.Errorf("expected default 200, got %d", w.Code)
	}
}

func TestHandleRangeRequest_RangeBeyondFile(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("short file")

	handled := handleRangeRequest(c, data, "bytes=100-200")
	if !handled {
		t.Fatal("expected out-of-range to be handled as 416")
	}
	if rangeStatusCode(c) != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("expected 416, got %d", rangeStatusCode(c))
	}
	if w.Header().Get("Content-Range") != "bytes */10" {
		t.Errorf("expected 'bytes */10', got %q", w.Header().Get("Content-Range"))
	}
}

func TestHandleRangeRequest_EndBeyondFile(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("hello world")

	handled := handleRangeRequest(c, data, "bytes=0-100")
	if !handled {
		t.Fatal("expected range to be handled with clamped end")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	if w.Body.String() != "hello world" {
		t.Errorf("expected 'hello world', got %q", w.Body.String())
	}
	if w.Header().Get("Content-Range") != "bytes 0-10/11" {
		t.Errorf("expected 'bytes 0-10/11', got %q", w.Header().Get("Content-Range"))
	}
}

func TestHandleRangeRequest_SuffixLargerThanFile(t *testing.T) {
	c, w := setupRangeTest()
	data := []byte("short")

	handled := handleRangeRequest(c, data, "bytes=-100")
	if !handled {
		t.Fatal("expected suffix range to be handled when suffix > total")
	}
	if rangeStatusCode(c) != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", rangeStatusCode(c))
	}
	if w.Body.String() != "short" {
		t.Errorf("expected 'short', got %q", w.Body.String())
	}
}
