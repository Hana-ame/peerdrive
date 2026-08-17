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
};
function y(e) {
	return `${e.host}:${e.port}:${e.key}:${e.path || "/"}`;
}
var b = class {
	constructor(e, t) {
		this.peerId = e, this.signaling = t, this.peer = null, this.conn = null, this.ready = !1, this.closed = !1, this.queue = [], this.pending = /* @__PURE__ */ new Map(), this.curReqId = null;
	}
	request(e, t, n, r) {
		let i = {
			url: e,
			resolve: t,
			reject: n,
			signal: r
		};
		if (!this.ready || this.closed || this.pending.size > 0) {
			this.queue.push(i), !this.opening && !this.closed && (this.opening = !0, this.open());
			return;
		}
		this.send(i);
	}
	flush() {
		if (!(!this.ready || this.closed)) for (; this.queue.length && this.pending.size === 0;) this.send(this.queue.shift());
	}
	open() {
		let e = this.signaling, t = typeof window < "u" && window.__PDM_DEBUG ? 3 : 0, n = new _(`pd-b-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`, {
			host: e.host,
			port: e.port,
			secure: e.secure,
			key: e.key,
			path: e.path,
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
				serialization: "raw"
			});
			this.conn = e, e.on("open", () => {
				clearTimeout(r), this.opening = !1, this.ready = !0, this.flush();
			}), e.on("data", (e) => this.handleData(e)), e.on("close", () => this.teardown("connection closed")), e.on("error", (e) => {
				!this.ready && !this.closed && this.failAll(`connection error: ${e?.type || e}`);
			});
		});
	}
	send(e) {
		if (this.closed) {
			e.reject(/* @__PURE__ */ Error("peerdrive-media: connection closed"));
			return;
		}
		let t = g(), n = {
			resolve: e.resolve,
			reject: e.reject,
			chunks: [],
			mime: null,
			size: 0,
			got: 0,
			cleanup: () => {}
		};
		if (e.signal) {
			if (e.signal.aborted) {
				e.reject(new DOMException("aborted", "AbortError"));
				return;
			}
			let r = () => {
				this.pending.delete(t), e.reject(new DOMException("aborted", "AbortError"));
			};
			n.cleanup = () => e.signal.removeEventListener("abort", r), e.signal.addEventListener("abort", r);
		}
		this.pending.set(t, n);
		try {
			this.conn.send(f(e.url, t));
		} catch (n) {
			this.pending.delete(t), e.reject(n);
		}
	}
	handleData(e) {
		if (m(e)) {
			let t = this.curReqId ? this.pending.get(this.curReqId) : null;
			if (!t) return;
			let n = h(e);
			t.chunks.push(n), t.got += n.length;
			return;
		}
		let t = p(e);
		if (!t) return;
		let n = this.pending.get(t.reqId);
		if (n) switch (t.type) {
			case "meta":
				n.mime = t.mime || "application/octet-stream", n.size = t.size || 0, this.curReqId = t.reqId, t.status >= 400 && (this.pending.delete(t.reqId), this.curReqId === t.reqId && (this.curReqId = null), n.cleanup(), n.reject(/* @__PURE__ */ Error(`peerdrive-media: upstream ${t.status}`)));
				break;
			case "done":
				this.pending.delete(t.reqId), this.curReqId === t.reqId && (this.curReqId = null);
				let e = new Blob(n.chunks, { type: n.mime });
				n.cleanup(), n.resolve({
					blob: e,
					blobUrl: URL.createObjectURL(e),
					mime: n.mime,
					size: n.got
				}), this.flush();
				break;
			case "err": this.pending.delete(t.reqId), this.curReqId === t.reqId && (this.curReqId = null), n.cleanup(), n.reject(/* @__PURE__ */ Error(`peerdrive-media: ${t.msg || "request failed"}`)), this.flush();
		}
	}
	failAll(e) {
		this.closed = !0, this.opening = !1;
		let t = /* @__PURE__ */ Error(`peerdrive-media: ${e}`);
		for (let [, e] of this.pending) e.cleanup(), e.reject(t);
		this.pending.clear();
		for (let e of this.queue.splice(0)) e.reject(t);
		this.curReqId = null, this.closePeer();
	}
	teardown(e) {
		this.closed || this.failAll(e);
	}
	closePeer() {
		try {
			this.peer?.destroy();
		} catch {}
		this.peer = null, this.conn = null;
	}
}, x = new class {
	constructor() {
		this.slots = /* @__PURE__ */ new Map();
	}
	async load(e, { peer: t, signaling: n = v, signal: r } = {}) {
		if (!t) throw Error("peerdrive-media: peer (node peer id) is required");
		if (!e || typeof e != "string") throw Error("peerdrive-media: url is required");
		let i = `${y(n)}|${t}`, a = this.slots.get(i);
		if (a || (a = new b(t, n), this.slots.set(i, a)), r?.aborted) throw new DOMException("aborted", "AbortError");
		return new Promise((t, n) => {
			a.request(e, t, n, r);
		});
	}
	dispose(e, t = v) {
		let n = `${y(t)}|${e}`, r = this.slots.get(n);
		r && (r.failAll("disposed"), this.slots.delete(n));
	}
}();
//#endregion
//#region src/react/usePeerMedia.js
function S({ url: e, peer: t, signaling: n } = {}) {
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
		}), x.load(e, {
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
function C(e) {
	return typeof e == "string" && e.startsWith("image/");
}
function w(e) {
	return typeof e == "string" && e.startsWith("video/");
}
function T(e, { loading: t, error: n }) {
	return e.status === "loading" ? t ?? /* @__PURE__ */ c("span", {
		className: "pm-loading",
		children: "loading…"
	}) : e.status === "error" ? n ?? /* @__PURE__ */ c("span", {
		className: "pm-error",
		children: String(e.error?.message || e.error)
	}) : null;
}
function E({ url: e, peer: t, signaling: n, alt: r = "", loading: i, error: a, ...o }) {
	let s = S({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? /* @__PURE__ */ c("img", {
		src: s.src,
		alt: r,
		...o
	}) : T(s, {
		loading: i,
		error: a
	});
}
function D({ url: e, peer: t, signaling: n, controls: r = !0, loading: i, error: a, ...o }) {
	let s = S({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? /* @__PURE__ */ c("video", {
		src: s.src,
		controls: r,
		...o
	}) : T(s, {
		loading: i,
		error: a
	});
}
function O({ url: e, peer: t, signaling: n, loading: r, error: i, imgProps: a = {}, videoProps: o = {} }) {
	let s = S({
		url: e,
		peer: t,
		signaling: n
	});
	return s.status === "ready" ? C(s.mime) ? /* @__PURE__ */ c("img", {
		src: s.src,
		alt: "",
		...a
	}) : w(s.mime) ? /* @__PURE__ */ c("video", {
		src: s.src,
		controls: !0,
		...o
	}) : /* @__PURE__ */ c("a", {
		href: s.src,
		download: !0,
		target: "_blank",
		rel: "noreferrer",
		children: e
	}) : T(s, {
		loading: r,
		error: i
	});
}
//#endregion
export { v as DEFAULT_SIGNALING, E as PeerImage, O as PeerMedia, l as PeerMediaContext, u as PeerMediaProvider, D as PeerVideo, x as defaultClient, S as usePeerMedia };
