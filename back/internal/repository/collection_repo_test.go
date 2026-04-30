package repository

import (
	"path/filepath"
	"testing"
)

func TestCollectionRepo_GetOrCreate(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))

	id, err := GetOrCreateCollection("testuser", "test-coll")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}

	// Second call returns same ID
	id2, err := GetOrCreateCollection("testuser", "test-coll")
	if err != nil {
		t.Fatalf("second GetOrCreate failed: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same ID, got %d vs %d", id, id2)
	}
}

func TestCollectionRepo_CreateWithVisibility(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))

	id, err := CreateCollectionWithVisibility("vuser", "pub-coll", "public")
	if err != nil {
		t.Fatalf("CreateCollectionWithVisibility failed: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	col, err := GetCollection("vuser", "pub-coll")
	if err != nil {
		t.Fatalf("GetCollection failed: %v", err)
	}
	if col.Username != "vuser" {
		t.Errorf("expected vuser, got %s", col.Username)
	}
}

func TestCollectionRepo_EntriesCRUD(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))
	id, _ := GetOrCreateCollection("euser", "entries-coll")

	err := AddCollectionEntry(id, "dir/file.txt", "abc123hash")
	if err != nil {
		t.Fatalf("AddCollectionEntry failed: %v", err)
	}
	err = AddCollectionEntry(id, "other.txt", "def456hash")
	if err != nil {
		t.Fatalf("AddCollectionEntry 2 failed: %v", err)
	}

	entries, err := ListCollectionEntries(id)
	if err != nil {
		t.Fatalf("ListCollectionEntries failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	err = RemoveCollectionEntry(id, "dir/file.txt")
	if err != nil {
		t.Fatalf("RemoveCollectionEntry failed: %v", err)
	}
	entries, _ = ListCollectionEntries(id)
	if len(entries) != 1 {
		t.Errorf("expected 1 after delete, got %d", len(entries))
	}
}

func TestCollectionRepo_VersionFlow(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))
	id, _ := GetOrCreateCollection("cuser", "version-coll")
	AddCollectionEntry(id, "a.txt", "hash1")
	AddCollectionEntry(id, "b.txt", "hash2")

	vid, vnum, err := CreateVersion(id, "first version", nil)
	if err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}
	if vid <= 0 || vnum != 1 {
		t.Errorf("expected vnum=1, got %d (vid=%d)", vnum, vid)
	}

	versions, err := GetVersionLog(id)
	if err != nil {
		t.Fatalf("GetVersionLog failed: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(versions))
	}
}

func TestCollectionRepo_ListAndSearch(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))
	GetOrCreateCollection("lu", "alpha")
	GetOrCreateCollection("lu", "beta")

	cols, err := ListCollections("lu")
	if err != nil {
		t.Fatalf("ListCollections failed: %v", err)
	}
	if len(cols) < 2 {
		t.Errorf("expected >=2, got %d", len(cols))
	}

	results, err := SearchCollections("alpha")
	if err != nil {
		t.Fatalf("SearchCollections failed: %v", err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 search result")
	}
}

func TestCollectionRepo_Tags(t *testing.T) {
	InitDB(filepath.Join(t.TempDir(), "test.db"))

	id, err := CreateCollectionWithTags("tuser", "tagged", "public", []string{"demo", "test"})
	if err != nil {
		t.Fatalf("CreateCollectionWithTags failed: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	err = UpdateCollectionTags("tuser", "tagged", []string{"updated", "v3"})
	if err != nil {
		t.Fatalf("UpdateCollectionTags failed: %v", err)
	}

	col, _ := GetCollection("tuser", "tagged")
	if col == nil {
		t.Fatal("collection not found")
	}
}
