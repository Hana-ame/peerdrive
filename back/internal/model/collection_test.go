package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollection_Init(t *testing.T) {
	hash := "abc123hash"
	c := Collection{
		ID:             1,
		Username:       "alice",
		CollectionName: "docs",
		CurrentHash:    &hash,
		CreatedAt:      "2024-01-01T00:00:00Z",
	}

	assert.Equal(t, 1, c.ID)
	assert.Equal(t, "alice", c.Username)
	assert.Equal(t, "docs", c.CollectionName)
	assert.Equal(t, &hash, c.CurrentHash)
	assert.Equal(t, "abc123hash", *c.CurrentHash)
	assert.Equal(t, "2024-01-01T00:00:00Z", c.CreatedAt)
}

func TestCollectionEntry_Init(t *testing.T) {
	e := CollectionEntry{
		ID:           42,
		CollectionID: 7,
		Path:         "/docs/readme.md",
		FileHash:     "def789hash",
	}

	assert.Equal(t, 42, e.ID)
	assert.Equal(t, 7, e.CollectionID)
	assert.Equal(t, "/docs/readme.md", e.Path)
	assert.Equal(t, "def789hash", e.FileHash)
}

func TestCollectionVersion_Init(t *testing.T) {
	parentID := 5
	v := CollectionVersion{
		ID:              100,
		CollectionID:    10,
		VersionNumber:   3,
		CommitMessage:   "update docs",
		CreatedAt:       "2024-06-01T12:00:00Z",
		ParentVersionID: &parentID,
	}

	assert.Equal(t, 100, v.ID)
	assert.Equal(t, 10, v.CollectionID)
	assert.Equal(t, 3, v.VersionNumber)
	assert.Equal(t, "update docs", v.CommitMessage)
	assert.Equal(t, "2024-06-01T12:00:00Z", v.CreatedAt)
	assert.NotNil(t, v.ParentVersionID)
	assert.Equal(t, 5, *v.ParentVersionID)
}
