import { createContext as e, createElement as t, useContext as n, useEffect as r, useRef as i, useState as a } from "react";
import * as o from "peerjs";
import s from "peerjs";
import { jsx as c } from "react/jsx-runtime";
//#region src/react/context.js
var l = e({});
function u({ peer: e, signaling: n, children: r }) {
	return t(l.Provider, { value: {
		peer: e,
		signaling: n
	} }, r);
}
function d() {
	return n(l);
}
function f(e, t) {
	return JSON.stringify({
		type: "url",
		url: e,
		reqId: t,
		v: 1
	});
}
function p(e) {
	try {
		let t = JSON.parse(e);
		if (typeof t == "object" && t && typeof t.type == "string") return t;
	} catch {}
	return null;
}
function m(e) {
	return typeof e != "string" && !!(e instanceof ArrayBuffer || ArrayBuffer.isView(e) || typeof Blob < "u" && e instanceof Blob);
}
function h(e) {
	if (e instanceof Uint8Array) return e;
	if (e instanceof ArrayBuffer) return new Uint8Array(e);
	if (ArrayBuffer.isView(e)) return new Uint8Array(e.buffer, e.byteOffset, e.byteLength);
	throw Error("peerdrive-media: unsupported binary frame type");
}
function g() {
	return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
//#endregion
//#region src/core.js
var _ = o.Peer || s.Peer || s.default && s.default.Peer, v = {
	host: "0.peerjs.com",
	port: 443,
	secure: !0,
	key: "peerjs",
	path: "/"
}, y = 5e3;
function b(e) {
	return `${e.host}:${e.port}:${e.key}:${e.path || "/"}`;
}
var x = class {
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
		let i = g(), a, o = !1;
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
			a.send(f(e, i));
		} catch (e) {
			this.pending.delete(i), s.cleanup(), n(e);
		}
		else {
			let t = () => {
				try {
					a.send(f(e, i));
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
	open() {
		let e = this.signaling, t = typeof window < "u" && window.__PDM_DEBUG ? 3 : 0, n = new _(`pd-b-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`, {
			host: e.host,
			port: e.port,
			secure: e.secure,
			key: e.key,
			path: e.path || "/",
			config: e.config || { iceServers: [] },
			debug: t
		});
		this.peer = n;
		let r = setTimeout(() => {
			!this.ready && !this.closed && this.failAll("peerjs signaling timeout");
		}, 15e3);
		n.on("error", (e) => {
			clearTimeout(r), !this.ready && !this.closed && this.failAll(`peerjs error: ${e?.type || e}`);
		}), n.on("open", () => {
			let e = n.connect(this.peerId, {
				reliable: !0,
				serialization: "raw",
				label: "control"
			});
			this.controlConn = e, e.on("open", () => {
				if (clearTimeout(r), this.opening = !1, this.ready = !0, this.startKeepalive(), this._waitingForReady && this._waitingForReady.length) {
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
		this.lastActive = Date.now(), typeof e != "string" || p(e)?.type;
	}
	handleFileData(e, t) {
		this.lastActive = Date.now();
		let n = this.pending.get(e);
		if (!n) return;
		if (m(t)) {
			let e = h(t);
			n.chunks.push(e), n.got += e.length;
			return;
		}
		let r = p(t);
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
		this.lastActive = Date.now(), this.kaTimer = setInterval(() => {
			if (this.closed) {
				clearInterval(this.kaTimer);
				return;
			}
			if (Date.now() - this.lastActive > 15e3) {
				clearInterval(this.kaTimer), this.teardown("keepalive timeout");
				return;
			}
			try {
				this.controlConn && this.controlConn.send(JSON.stringify({ type: "ping" }));
			} catch {}
		}, y);
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
}, S = new class {
	constructor() {
		this.slots = /* @__PURE__ */ new Map();
	}
	async load(e, { peer: t, signaling: n = v, signal: r } = {}) {
		if (!t) throw Error("peerdrive-media: peer (node peer id) is required");
		if (!e || typeof e != "string") throw Error("peerdrive-media: url is required");
		let i = `${b(n)}|${t}`, a = this.slots.get(i);
		if ((!a || a.closed) && (a = new x(t, n), this.slots.set(i, a)), r?.aborted) throw new DOMException("aborted", "AbortError");
		return new Promise((t, n) => {
			a.request(e, t, n, r);
		});
	}
	dispose(e, t = v) {
		let n = `${b(t)}|${e}`, r = this.slots.get(n);
		r && (r.failAll("disposed"), this.slots.delete(n));
	}
}();
//#endregion
//#region src/react/usePeerMedia.js
function C({ url: e, peer: t, signaling: n } = {}) {
	let o = d(), s = t || o.peer, c = n || o.signaling, [l, u] = a({
		status: "idle",
		src: null,
		mime: null,
		error: null
	}), [f, p] = a(0), m = i(null);
	return r(() => {
		if (!s || !e) {
			u({
				status: "idle",
				src: null,
				mime: null,
				error: null
			});
			return;
		}
		let t = new AbortController();
		return u({
			status: "loading",
			src: null,
			mime: null,
			error: null
		}), S.load(e, {
			peer: s,
			signaling: c,
			signal: t.signal
		}).then((e) => {
			if (t.signal.aborted) {
				URL.revokeObjectURL(e.blobUrl);
				return;
			}
			m.current = e.blobUrl, u({
				status: "ready",
				src: e.blobUrl,
				mime: e.mime,
				error: null
			});
		}).catch((e) => {
			e.name !== "AbortError" && u({
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
		...l,
		reload: () => p((e) => e + 1)
	};
}
//#endregion
//#region src/react/components.jsx
function w(e) {
	return typeof e == "string" && e.startsWith("image/");
}
function T(e) {
	return typeof e == "string" && e.startsWith("video/");
}
function E(e, { loading: t, error: n }) {
	return e.status === "loading" ? t ?? /* @__PURE__ */ c("span", {
		className: "pm-loading",
		children: "loading…"
	}) : e.status === "error" ? n ?? /* @__PURE__ */ c("span", {
		className: "pm-error",
		children: String(e.error?.message || e.error)
	}) : null;
}
function D({ url: e, peer: t, signaling: n, alt: r = "", loading: i, error: a, ...o }) {
	let s = C({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? /* @__PURE__ */ c("img", {
		src: s.src,
		alt: r,
		...o
	}) : E(s, {
		loading: i,
		error: a
	});
}
function O({ url: e, peer: t, signaling: n, controls: r = !0, loading: i, error: a, ...o }) {
	let s = C({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? /* @__PURE__ */ c("video", {
		src: s.src,
		controls: r,
		...o
	}) : E(s, {
		loading: i,
		error: a
	});
}
function k({ url: e, peer: t, signaling: n, loading: r, error: i, imgProps: a = {}, videoProps: o = {} }) {
	let s = C({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? w(s.mime) ? /* @__PURE__ */ c("img", {
		src: s.src,
		alt: "",
		...a
	}) : T(s.mime) ? /* @__PURE__ */ c("video", {
		src: s.src,
		controls: !0,
		...o
	}) : /* @__PURE__ */ c("a", {
		href: s.src,
		download: !0,
		target: "_blank",
		rel: "noreferrer",
		children: e
	}) : E(s, {
		loading: r,
		error: i
	});
}
//#endregion
export { v as DEFAULT_SIGNALING, D as PeerImage, k as PeerMedia, l as PeerMediaContext, u as PeerMediaProvider, O as PeerVideo, S as defaultClient, C as usePeerMedia };
