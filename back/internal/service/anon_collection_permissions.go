package service

import (
	"fmt"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// UpdateCollectionVisibility creates a new collection with changed permissions based on the original.
// Background: the frontend changed "broadcast" to a three-tier switch, so users can
// switch tiers at any time. Collections are content-addressed, and permissions are
// written into the JSON and participate in the digest, so changing tiers necessarily
// produces a new hash -- the return value is the new identity.
// Access control: only the Owner (or a legacy local collection without an Owner) can change this.
func (s *AnonService) UpdateCollectionVisibility(hash, visibility string, accessList []string, requester string) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionVisibility")()
	if !model.IsValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility: %s", visibility)
	}
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		return "", fmt.Errorf("access_list required for restricted visibility")
	}

	src, err := s.GetCollectionByHash(hash)
	if err != nil {
		return "", err
	}
	// Legacy collections without an Owner (pre-dating the permissions fields) are treated
	// as local collections and allowed; once an Owner exists it must match, to prevent
	// a third party who obtained the hash from rewriting someone else's permission tier.
	if src.Owner != "" && src.Owner != requester {
		log.LogWarn("anon-svc: UpdateCollectionVisibility denied hash=%s owner=%s requester=%q", hash, src.Owner, requester)
		return "", fmt.Errorf("collection not found")
	}

	dst := *src
	dst.PrevSHA = hash
	dst.Visibility = visibility
	dst.AccessList = accessList
	if dst.Owner == "" && requester != "" {
		dst.Owner = requester
	}
	return s.saveCollectionJSON(&dst)
}

// UpdateCollectionPermissions modifies visibility, access policy, and PSK settings (Issue #338).
// Setting PrevSHA maintains a git-like history pointer.
func (s *AnonService) UpdateCollectionPermissions(
	hash string,
	visibility string,
	accessList []string,
	accessPolicy string,
	psk string,
	requester string,
) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionPermissions")()
	if visibility != "" && !model.IsValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility: %s", visibility)
	}
	if accessPolicy != "" && !model.IsValidAccessPolicy(accessPolicy) {
		return "", fmt.Errorf("invalid access policy: %s", accessPolicy)
	}
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		return "", fmt.Errorf("access_list required for restricted visibility")
	}

	src, err := s.GetCollectionByHash(hash)
	if err != nil {
		return "", err
	}
	if src.Owner != "" && src.Owner != requester {
		log.LogWarn("anon-svc: UpdateCollectionPermissions denied hash=%s owner=%s requester=%q", hash, src.Owner, requester)
		return "", fmt.Errorf("collection not found")
	}

	dst := *src
	dst.PrevSHA = hash
	if visibility != "" {
		dst.Visibility = visibility
	}
	if accessList != nil {
		dst.AccessList = accessList
	}
	if accessPolicy != "" {
		dst.AccessPolicy = accessPolicy
	}
	if psk != "" {
		if len(psk) == 64 && isValidHash(psk) {
			dst.PSKHash = psk
		} else {
			dst.PSKHash = sha256Hex([]byte(psk))
		}
		dst.IsProtected = true
	}
	if dst.Owner == "" && requester != "" {
		dst.Owner = requester
	}
	return s.saveCollectionJSON(&dst)
}
