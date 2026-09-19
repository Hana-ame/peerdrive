// Mock API module — prevents real HTTP requests during tests.
// All async functions return promises that resolve to empty data.
// This avoids happy-dom Fetch creating Node HTTP requests that
// cause AbortError / socket hang up noise during window teardown.
//
// 这是**手写** mock（不是 automock）：新增 api 导出忘同步，页面在测试里会拿到
// undefined，然后死在离原因很远的调用点。tests/api-mock-sync.test.js 拿真实
// 模块的导出清单做双向守卫（缺了/多了都红），改 api.js 后跑一次就知道该改哪。

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
// 可见性三档：常量直接转发真实定义（组件读的是值，不是函数）
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

// 网盘链路（M1-M3 的后端端点，界面见 src/pages/{Drive,Market,Peers,PeerDetail,Transfers}）
export const getNodeMarket = () => EMPTY_PROMISE
export const getJoinedNodes = () => EMPTY_PROMISE
export const joinNode = () => EMPTY_PROMISE
export const leaveNode = () => EMPTY_PROMISE
export const getPeerShares = () => EMPTY_PROMISE
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

// P2P network config（已删，见 src/api.js 迁移记录）

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

// WS 下载/预览（迁移后的新 API，全部 mock 成空 Promise——测试不触发真实 WS）
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
