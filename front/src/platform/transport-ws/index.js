// index.js — transport-ws module entry point.
//
// Provides two operational planes over the local WebSocket session (/ws/peer):
// 1. Admin plane (admin / upload / stat): Admin verbs forwarded to the backend gin engine.
// 2. Data plane (download / downloadStream / downloadToFile): Content-addressed chunked file streaming.
// 3. Connection lifecycle (getStatus / onStatus): Live session state machine.

export { getStatus, onStatus } from './status.js'
export {
  admin,
  upload,
  stat,
  download,
  downloadStream,
  downloadToFile,
  __test,
} from './client.js'
