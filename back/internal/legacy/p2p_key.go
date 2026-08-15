package legacy

import (
	"crypto/rand"
	"os"
	"path/filepath"

	"peerdrive/internal/config"
	"peerdrive/internal/log"

	"github.com/libp2p/go-libp2p/core/crypto"
)

// getKeyPath 解析 libp2p 私钥持久化路径。
// 优先级：P2PKeyFile 显式配置 > AuthToken 存在时默认 <StorageDir>/libp2p.key > 空（不持久化）。
func getKeyPath(cfg *config.Config) string {
	if cfg.P2PKeyFile != "" {
		return cfg.P2PKeyFile
	}
	if cfg.NodeAuthToken == "" {
		return ""
	}
	return filepath.Join(cfg.StorageDir, "libp2p.key")
}

// loadOrCreateKey 加载或创建持久化 libp2p 私钥。
// 返回 (nil, nil) 表示不需要持久化（匿名节点），libp2p 将使用 ephemeral 密钥。
func loadOrCreateKey(cfg *config.Config) (crypto.PrivKey, error) {
	keyPath := getKeyPath(cfg)
	if keyPath == "" {
		return nil, nil
	}

	if key, err := loadKeyFromFile(keyPath); err == nil {
		log.LogInfo("p2p: loaded persistent identity from %s", keyPath)
		return key, nil
	}

	privKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		log.LogWarn("p2p: failed to generate key, falling back to ephemeral: %v", err)
		return nil, nil
	}

	if err := saveKeyToFile(privKey, keyPath); err != nil {
		log.LogWarn("p2p: failed to save key to %s, falling back to ephemeral: %v", keyPath, err)
		return nil, nil
	}

	log.LogInfo("p2p: generated new persistent identity, saved to %s", keyPath)
	return privKey, nil
}

// saveKeyToFile 将私钥序列化并写入文件（权限 0600）。
func saveKeyToFile(key crypto.PrivKey, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := crypto.MarshalPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// loadKeyFromFile 从文件读取并反序列化私钥。
func loadKeyFromFile(path string) (crypto.PrivKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return crypto.UnmarshalPrivateKey(data)
}
