// Mock API module — prevents real HTTP requests during tests.
// All async functions return promises that resolve to empty data.
// This avoids happy-dom Fetch creating Node HTTP requests that
// cause AbortError / socket hang up noise during window teardown.
//
// This is a **hand-written** mock (not automock): forgetting to sync a new
// api export means pages in tests will get undefined, then die at a call site
// far from the actual cause. tests/api-mock-sync.test.js does a bidirectional
// guard using the real module's export list (missing/extra both fail), so run
// it after changing api.js to know what to update.

const EMPTY_PROMISE = Promise.resolve({})
const EMPTY_ARRAY_PROMISE = Promise.resolve([])

// Request wrapper
async function request() { return {} }

// file
export const verifyFile = () => EMPTY_PROMISE
export const registerLocalFile = () => EMPTY_PROMISE
export const registerURL = () => EMPTY_PROMISE
export const registerFolder = () => EMPTY_PROMISE
export const browseDir = () => EMPTY_ARRAY_PROMISE

// anon collections
export const createAnonCollection = () => EMPTY_PROMISE
export const getAnonCollection = () => EMPTY_PROMISE
// Visibility three levels: constants forwarded directly from the real definition
// (components read values, not functions)
export { VISIBILITY, VISIBILITY_PUBLIC, VISIBILITY_RESTRICTED, VISIBILITY_PRIVATE } from '../constants.js'
export const setAnonCollectionVisibility = () => EMPTY_PROMISE
export const listKnownAccounts = () => EMPTY_ARRAY_PROMISE
export const listKnownGroups = () => EMPTY_ARRAY_PROMISE

// user collections
export const createUserCollection = () => EMPTY_PROMISE
export const getUserCollections = () => EMPTY_ARRAY_PROMISE
export const getUserCollection = () => EMPTY_PROMISE
export const addCollectionEntry = () => EMPTY_PROMISE
export const removeCollectionEntry = () => EMPTY_PROMISE
export const commitCollection = () => EMPTY_PROMISE
export const getVersionLog = () => EMPTY_ARRAY_PROMISE
export const rollbackVersion = () => EMPTY_PROMISE
export const forkUserCollection = () => EMPTY_PROMISE
export const mergeUserCollection = () => EMPTY_PROMISE

// BT
export const getBTStatus = () => EMPTY_PROMISE
export const getPeerjsNode = () => EMPTY_PROMISE

// Netdisk links (backend endpoints for M1-M3, UI in src/pages/{Drive,Market,Peers,PeerDetail,Transfers})
export const getNodeMarket = () => EMPTY_PROMISE
export const getJoinedNodes = () => EMPTY_PROMISE
export const joinNode = () => EMPTY_PROMISE
export const leaveNode = () => EMPTY_PROMISE
export const getPeerShares = () => EMPTY_PROMISE
// This node's share scope (M2.6)
export const getShareScope = () => EMPTY_PROMISE
export const setShareScope = () => EMPTY_PROMISE
export const setFilesShared = () => EMPTY_PROMISE
export const getPullJobs = () => EMPTY_PROMISE
export const startPull = () => EMPTY_PROMISE
export const startPullCollection = () => EMPTY_PROMISE
export const cancelPull = () => EMPTY_PROMISE
export const btAnnounce = () => EMPTY_PROMISE
export const btFind = () => EMPTY_PROMISE
export const btGetDownloads = () => EMPTY_ARRAY_PROMISE
export const btGetDownload = () => EMPTY_PROMISE
export const btMagnetResolve = () => EMPTY_PROMISE
export const btTorrentUpload = () => EMPTY_PROMISE
export const btRemoveDownload = () => EMPTY_PROMISE
export const btPauseDownload = () => EMPTY_PROMISE
export const btResumeDownload = () => EMPTY_PROMISE
export const btSeedDownload = () => EMPTY_PROMISE
export const btStopSeed = () => EMPTY_PROMISE
export const btSeedCollection = () => EMPTY_PROMISE

// Anon commit
export const listAnonCollections = () => EMPTY_ARRAY_PROMISE

// Search
export const searchCollections = () => EMPTY_ARRAY_PROMISE
export const listPublicCollections = () => EMPTY_ARRAY_PROMISE

// File upload/delete
export const uploadFile = () => EMPTY_PROMISE
export const deleteFile = () => EMPTY_PROMISE

