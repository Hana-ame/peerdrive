import { createContext as e, createElement as t, useContext as n, useEffect as r, useRef as i, useState as a } from "react";
import o from "peerjs";
import { jsx as s } from "react/jsx-runtime";
//#region src/react/context.js
var c = e({});
function l({ peer: e, signaling: n, children: r }) {
	return t(c.Provider, { value: {
		peer: e,
		signaling: n
	} }, r);
}
function u() {
	return n(c);
}
function d(e, t) {
	return JSON.stringify({
		type: "url",
		url: e,
		reqId: t,
		v: 1
	});
}
function f(e) {
	try {
		let t = JSON.parse(e);
		if (typeof t == "object" && t && typeof t.type == "string") return t;
	} catch {}
	return null;
}
function p(e) {
	return typeof e != "string" && !!(e instanceof ArrayBuffer || ArrayBuffer.isView(e) || typeof Blob < "u" && e instanceof Blob);
}
function m(e) {
	if (e instanceof Uint8Array) return e;
	if (e instanceof ArrayBuffer) return new Uint8Array(e);
	if (ArrayBuffer.isView(e)) return new Uint8Array(e.buffer, e.byteOffset, e.byteLength);
	throw Error("peerdrive-media: unsupported binary frame type");
}
function h() {
	return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
//#endregion
//#region src/core.js
var { Peer: g } = o, _ = {
	host: "0.peerjs.com",
	port: 443,
	secure: !0,
	key: "peerjs",
	path: "/"
};
function v(e) {
	return `${e.host}:${e.port}:${e.key}:${e.path || "/"}`;
}
var y = class {
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
		if (!this.ready || this.closed) {
			this.queue.push(i), this.closed || this.open();
			return;
		}
		this.send(i);
	}
	open() {
		let e = this.signaling, t = new g({
			host: e.host,
			port: e.port,
			secure: e.secure,
			key: e.key,
			path: e.path,
			debug: 0
		});
		this.peer = t;
		let n = setTimeout(() => {
			!this.ready && !this.closed && this.failAll("peerjs signaling timeout");
		}, 15e3);
		t.on("error", (e) => {
			clearTimeout(n), !this.ready && !this.closed && this.failAll(`peerjs error: ${e?.type || e}`);
		}), t.on("open", () => {
			let e = t.connect(this.peerId, {
				reliable: !0,
				serialization: "raw"
			});
			this.conn = e, e.on("open", () => {
				clearTimeout(n), this.ready = !0;
				for (let e of this.queue.splice(0)) this.send(e);
			}), e.on("data", (e) => this.handleData(e)), e.on("close", () => this.teardown("connection closed")), e.on("error", (e) => {
				this.ready || this.failAll(`connection error: ${e?.type || e}`), this.teardown("connection error");
			});
		});
	}
	send(e) {
		if (this.closed) {
			e.reject(/* @__PURE__ */ Error("peerdrive-media: connection closed"));
			return;
		}
		let t = h(), n = {
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
			this.conn.send(d(e.url, t));
		} catch (n) {
			this.pending.delete(t), e.reject(n);
		}
	}
	handleData(e) {
		if (p(e)) {
			let t = this.curReqId ? this.pending.get(this.curReqId) : null;
			if (!t) return;
			let n = m(e);
			t.chunks.push(n), t.got += n.length;
			return;
		}
		let t = f(e);
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
				});
				break;
			case "err": this.pending.delete(t.reqId), this.curReqId === t.reqId && (this.curReqId = null), n.cleanup(), n.reject(/* @__PURE__ */ Error(`peerdrive-media: ${t.msg || "request failed"}`));
		}
	}
	failAll(e) {
		this.closed = !0;
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
}, b = new class {
	constructor() {
		this.slots = /* @__PURE__ */ new Map();
	}
	async load(e, { peer: t, signaling: n = _, signal: r } = {}) {
		if (!t) throw Error("peerdrive-media: peer (node peer id) is required");
		if (!e || typeof e != "string") throw Error("peerdrive-media: url is required");
		let i = `${v(n)}|${t}`, a = this.slots.get(i);
		if (a || (a = new y(t, n), this.slots.set(i, a)), r?.aborted) throw new DOMException("aborted", "AbortError");
		return new Promise((t, n) => {
			a.request(e, t, n, r);
		});
	}
	dispose(e, t = _) {
		let n = `${v(t)}|${e}`, r = this.slots.get(n);
		r && (r.failAll("disposed"), this.slots.delete(n));
	}
}();
//#endregion
//#region src/react/usePeerMedia.js
function x({ url: e, peer: t, signaling: n } = {}) {
	let o = u(), s = t || o.peer, c = n || o.signaling, [l, d] = a({
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
		}), b.load(e, {
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
		...l,
		reload: () => p((e) => e + 1)
	};
}
//#endregion
//#region src/react/components.jsx
function S(e) {
	return typeof e == "string" && e.startsWith("image/");
}
function C(e) {
	return typeof e == "string" && e.startsWith("video/");
}
function w(e, { loading: t, error: n }) {
	return e.status === "loading" ? t ?? /* @__PURE__ */ s("span", {
		className: "pm-loading",
		children: "loading…"
	}) : e.status === "error" ? n ?? /* @__PURE__ */ s("span", {
		className: "pm-error",
		children: String(e.error?.message || e.error)
	}) : null;
}
function T({ url: e, peer: t, signaling: n, alt: r = "", loading: i, error: a, ...o }) {
	let c = x({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? /* @__PURE__ */ s("img", {
		src: c.src,
		alt: r,
		...o
	}) : w(c, {
		loading: i,
		error: a
	});
}
function E({ url: e, peer: t, signaling: n, controls: r = !0, loading: i, error: a, ...o }) {
	let c = x({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? /* @__PURE__ */ s("video", {
		src: c.src,
		controls: r,
		...o
	}) : w(c, {
		loading: i,
		error: a
	});
}
function D({ url: e, peer: t, signaling: n, loading: r, error: i, imgProps: a = {}, videoProps: o = {} }) {
	let c = x({
		url: e,
		peer: t,
		signaling: n
	});
	return c.status === "ready" ? S(c.mime) ? /* @__PURE__ */ s("img", {
		src: c.src,
		alt: "",
		...a
	}) : C(c.mime) ? /* @__PURE__ */ s("video", {
		src: c.src,
		controls: !0,
		...o
	}) : /* @__PURE__ */ s("a", {
		href: c.src,
		download: !0,
		target: "_blank",
		rel: "noreferrer",
		children: e
	}) : w(c, {
		loading: r,
		error: i
	});
}
//#endregion
export { _ as DEFAULT_SIGNALING, T as PeerImage, D as PeerMedia, c as PeerMediaContext, l as PeerMediaProvider, E as PeerVideo, b as defaultClient, x as usePeerMedia };
