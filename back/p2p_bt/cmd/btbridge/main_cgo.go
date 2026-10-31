//go:build cgo

// Command btbridge builds the Peerdrive BitTorrent runtime as a C-shared
// library (peerdrive_bt.dll on Windows, .so elsewhere).
//
// 发现背景：torrent 栈（anacrolix/torrent + dht + tracker 拉取）之前被主程序静态
// 链接，且无条件创建客户端 —— 进程一启动就开监听端口、跑 DHT、联网拉公共
// tracker，与 README 承诺的「off by default」不符。本命令把整个运行时搬进一个
// 按需加载的动态库：主程序（internal/btloader）只在 PEERDRIVE_BT_ENABLE=1 时
// LoadLibrary 它，否则连一个字节都不载入。
//
// 跨 ABI 只走字符串：C 侧看不到任何 Go 对象，编组全部由 btservice 的 JSON
// 入口承担（详见 p2p_bt/btservice）。导出面刻意极小：
//
//	int       pd_bt_open(const char* cfg_json);       // >0 handle, 0 = 失败
//	char*     pd_bt_last_error(void);                 // 需 pd_bt_free
//	char*     pd_bt_call(int h, const char* op, const char* args_json);
//	void      pd_bt_free(char* p);
//	int       pd_bt_close(int h);
//	char*     pd_bt_version(void);                    // 需 pd_bt_free
//
// pd_bt_call 恒返回一个信封：{"ok":true,"data":...} 或 {"ok":false,"error":"..."}，
// 宿主只需一条解码路径。
//
// 构建（Windows，交叉编译）：
//
//	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
//	  go build -buildmode=c-shared -o peerdrive_bt.dll ./cmd/btbridge
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/Hana-ame/go-peerdrive-bt/btservice"
)

// reply is the single response envelope seen by the host.
type reply struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

var (
	mu      sync.Mutex
	handles       = map[int32]*btservice.Service{}
	nextID  int32 = 1
	lastErr string
)

// pd_bt_open starts a BT runtime and returns a handle. Returns 0 on failure;
// the reason is available from pd_bt_last_error.
//
//export pd_bt_open
func pd_bt_open(cfgJSON *C.char) (h C.int) {
	defer func() {
		if r := recover(); r != nil {
			setLastErr(fmt.Sprintf("panic in pd_bt_open: %v", r))
			h = 0
		}
	}()

	var cfg btservice.Config
	if cfgJSON != nil {
		if err := json.Unmarshal([]byte(C.GoString(cfgJSON)), &cfg); err != nil {
			setLastErr("invalid config json: " + err.Error())
			return 0
		}
	}

	svc, err := btservice.New(cfg)
	if err != nil {
		setLastErr(err.Error())
		return 0
	}

	// Handles are int32 on purpose: the ABI must stay callable from 32-bit hosts
	// (back/dist ships a 386 build). A long long argument would need two stack
	// words on 386 and syscall.Syscall cannot pass it through uintptr.
	mu.Lock()
	if nextID > math.MaxInt32 {
		mu.Unlock()
		_ = svc.Close()
		setLastErr("bt handle space exhausted")
		return 0
	}
	id := nextID
	nextID++
	handles[id] = svc
	mu.Unlock()

	return C.int(id)
}

// pd_bt_call executes one op on a handle. args may be NULL or "" for no args.
// The result must be released with pd_bt_free.
//
//export pd_bt_call
func pd_bt_call(h C.int, op *C.char, argsJSON *C.char) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			out = replyToC(reply{Error: fmt.Sprintf("panic in pd_bt_call: %v", r)})
		}
	}()

	if op == nil {
		return replyToC(reply{Error: "op is required"})
	}

	svc, err := lookup(int32(h))
	if err != nil {
		return replyToC(reply{Error: err.Error()})
	}

	var args json.RawMessage
	if argsJSON != nil {
		if s := C.GoString(argsJSON); s != "" {
			args = json.RawMessage(s)
		}
	}

	val, err := svc.Call(C.GoString(op), args)
	if err != nil {
		return replyToC(reply{Error: err.Error()})
	}

	data, err := json.Marshal(val)
	if err != nil {
		return replyToC(reply{Error: "encode reply: " + err.Error()})
	}
	return replyToC(reply{OK: true, Data: data})
}

// pd_bt_close shuts a runtime down and forgets the handle.
//
//export pd_bt_close
func pd_bt_close(h C.int) (rc C.int) {
	defer func() {
		if r := recover(); r != nil {
			setLastErr(fmt.Sprintf("panic in pd_bt_close: %v", r))
			rc = -1
		}
	}()

	mu.Lock()
	svc, ok := handles[int32(h)]
	delete(handles, int32(h))
	mu.Unlock()
	if !ok {
		return -1
	}
	if err := svc.Close(); err != nil {
		setLastErr(err.Error())
		return -1
	}
	return 0
}

// pd_bt_last_error returns the most recent open/close error. Caller frees.
//
//export pd_bt_last_error
func pd_bt_last_error() *C.char {
	mu.Lock()
	msg := lastErr
	mu.Unlock()
	return C.CString(msg)
}

// pd_bt_version reports what this binary was built from (hosts log it once at
// load, which is the only cheap way to catch a stale DLL next to a new exe).
//
//export pd_bt_version
func pd_bt_version() *C.char {
	return C.CString(fmt.Sprintf("btservice/1 %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))
}

// pd_bt_free releases a string returned by this library.
//
//export pd_bt_free
func pd_bt_free(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

// ---- internals ----

func lookup(h int32) (*btservice.Service, error) {
	mu.Lock()
	svc, ok := handles[h]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("invalid bt handle %d", h)
	}
	return svc, nil
}

func setLastErr(msg string) {
	mu.Lock()
	lastErr = msg
	mu.Unlock()
}

func replyToC(r reply) *C.char {
	raw, err := json.Marshal(r)
	if err != nil {
		// json.Marshal on this struct cannot fail: every field is a string or a
		// pre-validated RawMessage. Keep a static fallback anyway so a corrupted
		// reply can never surface as a nil pointer at the C side.
		raw = []byte(`{"ok":false,"error":"encode reply failed"}`)
	}
	return C.CString(string(raw))
}

// main is required by -buildmode=c-shared but never runs: the Go runtime starts
// on DLL load, and work happens only when the host calls an export.
func main() {}
