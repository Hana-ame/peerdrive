package service

// nodeshare_token_test.go — 跨节点身份令牌与受限合集解锁测试（API-DESIGN.md §8 / Phase 7）。
// 发现背景：受限合集（restricted visibility / access_list）与属主私有合集
// 在未携带有效身份令牌时必须严格保密；当对端请求携带有效令牌并匹配 AccessList/Owner 时，
// 节点在 share-resp 帧中解锁条目列表，实现合规跨节点共享。

import (
	"testing"

	"peerdrive/internal/model"
)

func TestNodeShareRestrictedCollectionTokenUnlock(t *testing.T) {
	restrHash := sha("1111")
	privHash := sha("2222")

	colls := map[string]*model.AnonCollection{
		restrHash: {
			FriendlyName: "Restricted Project Docs",
			Visibility:   model.VisibilityRestricted,
			AccessPolicy: model.AccessPolicyRestricted,
			AccessList:   []string{"alice", "charlie"},
			Owner:        "node-owner",
			Entries: []model.AnonCollectionEntry{
				{
					Path: "spec.pdf",
					Providers: []model.Provider{
						{Type: "sha256", Value: sha("spec-data")},
					},
				},
			},
		},
		privHash: {
			FriendlyName: "Owner Only Vault",
			Visibility:   model.VisibilityPrivate,
			AccessPolicy: model.AccessPolicyPrivate,
			Owner:        "node-owner",
			Entries: []model.AnonCollectionEntry{
				{
					Path: "keys.txt",
					Providers: []model.Provider{
						{Type: "sha256", Value: sha("keys-data")},
					},
				},
			},
		},
	}

	cfg := testShareCfg(true, restrHash+","+privHash, "")
	cfg.ShareFriends = "friend-peer"
	s := newShareWith(t, cfg, colls, nil, nil)

	// 注入身份验证器
	s.SetTokenVerifier(func(token string) string {
		switch token {
		case "token-alice":
			return "alice"
		case "token-bob":
			return "bob"
		case "token-owner":
			return "node-owner"
		default:
			return ""
		}
	})

	// 1. 匿名陌生人访问 (无 token) -> 受限合集与私有合集均不可见
	snap := s.SnapshotForToken("stranger-peer", "")
	if len(snap.Collections) != 0 {
		t.Fatalf("anon stranger must see 0 collections, got %d", len(snap.Collections))
	}

	// 2. 好友无 token 访问 -> 降级为 private 后可见壳体，但 isProtected=true 且 entries 为空
	snapFriend := s.SnapshotForToken("friend-peer", "")
	if len(snapFriend.Collections) != 2 {
		t.Fatalf("friend without token should see 2 private shells, got %d", len(snapFriend.Collections))
	}
	for _, c := range snapFriend.Collections {
		if !c.IsProtected {
			t.Errorf("collection %s should be protected for friend without token", c.Name)
		}
		if len(c.Entries) != 0 {
			t.Errorf("collection %s should have 0 entries without token", c.Name)
		}
	}

	// 3. 非授权用户访问 (Bob 携带有效 token，但不在 access_list 中) -> 0 collections
	snapBob := s.SnapshotForToken("stranger-peer", "token-bob")
	if len(snapBob.Collections) != 0 {
		t.Fatalf("unauthorized user Bob must see 0 collections, got %d", len(snapBob.Collections))
	}

	// 4. 授权用户访问 (Alice 携带有效 token，在 access_list 中) -> 解锁受限合集
	snapAlice := s.SnapshotForToken("stranger-peer", "token-alice")
	if len(snapAlice.Collections) != 1 {
		t.Fatalf("authorized user Alice should see exactly 1 collection, got %d", len(snapAlice.Collections))
	}
	collAlice := snapAlice.Collections[0]
	if collAlice.Hash != restrHash {
		t.Errorf("expected restricted collection %s, got %s", restrHash, collAlice.Hash)
	}
	if collAlice.IsProtected {
		t.Errorf("restricted collection should not be protected for authorized user Alice")
	}
	if len(collAlice.Entries) != 1 || collAlice.Entries[0].Path != "spec.pdf" {
		t.Errorf("restricted collection entries not unlocked for Alice: %+v", collAlice.Entries)
	}

	// 5. 属主访问 (携带 owner token) -> 同时解锁受限合集与私有合集
	snapOwner := s.SnapshotForToken("stranger-peer", "token-owner")
	if len(snapOwner.Collections) != 2 {
		t.Fatalf("owner should see 2 collections, got %d", len(snapOwner.Collections))
	}
	for _, c := range snapOwner.Collections {
		if c.IsProtected {
			t.Errorf("collection %s should not be protected for owner", c.Name)
		}
		if len(c.Entries) != 1 {
			t.Errorf("collection %s entries should be unlocked for owner, got %d", c.Name, len(c.Entries))
		}
	}
}
