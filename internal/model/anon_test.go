package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAnonCollection_WithNameAndEntries(t *testing.T) {
	entries := []AnonCollectionEntry{
		{Path: "/a.txt", Hash: "abc123"},
		{Path: "/b.txt", Hash: "def456"},
	}
	a := NewAnonCollection("my-collection", entries)

	assert.Equal(t, 1, a.Version)
	assert.Equal(t, "my-collection", a.FriendlyName)
	assert.Len(t, a.Entries, 2)
	assert.Equal(t, "/a.txt", a.Entries[0].Path)
	assert.Equal(t, "abc123", a.Entries[0].Hash)
	assert.NotEmpty(t, a.CreatedAt)
}

func TestNewAnonCollection_WithNilEntries(t *testing.T) {
	a := NewAnonCollection("test", nil)

	assert.NotNil(t, a.Entries)
	assert.Len(t, a.Entries, 0)
	assert.Equal(t, "test", a.FriendlyName)
}

func TestNewAnonCollection_WithEmptyName(t *testing.T) {
	entries := []AnonCollectionEntry{{Path: "/x", Hash: "hash"}}
	a := NewAnonCollection("", entries)

	assert.Equal(t, "", a.FriendlyName)
	assert.Len(t, a.Entries, 1)
}
