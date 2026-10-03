// Collection visibility level constants — must one-to-one correspond to the
// three strings in back/internal/model/anon.go on the backend.
//
// Why a separate file instead of putting them in api.js:
// Tests do `vi.mock('../src/api.js')` as a full automock (tests/setup.js, used
// to cut off real requests in component useEffect), non-function exports get
// replaced → writing `api.VISIBILITY.PUBLIC` in a component throws
// "Cannot read properties of undefined" at render time.
// Constants are pure data, so keeping them here lets the api layer reuse them
// without being affected by the mock.
export const VISIBILITY_PUBLIC = 'public';
export const VISIBILITY_RESTRICTED = 'restricted';
export const VISIBILITY_PRIVATE = 'private';

export const VISIBILITY = {
  PUBLIC: VISIBILITY_PUBLIC,
  RESTRICTED: VISIBILITY_RESTRICTED,
  PRIVATE: VISIBILITY_PRIVATE,
};

// English labels and icons for display in list/detail pages (kept consistent
// with the three options in VisibilityPicker)
export const VISIBILITY_META = {
  [VISIBILITY_PUBLIC]: { icon: '🌐', label: 'Public' },
  [VISIBILITY_RESTRICTED]: { icon: '👥', label: 'Authorized Only' },
  [VISIBILITY_PRIVATE]: { icon: '🔒', label: 'Self Only' },
};

// visibilityMeta fallback: for legacy collections missing the field, the backend
// has already backfilled 'public'; this adds another layer of defense against
// dirty data.
export function visibilityMeta(v) {
  return VISIBILITY_META[v] || VISIBILITY_META[VISIBILITY_PUBLIC];
}

export default VISIBILITY;