// Health
export const ping = () => EMPTY_PROMISE

// Settings
export const getApiBase = () => ''
export const setApiBase = () => {}
export const getAuthToken = () => ''
export const DEFAULT_API = 'http://localhost:3000'

// Backend switching (mocks)
export const DEFAULT_BACKENDS = [
  { id: 'wsl', name: 'WSL', url: 'https://wsl-3000.moonchan.xyz' },
  { id: 'bwh', name: 'BWH', url: 'http://97.64.30.221:3000' },
]
export const getBackends = () => DEFAULT_BACKENDS
export const setBackends = () => {}
export const getCurrentBackendId = () => 'wsl'
export const setCurrentBackendId = () => {}
export const switchBackend = () => true
export const addBackend = () => 'mock-id'
export const removeBackend = () => true
export const updateBackend = () => true
export const getBackendField = () => ''
export const updateBackendField = () => true

// LLM
export const getLlmEndpoint = () => ''
export const setLlmEndpoint = () => {}
export const getLlmModel = () => ''
export const setLlmModel = () => {}
export const getLlmApiKey = () => ''
export const setLlmApiKey = () => {}
export const getLlmBodyTemplate = () => '{}'
export const setLlmBodyTemplate = () => {}
export const getDataConsent = () => false
export const setDataConsent = () => {}
export const DEFAULT_LLM_ENDPOINT = ''
export const DEFAULT_LLM_MODEL = ''
export const DEFAULT_LLM_BODY = '{}'
export const FREE_LLM_MODELS = []

// Auth header toggle
export const getAuthHeaderEnabled = () => false
export const setAuthHeaderEnabled = () => {}

// Follow redirects
export const getFollowRedirects = () => true
export const setFollowRedirects = () => {}

// P2P network config (deleted, see src/api.js migration notes)

// IPFS
export const getIPFSEnabled = () => false
export const getIPFSCompatStatus = () => EMPTY_PROMISE
export const setIPFSCompatEnabled = () => EMPTY_PROMISE
export const pinCID = () => EMPTY_PROMISE
export const unpinCID = () => EMPTY_PROMISE
export const listPins = () => EMPTY_ARRAY_PROMISE
export const getIPFSGatewayStatus = () => EMPTY_ARRAY_PROMISE

// Access list
export const createAccessList = () => EMPTY_PROMISE
export const getAccessList = () => EMPTY_PROMISE

// Regserver proxy
export const listRegUsers = () => EMPTY_ARRAY_PROMISE
export const listRegGroups = () => EMPTY_ARRAY_PROMISE
export const getGroupMembers = () => EMPTY_ARRAY_PROMISE

// Local sync
export const saveLocal = () => EMPTY_PROMISE
export const getLocalStatus = () => EMPTY_PROMISE

// Files
export const listFiles = () => EMPTY_ARRAY_PROMISE

// Auth
export const getAuthStatus = () => EMPTY_PROMISE
export const setRegServerUrl = () => {}

// Groups (reg server)
export const getUserGroups = () => EMPTY_ARRAY_PROMISE
export const addUserToGroup = () => EMPTY_PROMISE

// Comments
export const getComments = () => EMPTY_ARRAY_PROMISE
export const postComment = () => EMPTY_PROMISE

// Consent
export const saveConsentLocal = () => {}

// WS download/preview (post-migration new APIs, all mocked as empty Promises —
// tests do not trigger real WS)
export const downloadFile = () => Promise.resolve(new Uint8Array(0))
export const downloadFileToDisk = () => Promise.resolve()
export const downloadAnonFile = () => Promise.resolve(new Uint8Array(0))
export const downloadUserFile = () => Promise.resolve(new Uint8Array(0))
export const downloadTorrentFile = () => Promise.resolve(new Uint8Array(0))
export const getBlobUrl = () => ''
export const revokeBlobUrl = () => {}
export const btGetMagnetUri = () => EMPTY_PROMISE

// Alias
export const createCollection = createUserCollection
export const listCollections = getUserCollections
export const getCollection = getUserCollection
export const addEntry = addCollectionEntry
export const deleteEntry = removeCollectionEntry
export const commitVersion = commitCollection
export const downloadFileByPath = downloadUserFile
export const mergeCollection = mergeUserCollection

export const getBEP51Sample = () => EMPTY_ARRAY_PROMISE