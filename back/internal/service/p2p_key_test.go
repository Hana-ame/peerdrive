package service

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestGetKeyPath(t *testing.T) {
	t.Run("explicit path", func(t *testing.T) {
		cfg := &config.Config{P2PKeyFile: "/custom/path/key"}
		if p := getKeyPath(cfg); p != "/custom/path/key" {
			t.Errorf("expected /custom/path/key, got %s", p)
		}
	})

	t.Run("anonymous without auth token", func(t *testing.T) {
		cfg := &config.Config{StorageDir: "/tmp/foo"}
		if p := getKeyPath(cfg); p != "" {
			t.Errorf("expected empty path for anonymous, got %s", p)
		}
	})

	t.Run("authenticated with default path", func(t *testing.T) {
		cfg := &config.Config{NodeAuthToken: "token123", StorageDir: "/tmp/storage"}
		if p := getKeyPath(cfg); p != filepath.Join("/tmp/storage", "libp2p.key") {
			t.Errorf("expected default path, got %s", p)
		}
	})
}

func TestLoadOrCreateKey(t *testing.T) {
	t.Run("anonymous returns nil", func(t *testing.T) {
		cfg := &config.Config{}
		key, err := loadOrCreateKey(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key != nil {
			t.Error("expected nil key for anonymous node")
		}
	})

	t.Run("generate and round-trip", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{
			NodeAuthToken: "token123",
			StorageDir:    tmpDir,
		}

		key1, err := loadOrCreateKey(cfg)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if key1 == nil {
			t.Fatal("expected non-nil key for authenticated node")
		}

		key2, err := loadOrCreateKey(cfg)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}

		ok := key1.Equals(key2)
		if !ok {
			t.Error("keys from two calls should be equal (round-trip)")
		}
	})

	t.Run("stable peer ID across restarts", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{
			NodeAuthToken: "token456",
			StorageDir:    tmpDir,
		}

		key1, _ := loadOrCreateKey(cfg)
		key2, _ := loadOrCreateKey(cfg)

		id1, _ := peer.IDFromPublicKey(key1.GetPublic())
		id2, _ := peer.IDFromPublicKey(key2.GetPublic())
		if id1 != id2 {
			t.Errorf("peer ID changed across restarts: %s vs %s", id1, id2)
		}
	})

	t.Run("custom key file path", func(t *testing.T) {
		tmpDir := t.TempDir()
		customPath := filepath.Join(tmpDir, "custom.key")
		cfg := &config.Config{
			NodeAuthToken: "token789",
			P2PKeyFile:    customPath,
		}

		key, err := loadOrCreateKey(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key == nil {
			t.Fatal("expected non-nil key")
		}
		if _, err := os.Stat(customPath); os.IsNotExist(err) {
			t.Error("custom key file not created")
		}
	})

	t.Run("key file has correct permissions", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{
			NodeAuthToken: "perm-test",
			StorageDir:    tmpDir,
		}

		_, _ = loadOrCreateKey(cfg)
		keyPath := filepath.Join(tmpDir, "libp2p.key")
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("stat key file: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Errorf("expected 0600 permissions, got %04o", mode)
		}
	})
}

func TestSaveLoadKey(t *testing.T) {
	t.Run("save and load", func(t *testing.T) {
		tmpDir := t.TempDir()
		keyPath := filepath.Join(tmpDir, "test.key")

		privKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}

		if err := saveKeyToFile(privKey, keyPath); err != nil {
			t.Fatalf("save: %v", err)
		}

		loaded, err := loadKeyFromFile(keyPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		ok := privKey.Equals(loaded)
		if !ok {
			t.Error("loaded key does not match saved key")
		}
	})

	t.Run("load corrupted file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		keyPath := filepath.Join(tmpDir, "corrupt.key")
		os.WriteFile(keyPath, []byte("not a valid key"), 0600)

		if _, err := loadKeyFromFile(keyPath); err == nil {
			t.Error("expected error loading corrupted key file")
		}
	})
}
