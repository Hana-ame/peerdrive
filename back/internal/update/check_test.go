package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// manifestFor 构造一个覆盖当前平台资产的合法清单 JSON。
func manifestFor(t *testing.T, version string) string {
	t.Helper()
	return fmt.Sprintf(`{
  "version": %q,
  "assets": [
    {"name": %q, "os": %q, "arch": %q,
     "url": "https://example.invalid/dl/%s", "sha256": %q}
  ]
}`, version, AssetName(runtime.GOOS, runtime.GOARCH), runtime.GOOS, runtime.GOARCH,
		AssetName(runtime.GOOS, runtime.GOARCH), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, manifestFor(t, "v0.3.1"))
	}))
	defer srv.Close()

	res, err := Check(context.Background(), "v0.3.0", srv.URL)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	if !res.HasUpdate || res.Latest != "v0.3.1" || res.Asset == nil {
		t.Fatalf("Check(newer) = %+v", res)
	}

	res, err = Check(context.Background(), "v0.3.1", srv.URL)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	if res.HasUpdate {
		t.Errorf("Check(same version) HasUpdate = true, want false")
	}

	res, err = Check(context.Background(), "(dev)", srv.URL)
	if err != nil || !res.HasUpdate {
		t.Errorf("Check(dev build) HasUpdate = %v, err = %v, want true", res.HasUpdate, err)
	}
}

func TestCheckErrors(t *testing.T) {
	// HTTP 500。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	if _, err := Check(context.Background(), "v0.3.0", srv.URL); err == nil {
		t.Error("Check(500) expected error")
	}
	srv.Close()

	// 非法 JSON。
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	if _, err := Check(context.Background(), "v0.3.0", srv.URL); err == nil {
		t.Error("Check(invalid json) expected error")
	}
	srv.Close()

	// 清单缺本平台资产。
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version": "v0.3.1", "assets": [{"name": "peerdrive-freebsd-amd64", "os": "freebsd", "arch": "amd64", "url": "u", "sha256": "%s"}]}`,
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	}))
	if _, err := Check(context.Background(), "v0.3.0", srv.URL); err == nil {
		t.Error("Check(missing platform asset) expected error")
	}
	srv.Close()
}
