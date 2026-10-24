import { createContext as e, createElement as t, useContext as n, useEffect as r, useRef as i, useState as a } from "react";
import { jsx as o } from "react/jsx-runtime";
//#region src/react/context.js
var s = e({});
function c({ peer: e, signaling: n, children: r }) {
	return t(s.Provider, { value: {
		peer: e,
		signaling: n
	} }, r);
}
function l() {
	return n(s);
}
//#endregion
//#region ../peerdrive-core/src/security.js
var u = 8388608;
function d(e, { maxBytes: t = u, throwOnError: n = !1 } = {}) {
	let r = (e) => {
		if (n) throw Error(e);
		return null;
	};
	if (typeof e != "string") return r("safeJsonParse: non-string input");
	if (e.length > t) return r(`safeJsonParse: document exceeds ${t} bytes`);
	let i;
	try {
		i = JSON.parse(e);
	} catch (e) {
		return r(`safeJsonParse: invalid JSON (${e.message})`);
	}
	let a = 0, o = (e, t) => {
		if (t > 64) throw Error("safeJsonParse: nesting too deep");
		if (a += 1, a > 2e4) throw Error("safeJsonParse: too many nodes");
		if (Array.isArray(e)) {
			for (let n of e) o(n, t + 1);
			return;
		}
		if (e && typeof e == "object") for (let n of Object.keys(e)) {
			if (n === "__proto__" || n === "constructor" || n === "prototype") throw Error(`safeJsonParse: dangerous key "${n}"`);
			o(e[n], t + 1);
		}
	};
	try {
		o(i, 0);
	} catch (e) {
		return r(e.message);
	}
	return i;
}
var f = 0;
function p() {
	return f = f + 1 & 1048575, `${Date.now().toString(36)}-${f.toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
function m(e, t) {
	return JSON.stringify({
		type: "url",
		url: e,
		reqId: t,
		v: 1
	});
}
var h = {
	"file.req": "req",
	"file.meta": "meta",
	"file.data": "data",
	"file.done": "done",
	"file.err": "err",
	"share.req": "share",
	"share.resp": "share-resp",
	"auth.psk": "psk-auth",
	"auth.psk_ok": "psk-ok",
	"auth.psk_err": "psk-err",
	"admin.request": "admin",
	"admin.response": "admin-resp",
	"admin.binary": "admin-bin",
	"tunnel.open": "fwd-open",
	"tunnel.auth": "fwd-auth",
	"tunnel.data": "fwd-data",
	"tunnel.close": "fwd-close"
};
function g(e) {
	if (typeof e != "string") return null;
	let t = d(e, {
		maxBytes: u,
		throwOnError: !1
	});
	if (!t || typeof t != "object" || Array.isArray(t)) return null;
	if (typeof t.type == "string") return t;
	if (typeof t.event == "string") {
		let e = h[t.event] || t.event.replace(/\./g, "-"), n = {
			...t.data && typeof t.data == "object" && !Array.isArray(t.data) ? t.data : {},
			...t,
			type: e
		};
		return t.req_id && !n.reqId && (n.reqId = t.req_id), n;
	}
	return null;
}
function _(e) {
	return typeof e != "string" && !!(typeof ArrayBuffer < "u" && e instanceof ArrayBuffer || typeof ArrayBuffer < "u" && ArrayBuffer.isView(e) || typeof Blob < "u" && e instanceof Blob);
}
function v(e) {
	if (e instanceof Uint8Array) return e;
	if (typeof ArrayBuffer < "u" && e instanceof ArrayBuffer) return new Uint8Array(e);
	if (typeof ArrayBuffer < "u" && ArrayBuffer.isView(e)) return new Uint8Array(e.buffer, e.byteOffset, e.byteLength);
	throw TypeError("peerdrive-core: unsupported binary frame type");
}
//#endregion
//#region src/core.js
var y = globalThis.Peer || null, b = null;
function x() {
	return b ||= (async () => {
		try {
			let e = await import("peerjs"), t = e.default || e;
			y = e.Peer || t.Peer || t.default && t.default.Peer || globalThis.Peer || null;
		} catch {
			y = globalThis.Peer || null;
		}
		return y;
	})(), b;
}
var S = {
	host: "0.peerjs.com",
	port: 443,
	secure: !0,
	key: "peerjs",
	path: "/"
}, C = 5e3;
function w(e) {
	return `${e.host}:${e.port}:${e.key}:${e.path || "/"}`;
}
var T = class {
	constructor(e, t) {
		this.peerId = e, this.signaling = t, this.peer = null, this.controlConn = null, this.ready = !1, this.closed = !1, this.opening = !1, this.pending = /* @__PURE__ */ new Map(), this.lastActive = 0, this.kaTimer = null, this.pool = [], this.inUse = /* @__PURE__ */ new Set(), this.poolSize = 2;
	}
	request(e, t, n, r) {
		if (this.closed) {
			n(/* @__PURE__ */ Error("peerdrive-media: connection closed"));
			return;
		}
		if (!this.ready) {
			this.opening || (this.opening = !0, this.open()), this._waitingForReady = this._waitingForReady || [];
			let i = {
				url: e,
				resolve: t,
				reject: n,
				signal: r
			};
			if (this._waitingForReady.push(i), r) {
				if (r.aborted) {
					let e = this._waitingForReady.findIndex((e) => e === i);
					e >= 0 && this._waitingForReady.splice(e, 1), n(new DOMException("aborted", "AbortError"));
					return;
				}
				let e = () => {
					if (this._waitingForReady) {
						let e = this._waitingForReady.findIndex((e) => e === i);
						e >= 0 && (this._waitingForReady.splice(e, 1), n(new DOMException("aborted", "AbortError")));
					}
					r.removeEventListener("abort", e);
				};
				i._onAbort = e, r.addEventListener("abort", e);
			}
			return;
		}
		this.sendFileRequest(e, t, n, r);
	}
	sendFileRequest(e, t, n, r) {
		let i = p(), a, o = !1;
		this.pool.length > 0 ? (a = this.pool.shift(), o = !0) : a = this.peer.connect(this.peerId, {
			reliable: !0,
			serialization: "raw",
			label: `file-${i}`
		}), this.inUse.add(a);
		let s = {
			resolve: t,
			reject: n,
			chunks: [],
			mime: null,
			size: 0,
			got: 0,
			conn: a,
			_listeners: {},
			cleanup: () => {
				this.inUse.delete(a), s._listeners.data && a.removeListener("data", s._listeners.data), s._listeners.close && a.removeListener("close", s._listeners.close), s._listeners.error && a.removeListener("error", s._listeners.error), s._listeners.open && a.removeListener("open", s._listeners.open), a.closed || this.pool.push(a);
			}
		};
		if (r) {
			if (r.aborted) {
				a.close(), n(new DOMException("aborted", "AbortError"));
				return;
			}
			let e = () => {
				this.pending.delete(i), s.cleanup();
				try {
					a.close();
				} catch {}
				n(new DOMException("aborted", "AbortError"));
			};
			r.addEventListener("abort", e), s._onAbort = e;
		}
		if (this.pending.set(i, s), o) try {
			a.send(m(e, i));
		} catch (e) {
			this.pending.delete(i), s.cleanup(), n(e);
		}
		else {
			let t = () => {
				try {
					a.send(m(e, i));
				} catch (e) {
					this.pending.delete(i), s.cleanup(), n(e);
				}
			};
			a.on("open", t), s._listeners.open = t;
		}
		let c = (e) => this.handleFileData(i, e);
		a.on("data", c), s._listeners.data = c;
		let l = () => {
			let e = this.pending.get(i);
			e && (this.pending.delete(i), e.cleanup(), e.reject(/* @__PURE__ */ Error("peerdrive-media: file channel closed")));
		};
		a.on("close", l), s._listeners.close = l;
		let u = (e) => {
			let t = this.pending.get(i);
			t && (this.pending.delete(i), t.cleanup(), t.reject(/* @__PURE__ */ Error(`peerdrive-media: file channel error: ${e?.type || e}`)));
		};
		a.on("error", u), s._listeners.error = u;
	}
	async open() {
		let e = this.signaling, t = typeof window < "u" && window.__PDM_DEBUG ? 3 : 0, n = globalThis.Peer || await x();
		if (!n) {
			this.failAll("peerjs 不可用：等 window.Peer 就绪，或确认 dynamic import 成功");
			return;
		}
		let r = new n(`pd-b-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`, {
			host: e.host,
			port: e.port,
			secure: e.secure,
			key: e.key,
			path: e.path || "/",
			config: e.config || { iceServers: [] },
			debug: t
		});
		this.peer = r;
		let i = setTimeout(() => {
			!this.ready && !this.closed && this.failAll("peerjs signaling timeout");
		}, 15e3);
		i.unref?.(), r.on("error", (e) => {
			clearTimeout(i), !this.ready && !this.closed && this.failAll(`peerjs error: ${e?.type || e}`);
		}), r.on("open", () => {
			let e = r.connect(this.peerId, {
				reliable: !0,
				serialization: "raw",
				label: "control"
			});
			this.controlConn = e, e.on("open", () => {
				if (clearTimeout(i), this.opening = !1, this.ready = !0, this.startKeepalive(), this._waitingForReady && this._waitingForReady.length) {
					let e = this._waitingForReady;
					this._waitingForReady = null;
					for (let t of e) t.signal && t.signal.removeEventListener("abort", t._onAbort), this.sendFileRequest(t.url, t.resolve, t.reject, t.signal);
				}
				setTimeout(() => this.warmUp(), 0);
			}), e.on("data", (e) => this.handleControlData(e)), e.on("close", () => this.teardown("control channel closed")), e.on("error", (e) => {
				!this.ready && !this.closed && this.failAll(`control channel error: ${e?.type || e}`);
			});
		});
	}
	handleControlData(e) {
		this.lastActive = Date.now(), this._kaTicks = 0, typeof e != "string" || g(e)?.type;
	}
	handleFileData(e, t) {
		this.lastActive = Date.now(), this._kaTicks = 0;
		let n = this.pending.get(e);
		if (!n) return;
		if (_(t)) {
			let e = v(t);
			n.chunks.push(e), n.got += e.length;
			return;
		}
		let r = g(t);
		if (r) switch (r.type) {
			case "meta":
				n.mime = r.mime || "application/octet-stream", n.size = r.size || 0, r.status >= 400 && (this.pending.delete(e), n.cleanup(), n.reject(/* @__PURE__ */ Error(`peerdrive-media: upstream ${r.status}`)));
				break;
			case "done":
				this.pending.delete(e);
				let t = new Blob(n.chunks, { type: n.mime });
				n.cleanup(), n.resolve({
					blob: t,
					blobUrl: URL.createObjectURL(t),
					mime: n.mime,
					size: n.got
				});
				break;
			case "err":
				this.pending.delete(e), n.cleanup(), n.reject(/* @__PURE__ */ Error(`peerdrive-media: ${r.msg || "request failed"}`));
				break;
			case "ping": try {
				n.conn.send(JSON.stringify({ type: "ping-ack" }));
			} catch {}
		}
	}
	startKeepalive() {
		this.lastActive = Date.now(), this._kaTicks = 0, this.kaTimer = setInterval(() => {
			if (this.closed) {
				clearInterval(this.kaTimer);
				return;
			}
			if (this._kaTicks = (this._kaTicks || 0) + 1, Date.now() - this.lastActive > 15e3 || this._kaTicks * 5e3 >= 15e3) {
				clearInterval(this.kaTimer), this.teardown("keepalive timeout");
				return;
			}
			try {
				this.controlConn && this.controlConn.send(JSON.stringify({ type: "ping" }));
			} catch {}
		}, C), this.kaTimer.unref?.();
	}
	warmUp() {
		let e = [];
		for (let t = 0; t < this.poolSize; t++) e.push(new Promise((e) => {
			let n = this.peer.connect(this.peerId, {
				reliable: !0,
				serialization: "raw",
				label: `file-pool-${Date.now()}-${t}`
			});
			n.on("open", () => {
				this.pool.push(n), e();
			}), n.on("close", () => e()), n.on("error", () => e());
		}));
		Promise.all(e).then(() => {});
	}
	failAll(e) {
		this.closed = !0, this.opening = !1, this.kaTimer &&= (clearInterval(this.kaTimer), null);
		let t = /* @__PURE__ */ Error(`peerdrive-media: ${e}`);
		for (let [, e] of this.pending) {
			e.cleanup();
			try {
				e.conn?.close();
			} catch {}
			e.reject(t);
		}
		this.pending.clear();
		for (let e of this.pool) try {
			e.close();
		} catch {}
		if (this.pool = [], this.inUse = /* @__PURE__ */ new Set(), this._waitingForReady) {
			for (let e of this._waitingForReady) e.reject(t);
			this._waitingForReady = null;
		}
		this.closePeer();
	}
	teardown(e) {
		this.closed || this.failAll(e);
	}
	closePeer() {
		try {
			this.peer?.destroy();
		} catch {}
		this.peer = null, this.controlConn = null;
	}
}, E = new class {
	constructor() {
		this.slots = /* @__PURE__ */ new Map();
	}
	async load(e, { peer: t, signaling: n = S, signal: r } = {}) {
		if (!t) throw Error("peerdrive-media: peer (node peer id) is required");
		if (!e || typeof e != "string") throw Error("peerdrive-media: url is required");
		let i = `${w(n)}|${t}`, a = this.slots.get(i);
		if ((!a || a.closed) && (a = new T(t, n), this.slots.set(i, a)), r?.aborted) throw new DOMException("aborted", "AbortError");
		return new Promise((t, n) => {
			a.request(e, t, n, r);
		});
	}
	dispose(e, t = S) {
		let n = `${w(t)}|${e}`, r = this.slots.get(n);
		r && (r.failAll("disposed"), this.slots.delete(n));
	}
}();
//#endregion
//#region src/react/usePeerMedia.js
function D({ url: e, peer: t, signaling: n } = {}) {
	let o = l(), s = t || o.peer, c = n || o.signaling, [u, d] = a({
		status: "idle",
		src: null,
		mime: null,
		error: null
	}), [f, p] = a(0), m = i(null);
	return r(() => {
		if (!s || !e) {
			d({
				status: "idle",
				src: null,
				mime: null,
				error: null
			});
			return;
		}
		let t = new AbortController();
		return d({
			status: "loading",
			src: null,
			mime: null,
			error: null
		}), E.load(e, {
			peer: s,
			signaling: c,
			signal: t.signal
		}).then((e) => {
			if (t.signal.aborted) {
				URL.revokeObjectURL(e.blobUrl);
				return;
			}
			m.current = e.blobUrl, d({
				status: "ready",
				src: e.blobUrl,
				mime: e.mime,
				error: null
			});
		}).catch((e) => {
			e.name !== "AbortError" && d({
				status: "error",
				src: null,
				mime: null,
				error: e
			});
		}), () => {
			t.abort(), m.current &&= (URL.revokeObjectURL(m.current), null);
		};
	}, [
		e,
		s,
		c,
		f
	]), {
		...u,
		reload: () => p((e) => e + 1)
	};
}
//#endregion
//#region src/react/components.jsx
function O(e) {
	return typeof e == "string" && e.startsWith("image/");
}
function k(e) {
	return typeof e == "string" && e.startsWith("video/");
}
function A(e, { loading: t, error: n }) {
	return e.status === "loading" ? t ?? /* @__PURE__ */ o("span", {
		className: "pm-loading",
		children: "loading…"
	}) : e.status === "error" ? n ?? /* @__PURE__ */ o("span", {
		className: "pm-error",
		children: String(e.error?.message || e.error)
	}) : null;
}
function j({ url: e, peer: t, signaling: n, alt: r = "", loading: i, error: a, ...s }) {
	let c = D({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? /* @__PURE__ */ o("img", {
		src: c.src,
		alt: r,
		...s
	}) : A(c, {
		loading: i,
		error: a
	});
}
function M({ url: e, peer: t, signaling: n, controls: r = !0, loading: i, error: a, ...s }) {
	let c = D({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? /* @__PURE__ */ o("video", {
		src: c.src,
		controls: r,
		...s
	}) : A(c, {
		loading: i,
		error: a
	});
}
function N({ url: e, peer: t, signaling: n, loading: r, error: i, imgProps: a = {}, videoProps: s = {} }) {
	let c = D({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? O(c.mime) ? /* @__PURE__ */ o("img", {
		src: c.src,
		alt: "",
		...a
	}) : k(c.mime) ? /* @__PURE__ */ o("video", {
		src: c.src,
		controls: !0,
		...s
	}) : /* @__PURE__ */ o("a", {
		href: c.src,
		download: !0,
		target: "_blank",
		rel: "noreferrer",
		children: e
	}) : A(c, {
		loading: r,
		error: i
	});
}
//#endregion
export { S as DEFAULT_SIGNALING, j as PeerImage, N as PeerMedia, s as PeerMediaContext, c as PeerMediaProvider, M as PeerVideo, E as defaultClient, D as usePeerMedia };
