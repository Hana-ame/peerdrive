package service

import (
	"os"
	"testing"

	"peerdrive/internal/model"

	"github.com/stretchr/testify/assert"
)

func validEntries() []model.AnonCollectionEntry {
	return []model.AnonCollectionEntry{
		{Path: "README.md", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
}

// Discovery background: the frontend "broadcast" was changed to three options
// (public/specified permissions/only self), whether a restricted collection can
// be seen by others depends entirely on CanView; and the easiest thing to get
// wrong in CanView is treating "no account (unauthenticated request)" as
// allowed.
func TestAnonCollection_CanView(t *testing.T) {
	coll := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityRestricted, AccessList: []string{"bob"}}

	assert.True(t, coll.CanView("bob"), "account in access list should be allowed")
	assert.True(t, coll.CanView("alice"), "owner should be allowed even outside the list")
	assert.False(t, coll.CanView("carol"), "account outside list should be denied")
	assert.False(t, coll.CanView(""), "unauthenticated request cannot bypass restricted")

	privateColl := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityPrivate}
	assert.True(t, privateColl.CanView("alice"))
	assert.False(t, privateColl.CanView("bob"))
	assert.False(t, privateColl.CanView(""))

	// Legacy collections don't have a visibility field (JSON written by old
	// versions) → fallback to public
	legacy := &model.AnonCollection{Owner: "alice"}
	assert.Equal(t, model.VisibilityPublic, legacy.EffectiveVisibility())
	assert.True(t, legacy.CanView(""))
}

// Discovery background: when restricted has no access list, the collection
// becomes "visible to nobody" — a typical UI misuse where the user switched the
// option but forgot to pick people. This must be blocked at the service layer,
// rather than silently generating a hash nobody can open.
func TestCreateCollectionWithVisibility_AccessListRequired(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	_, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityRestricted, nil, "alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access_list required")

	_, err = svc.CreateCollectionWithVisibility("c", validEntries(), nil, "bogus", []string{"bob"}, "alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid visibility")
}

// Discovery background: collections are content-addressed, permissions written
// into JSON participate in the digest — changing permissions must produce a new
// hash. The previous worry was "can the old hash still be read", the answer is
// yes, and it reflects the old permissions (both coexist). This test locks in
// that semantics.
func TestUpdateCollectionVisibility_ProducesNewHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	newHash, err := svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, newHash, "changing permissions must produce a new hash")

	updated, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPrivate, updated.EffectiveVisibility())
	assert.False(t, updated.CanView("bob"))

	// Old hash still resolves to old permissions (snapshot semantics, not
	// in-place modification)
	old, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPublic, old.EffectiveVisibility())
}

// Discovery background: a third party who gets the hash should not be able to
// modify someone else's permission level. Content-addressed storage has no ACL
// table, the only ownership basis is the Owner field, which must be explicitly
// compared.
func TestUpdateCollectionVisibility_OwnershipEnforced(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "bob")
	assert.Error(t, err, "non-owner cannot change visibility")

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "")
	assert.Error(t, err, "unauthenticated request cannot change visibility")
}

// Discovery background: GetCollectionVisibleTo is the only entry point for
// reading collections by remote (future regserver-authenticated peers),
// unauthorized access must manifest as 404 not 403 — otherwise it's
// equivalent to confirming to the attacker "this hash exists and belongs to
// someone else".
func TestGetCollectionVisibleTo_DeniedLooksLikeNotFound(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityRestricted, []string{"bob"}, "alice")
	assert.NoError(t, err)

	_, err = svc.GetCollectionVisibleTo(hash, "carol")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")

	coll, err := svc.GetCollectionVisibleTo(hash, "bob")
	assert.NoError(t, err)
	assert.Equal(t, "c", coll.FriendlyName)
}

// Discovery background (2026-09-19): commit rolls out a new hash, and new
// collections used to be "bare collections with only version/name/entries/tags"
// — a restricted collection would silently revert to public after a single
// commit (content exposed externally via the new hash). This test locks in
// permission inheritance + unauthorized rejection.
func TestCommitCollection_PreservesVisibility(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityRestricted, []string{"bob"}, "alice")
	assert.NoError(t, err)

	newHash, err := svc.CommitCollection(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, newHash, "commit must produce a new hash")

	committed, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityRestricted, committed.EffectiveVisibility(), "commit must not downgrade restricted to public")
	assert.Equal(t, []string{"bob"}, committed.AccessList, "access list must be inherited")
	assert.Equal(t, "alice", committed.Owner, "owner must be inherited")
	assert.False(t, committed.CanView("carol"), "inherited permissions still deny accounts outside the list")
	assert.True(t, committed.CanView("bob"))
}

// Discovery background (same batch): the commit entry point must not read the
// source collection before validating permissions — otherwise someone with a
// hash could commit someone else's private collection, then use the "new
// collection" to write themselves as owner.
func TestCommitCollection_DeniedForNonViewer(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.CommitCollection(hash, nil, "", "bob")
	assert.Error(t, err, "non-owner cannot commit private collection")
	assert.Contains(t, err.Error(), "source collection not found")
}

// Discovery background (2026-09-19): the controller comment has always said
// "empty hash in entries = delete entry", but the service layer reused the
// creation-time providers validation and judged empty providers as invalid →
// the deletion path was never reached (removeEmpty was dead code). This test
// locks in the "empty providers/empty hash = delete" semantics, while also
// confirming that "only set Hash, no Providers" is normalized to an addition
// rather than a mistaken deletion.
func TestCommitCollection_EmptyHashRemovesEntry(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	// Only set Hash (old-format callers) → normalized to addition, must not
	// become deletion
	addHash, err := svc.CommitCollection(hash, []model.AnonCollectionEntry{
		{Path: "second.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add", "alice")
	assert.NoError(t, err)
	added, err := svc.GetCollectionByHash(addHash)
	assert.NoError(t, err)
	assert.Len(t, added.Entries, 2, "legacy-format hash field should be normalized to provider and added")

	// Empty hash / empty providers → delete that entry
	delHash, err := svc.CommitCollection(addHash, []model.AnonCollectionEntry{
		{Path: "second.txt"},
	}, "remove", "alice")
	assert.NoError(t, err)
	deleted, err := svc.GetCollectionByHash(delHash)
	assert.NoError(t, err)
	assert.Len(t, deleted.Entries, 1, "empty hash should delete the corresponding entry")
	assert.Equal(t, "README.md", deleted.Entries[0].Path)
}
