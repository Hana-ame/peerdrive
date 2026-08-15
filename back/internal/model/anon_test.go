package model

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAnonCollection_WithNameAndEntries(t *testing.T) {
	e1 := AnonCollectionEntry{Path: "/a.txt", Hash: "abc123"}
	e1.Normalize()
	e2 := AnonCollectionEntry{Path: "/b.txt", Hash: "def456"}
	e2.Normalize()
	a := NewAnonCollection("my-collection", []AnonCollectionEntry{e1, e2}, nil)

	assert.Equal(t, 2, a.Version)
	assert.Equal(t, "my-collection", a.FriendlyName)
	assert.Len(t, a.Entries, 2)
	assert.Equal(t, "/a.txt", a.Entries[0].Path)
	assert.Equal(t, "abc123", a.Entries[0].GetPrimaryHash())
	assert.NotEmpty(t, a.CreatedAt)
}

func TestNewAnonCollection_WithNilEntries(t *testing.T) {
	a := NewAnonCollection("test", nil, nil)

	assert.NotNil(t, a.Entries)
	assert.Len(t, a.Entries, 0)
	assert.Equal(t, "test", a.FriendlyName)
}

func TestNewAnonCollection_WithEmptyName(t *testing.T) {
	entries := []AnonCollectionEntry{{Path: "/x", Hash: "hash"}}
	a := NewAnonCollection("", entries, nil)

	assert.Equal(t, "", a.FriendlyName)
	assert.Len(t, a.Entries, 1)
}

func TestAnonCollectionEntry_MarshalJSON(t *testing.T) {
	e := AnonCollectionEntry{
		Path: "file.txt",
		Providers: []Provider{
			{Type: "sha256", Value: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a", MimeType: "text/plain"},
		},
	}
	data, err := json.Marshal(e)
	assert.NoError(t, err)

	var out map[string]interface{}
	json.Unmarshal(data, &out)
	assert.Equal(t, "file.txt", out["path"])
	providers := out["providers"].([]interface{})
	assert.Len(t, providers, 1)
	p0 := providers[0].(map[string]interface{})
	assert.Equal(t, "sha256", p0["type"])
	// Hash 字段也应输出以保证旧客户端兼容
	assert.Equal(t, "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a", out["hash"])
}

func TestAnonCollectionEntry_UnmarshalJSON_OldFormat(t *testing.T) {
	data := []byte(`{"path":"old.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	var e AnonCollectionEntry
	err := json.Unmarshal(data, &e)
	assert.NoError(t, err)
	assert.Equal(t, "old.txt", e.Path)
	assert.Len(t, e.Providers, 1)
	assert.Equal(t, "sha256", e.Providers[0].Type)
	assert.Equal(t, "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a", e.Providers[0].Value)
}

func TestAnonCollectionEntry_UnmarshalJSON_NewFormat(t *testing.T) {
	data := []byte(`{"path":"new.txt","providers":[{"type":"url","value":"https://example.com/file"}]}`)
	var e AnonCollectionEntry
	err := json.Unmarshal(data, &e)
	assert.NoError(t, err)
	assert.Equal(t, "new.txt", e.Path)
	assert.Len(t, e.Providers, 1)
	assert.Equal(t, "url", e.Providers[0].Type)
}

func TestAnonCollectionEntry_GetPrimaryHash(t *testing.T) {
	e := AnonCollectionEntry{
		Providers: []Provider{
			{Type: "url", Value: "https://example.com/file"},
			{Type: "sha256", Value: "deadbeef"},
		},
	}
	assert.Equal(t, "deadbeef", e.GetPrimaryHash())
}

func TestAnonCollectionEntry_GetPrimaryMime(t *testing.T) {
	e := AnonCollectionEntry{
		Providers: []Provider{
			{Type: "sha256", Value: "abc", MimeType: "image/png"},
			{Type: "url", Value: "https://example.com/file"},
		},
	}
	assert.Equal(t, "image/png", e.GetPrimaryMime())
}

func TestAnonCollection_NormalizeEntries(t *testing.T) {
	c := &AnonCollection{
		Version: 1,
		Entries: []AnonCollectionEntry{
			{Path: "legacy.txt", Hash: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		},
	}
	c.NormalizeEntries()
	assert.Equal(t, 2, c.Version)
	assert.Len(t, c.Entries[0].Providers, 1)
	assert.Equal(t, "sha256", c.Entries[0].Providers[0].Type)
}

func TestAnonCollectionEntry_Normalize(t *testing.T) {
	e := AnonCollectionEntry{Path: "test.txt", Hash: "abc123"}
	e.Normalize()
	assert.Len(t, e.Providers, 1)
	assert.Equal(t, "sha256", e.Providers[0].Type)
	assert.Equal(t, "abc123", e.Providers[0].Value)
}
