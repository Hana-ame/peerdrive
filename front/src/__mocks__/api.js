// Mock API module — prevents real HTTP requests during tests.
// All async functions return promises that resolve to empty data.
// This avoids happy-dom Fetch creating Node HTTP requests that
// cause AbortError / socket hang up noise during window teardown.

const EMPTY_PROMISE = Promise.resolve({})
const EMPTY_ARRAY_PROMISE = Promise.resolve([])

// Request wrapper
async function request() { return {} }

// file
export const verifyFile = () => EMPTY_PROMISE
export const getDownloadUrl = () => ''
export const registerLocalFile = () => EMPTY_PROMISE
export const registerURL = () => EMPTY_PROMISE
export const registerFolder = () => EMPTY_PROMISE
export const browseDir = () => EMPTY_ARRAY_PROMISE

// anon collections
export const createAnonCollection = () => EMPTY_PROMISE
export const getAnonCollection = () => EMPTY_PROMISE
export const getAnonFileDownloadUrl = () => ''
export const forkAnonCollection = () => EMPTY_PROMISE

// user collections
export const createUserCollection = () => EMPTY_PROMISE
export const getUserCollections = () => EMPTY_ARRAY_PROMISE
export const getUserCollection = () => EMPTY_PROMISE
export const updateCollectionTags = () => EMPTY_PROMISE
export const addCollectionEntry = () => EMPTY_PROMISE
export const removeCollectionEntry = () => EMPTY_PROMISE
export const commitCollection = () => EMPTY_PROMISE
export const getVersionLog = () => EMPTY_ARRAY_PROMISE
export const rollbackVersion = () => EMPTY_PROMISE
export const forkUserCollection = () => EMPTY_PROMISE
export const mergeUserCollection = () => EMPTY_PROMISE
export const pullUserCollection = () => EMPTY_PROMISE
export const getUserFileDownloadUrl = () => ''

// P2P
export const getP2PStatus = () => EMPTY_PROMISE
export const getP2PNode = () => EMPTY_PROMISE
export const getP2PPeers = () => EMPTY_ARRAY_PROMISE
export const getP2PDiscovered = () => EMPTY_ARRAY_PROMISE
export const pingPeer = () => EMPTY_PROMISE
export const connectPeer = () => EMPTY_PROMISE
export const p2pAnnounce = () => EMPTY_PROMISE
export const p2pFetch = () => EMPTY_PROMISE
export const p2pSync = () => EMPTY_PROMISE
export const p2pPush = () => EMPTY_PROMISE
export const p2pRequestFile = () => EMPTY_PROMISE
export const getWSInfo = () => EMPTY_PROMISE
export const getSignalPeers = () => EMPTY_ARRAY_PROMISE
export const getP2PTopology = () => EMPTY_ARRAY_PROMISE
export const getP2PQuality = () => EMPTY_PROMISE
export const getPeersDetail = () => EMPTY_ARRAY_PROMISE
export const getPeerDetail = () => EMPTY_PROMISE
export const getP2PStats = () => EMPTY_PROMISE
export const getConnections = () => EMPTY_ARRAY_PROMISE

// BT
export const getBTStatus = () => EMPTY_PROMISE
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
export const btGetStats = () => EMPTY_PROMISE

// Dual
export const dualAnnounce = () => EMPTY_PROMISE
export const dualFind = () => EMPTY_PROMISE

// Anon commit
export const commitAnonCollection = () => EMPTY_PROMISE
export const listAnonCollections = () => EMPTY_ARRAY_PROMISE

// Search
export const searchCollections = () => EMPTY_ARRAY_PROMISE
export const listPublicCollections = () => EMPTY_ARRAY_PROMISE
export const setCollectionVisibility = () => EMPTY_PROMISE

// File upload/delete
export const uploadFile = () => EMPTY_PROMISE
export const deleteFile = () => EMPTY_PROMISE

// Tasks & health
export const getTasks = () => EMPTY_ARRAY_PROMISE
export const getTaskStatus = () => EMPTY_PROMISE
export const ping = () => EMPTY_PROMISE

// Settings
export const getApiBase = () => ''
export const setApiBase = () => {}
export const getAuthToken = () => ''
export const DEFAULT_API = 'http://localhost:3000'
export const getApiBaseUrl = () => ''
export const WS_TRANSFER_URL_BASE = 'http://localhost:3000'
export const WS_TRANSFER_URL = 'ws://localhost:3000/ws/transfer'

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

// P2P network config
export const getBootstrapPeer = () => ''
export const setBootstrapPeer = () => {}
export const getRelayServer = () => ''
export const setRelayServer = () => {}
export const getStunUrl = () => ''
export const setStunUrl = () => {}
export const getTurnUrl = () => ''
export const setTurnUrl = () => {}
export const getTurnCredential = () => ''
export const setTurnCredential = () => {}

// IPFS
export const getIPFSEnabled = () => false
export const setIPFSEnabled = () => {}
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
export const getRegServerUrl = () => ''
export const setRegServerUrl = () => {}

// Groups (reg server)
export const getUserGroups = () => EMPTY_ARRAY_PROMISE
export const addUserToGroup = () => EMPTY_PROMISE

// Comments
export const getComments = () => EMPTY_ARRAY_PROMISE
export const postComment = () => EMPTY_PROMISE

// Stats
export const getRegServerStats = () => EMPTY_PROMISE
export const getServiceStats = () => EMPTY_PROMISE

// Consent
export const uploadConsent = () => {}

// Alias
export const createCollection = createUserCollection
export const listCollections = getUserCollections
export const getCollection = getUserCollection
export const addEntry = addCollectionEntry
export const deleteEntry = removeCollectionEntry
export const commitVersion = commitCollection
export const downloadFileByPath = getUserFileDownloadUrl
export const mergeCollection = mergeUserCollection
export const getNodeInfo = getP2PNode

export const getBEP51Sample = () => EMPTY_ARRAY_PROMISE
