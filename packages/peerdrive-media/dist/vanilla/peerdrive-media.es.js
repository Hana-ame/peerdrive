//#region \0rolldown/runtime.js
var e = Object.create, t = Object.defineProperty, n = Object.getOwnPropertyDescriptor, r = Object.getOwnPropertyNames, i = Object.getPrototypeOf, a = Object.prototype.hasOwnProperty, o = (e, t, n) => () => {
	if (n) throw n[0];
	try {
		return e && (t = e(e = 0)), t;
	} catch (e) {
		throw n = [e], e;
	}
}, s = (e, t) => () => (t || (e((t = { exports: {} }).exports, t), e = null), t.exports), c = (e, n) => {
	let r = {};
	for (var i in e) t(r, i, {
		get: e[i],
		enumerable: !0
	});
	return n || t(r, Symbol.toStringTag, { value: "Module" }), r;
}, l = (e, i, o, s) => {
	if (i && typeof i == "object" || typeof i == "function") for (var c = r(i), l = 0, u = c.length, d; l < u; l++) d = c[l], !a.call(e, d) && d !== o && t(e, d, {
		get: ((e) => i[e]).bind(null, d),
		enumerable: !(s = n(i, d)) || s.enumerable
	});
	return e;
}, u = (n, r, o) => (o = n == null ? {} : e(i(n)), l(r || !n || !n.__esModule || !a.call(n, "default") ? t(o, "default", {
	value: n,
	enumerable: !0
}) : o, n)), d = 8388608;
function f(e, { maxBytes: t = d, throwOnError: n = !1 } = {}) {
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
var p = 0;
function ee() {
	return p = p + 1 & 1048575, `${Date.now().toString(36)}-${p.toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
function te(e, t) {
	return JSON.stringify({
		type: "url",
		url: e,
		reqId: t,
		v: 1
	});
}
var ne = {
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
function re(e) {
	if (typeof e != "string") return null;
	let t = f(e, {
		maxBytes: d,
		throwOnError: !1
	});
	if (!t || typeof t != "object" || Array.isArray(t)) return null;
	if (typeof t.type == "string") return t;
	if (typeof t.event == "string") {
		let e = ne[t.event] || t.event.replace(/\./g, "-"), n = {
			...t.data && typeof t.data == "object" && !Array.isArray(t.data) ? t.data : {},
			...t,
			type: e
		};
		return t.req_id && !n.reqId && (n.reqId = t.req_id), n;
	}
	return null;
}
function ie(e) {
	return typeof e != "string" && !!(typeof ArrayBuffer < "u" && e instanceof ArrayBuffer || typeof ArrayBuffer < "u" && ArrayBuffer.isView(e) || typeof Blob < "u" && e instanceof Blob);
}
function ae(e) {
	if (e instanceof Uint8Array) return e;
	if (typeof ArrayBuffer < "u" && e instanceof ArrayBuffer) return new Uint8Array(e);
	if (typeof ArrayBuffer < "u" && ArrayBuffer.isView(e)) return new Uint8Array(e.buffer, e.byteOffset, e.byteLength);
	throw TypeError("peerdrive-core: unsupported binary frame type");
}
//#endregion
//#region node_modules/peerjs-js-binarypack/dist/binarypack.mjs
function oe(e) {
	let t = 0;
	for (let n of e) t += n.byteLength;
	let n = new Uint8Array(t), r = 0;
	for (let t of e) {
		let e = new Uint8Array(t.buffer, t.byteOffset, t.byteLength);
		n.set(e, r), r += t.byteLength;
	}
	return n;
}
function se(e) {
	return new ue(e).unpack();
}
function ce(e) {
	let t = new de(), n = t.pack(e);
	return n instanceof Promise ? n.then(() => t.getBuffer()) : t.getBuffer();
}
var le, ue, de, fe = o((() => {
	le = class {
		constructor() {
			this.encoder = new TextEncoder(), this._pieces = [], this._parts = [];
		}
		append_buffer(e) {
			this.flush(), this._parts.push(e);
		}
		append(e) {
			this._pieces.push(e);
		}
		flush() {
			if (this._pieces.length > 0) {
				let e = new Uint8Array(this._pieces);
				this._parts.push(e), this._pieces = [];
			}
		}
		toArrayBuffer() {
			let e = [];
			for (let t of this._parts) e.push(t);
			return oe(e).buffer;
		}
	}, ue = class {
		constructor(e) {
			this.index = 0, this.dataBuffer = e, this.dataView = new Uint8Array(this.dataBuffer), this.length = this.dataBuffer.byteLength;
		}
		unpack() {
			let e = this.unpack_uint8();
			if (e < 128) return e;
			if ((e ^ 224) < 32) return (e ^ 224) - 32;
			let t;
			if ((t = e ^ 160) <= 15) return this.unpack_raw(t);
			if ((t = e ^ 176) <= 15) return this.unpack_string(t);
			if ((t = e ^ 144) <= 15) return this.unpack_array(t);
			if ((t = e ^ 128) <= 15) return this.unpack_map(t);
			switch (e) {
				case 192: return null;
				case 193: return;
				case 194: return !1;
				case 195: return !0;
				case 202: return this.unpack_float();
				case 203: return this.unpack_double();
				case 204: return this.unpack_uint8();
				case 205: return this.unpack_uint16();
				case 206: return this.unpack_uint32();
				case 207: return this.unpack_uint64();
				case 208: return this.unpack_int8();
				case 209: return this.unpack_int16();
				case 210: return this.unpack_int32();
				case 211: return this.unpack_int64();
				case 212: return;
				case 213: return;
				case 214: return;
				case 215: return;
				case 216: return t = this.unpack_uint16(), this.unpack_string(t);
				case 217: return t = this.unpack_uint32(), this.unpack_string(t);
				case 218: return t = this.unpack_uint16(), this.unpack_raw(t);
				case 219: return t = this.unpack_uint32(), this.unpack_raw(t);
				case 220: return t = this.unpack_uint16(), this.unpack_array(t);
				case 221: return t = this.unpack_uint32(), this.unpack_array(t);
				case 222: return t = this.unpack_uint16(), this.unpack_map(t);
				case 223: return t = this.unpack_uint32(), this.unpack_map(t);
			}
		}
		unpack_uint8() {
			let e = this.dataView[this.index] & 255;
			return this.index++, e;
		}
		unpack_uint16() {
			let e = this.read(2), t = (e[0] & 255) * 256 + (e[1] & 255);
			return this.index += 2, t;
		}
		unpack_uint32() {
			let e = this.read(4), t = ((e[0] * 256 + e[1]) * 256 + e[2]) * 256 + e[3];
			return this.index += 4, t;
		}
		unpack_uint64() {
			let e = this.read(8), t = ((((((e[0] * 256 + e[1]) * 256 + e[2]) * 256 + e[3]) * 256 + e[4]) * 256 + e[5]) * 256 + e[6]) * 256 + e[7];
			return this.index += 8, t;
		}
		unpack_int8() {
			let e = this.unpack_uint8();
			return e < 128 ? e : e - 256;
		}
		unpack_int16() {
			let e = this.unpack_uint16();
			return e < 32768 ? e : e - 65536;
		}
		unpack_int32() {
			let e = this.unpack_uint32();
			return e < 2 ** 31 ? e : e - 2 ** 32;
		}
		unpack_int64() {
			let e = this.unpack_uint64();
			return e < 2 ** 63 ? e : e - 2 ** 64;
		}
		unpack_raw(e) {
			if (this.length < this.index + e) throw Error(`BinaryPackFailure: index is out of range ${this.index} ${e} ${this.length}`);
			let t = this.dataBuffer.slice(this.index, this.index + e);
			return this.index += e, t;
		}
		unpack_string(e) {
			let t = this.read(e), n = 0, r = "", i, a;
			for (; n < e;) i = t[n], i < 160 ? (a = i, n++) : (i ^ 192) < 32 ? (a = (i & 31) << 6 | t[n + 1] & 63, n += 2) : (i ^ 224) < 16 ? (a = (i & 15) << 12 | (t[n + 1] & 63) << 6 | t[n + 2] & 63, n += 3) : (a = (i & 7) << 18 | (t[n + 1] & 63) << 12 | (t[n + 2] & 63) << 6 | t[n + 3] & 63, n += 4), r += String.fromCodePoint(a);
			return this.index += e, r;
		}
		unpack_array(e) {
			let t = Array(e);
			for (let n = 0; n < e; n++) t[n] = this.unpack();
			return t;
		}
		unpack_map(e) {
			let t = {};
			for (let n = 0; n < e; n++) {
				let e = this.unpack();
				t[e] = this.unpack();
			}
			return t;
		}
		unpack_float() {
			let e = this.unpack_uint32(), t = e >> 31, n = (e >> 23 & 255) - 127, r = e & 8388607 | 8388608;
			return (t === 0 ? 1 : -1) * r * 2 ** (n - 23);
		}
		unpack_double() {
			let e = this.unpack_uint32(), t = this.unpack_uint32(), n = e >> 31, r = (e >> 20 & 2047) - 1023, i = (e & 1048575 | 1048576) * 2 ** (r - 20) + t * 2 ** (r - 52);
			return (n === 0 ? 1 : -1) * i;
		}
		read(e) {
			let t = this.index;
			if (t + e <= this.length) return this.dataView.subarray(t, t + e);
			throw Error("BinaryPackFailure: read index out of range");
		}
	}, de = class {
		getBuffer() {
			return this._bufferBuilder.toArrayBuffer();
		}
		pack(e) {
			if (typeof e == "string") this.pack_string(e);
			else if (typeof e == "number") Math.floor(e) === e ? this.pack_integer(e) : this.pack_double(e);
			else if (typeof e == "boolean") e === !0 ? this._bufferBuilder.append(195) : e === !1 && this._bufferBuilder.append(194);
			else if (e === void 0) this._bufferBuilder.append(192);
			else if (typeof e == "object") {
				if (e === null) this._bufferBuilder.append(192);
				else {
					let t = e.constructor;
					if (e instanceof Array) {
						let t = this.pack_array(e);
						if (t instanceof Promise) return t.then(() => this._bufferBuilder.flush());
					} else if (e instanceof ArrayBuffer) this.pack_bin(new Uint8Array(e));
					else if ("BYTES_PER_ELEMENT" in e) {
						let t = e;
						this.pack_bin(new Uint8Array(t.buffer, t.byteOffset, t.byteLength));
					} else if (e instanceof Date) this.pack_string(e.toString());
					else if (e instanceof Blob) return e.arrayBuffer().then((e) => {
						this.pack_bin(new Uint8Array(e)), this._bufferBuilder.flush();
					});
					else if (t == Object || t.toString().startsWith("class")) {
						let t = this.pack_object(e);
						if (t instanceof Promise) return t.then(() => this._bufferBuilder.flush());
					} else throw Error(`Type "${t.toString()}" not yet supported`);
				}
			} else throw Error(`Type "${typeof e}" not yet supported`);
			this._bufferBuilder.flush();
		}
		pack_bin(e) {
			let t = e.length;
			if (t <= 15) this.pack_uint8(160 + t);
			else if (t <= 65535) this._bufferBuilder.append(218), this.pack_uint16(t);
			else if (t <= 4294967295) this._bufferBuilder.append(219), this.pack_uint32(t);
			else throw Error("Invalid length");
			this._bufferBuilder.append_buffer(e);
		}
		pack_string(e) {
			let t = this._textEncoder.encode(e), n = t.length;
			if (n <= 15) this.pack_uint8(176 + n);
			else if (n <= 65535) this._bufferBuilder.append(216), this.pack_uint16(n);
			else if (n <= 4294967295) this._bufferBuilder.append(217), this.pack_uint32(n);
			else throw Error("Invalid length");
			this._bufferBuilder.append_buffer(t);
		}
		pack_array(e) {
			let t = e.length;
			if (t <= 15) this.pack_uint8(144 + t);
			else if (t <= 65535) this._bufferBuilder.append(220), this.pack_uint16(t);
			else if (t <= 4294967295) this._bufferBuilder.append(221), this.pack_uint32(t);
			else throw Error("Invalid length");
			let n = (r) => {
				if (r < t) {
					let t = this.pack(e[r]);
					return t instanceof Promise ? t.then(() => n(r + 1)) : n(r + 1);
				}
			};
			return n(0);
		}
		pack_integer(e) {
			if (e >= -32 && e <= 127) this._bufferBuilder.append(e & 255);
			else if (e >= 0 && e <= 255) this._bufferBuilder.append(204), this.pack_uint8(e);
			else if (e >= -128 && e <= 127) this._bufferBuilder.append(208), this.pack_int8(e);
			else if (e >= 0 && e <= 65535) this._bufferBuilder.append(205), this.pack_uint16(e);
			else if (e >= -32768 && e <= 32767) this._bufferBuilder.append(209), this.pack_int16(e);
			else if (e >= 0 && e <= 4294967295) this._bufferBuilder.append(206), this.pack_uint32(e);
			else if (e >= -2147483648 && e <= 2147483647) this._bufferBuilder.append(210), this.pack_int32(e);
			else if (e >= -0x8000000000000000 && e <= 0x8000000000000000) this._bufferBuilder.append(211), this.pack_int64(e);
			else if (e >= 0 && e <= 0x10000000000000000) this._bufferBuilder.append(207), this.pack_uint64(e);
			else throw Error("Invalid integer");
		}
		pack_double(e) {
			let t = 0;
			e < 0 && (t = 1, e = -e);
			let n = Math.floor(Math.log(e) / Math.LN2), r = e / 2 ** n - 1, i = Math.floor(r * 2 ** 52), a = 2 ** 32, o = t << 31 | n + 1023 << 20 | i / a & 1048575, s = i % a;
			this._bufferBuilder.append(203), this.pack_int32(o), this.pack_int32(s);
		}
		pack_object(e) {
			let t = Object.keys(e), n = t.length;
			if (n <= 15) this.pack_uint8(128 + n);
			else if (n <= 65535) this._bufferBuilder.append(222), this.pack_uint16(n);
			else if (n <= 4294967295) this._bufferBuilder.append(223), this.pack_uint32(n);
			else throw Error("Invalid length");
			let r = (n) => {
				if (n < t.length) {
					let i = t[n];
					if (e.hasOwnProperty(i)) {
						this.pack(i);
						let t = this.pack(e[i]);
						if (t instanceof Promise) return t.then(() => r(n + 1));
					}
					return r(n + 1);
				}
			};
			return r(0);
		}
		pack_uint8(e) {
			this._bufferBuilder.append(e);
		}
		pack_uint16(e) {
			this._bufferBuilder.append(e >> 8), this._bufferBuilder.append(e & 255);
		}
		pack_uint32(e) {
			let t = e & 4294967295;
			this._bufferBuilder.append((t & 4278190080) >>> 24), this._bufferBuilder.append((t & 16711680) >>> 16), this._bufferBuilder.append((t & 65280) >>> 8), this._bufferBuilder.append(t & 255);
		}
		pack_uint64(e) {
			let t = e / 2 ** 32, n = e % 2 ** 32;
			this._bufferBuilder.append((t & 4278190080) >>> 24), this._bufferBuilder.append((t & 16711680) >>> 16), this._bufferBuilder.append((t & 65280) >>> 8), this._bufferBuilder.append(t & 255), this._bufferBuilder.append((n & 4278190080) >>> 24), this._bufferBuilder.append((n & 16711680) >>> 16), this._bufferBuilder.append((n & 65280) >>> 8), this._bufferBuilder.append(n & 255);
		}
		pack_int8(e) {
			this._bufferBuilder.append(e & 255);
		}
		pack_int16(e) {
			this._bufferBuilder.append((e & 65280) >> 8), this._bufferBuilder.append(e & 255);
		}
		pack_int32(e) {
			this._bufferBuilder.append(e >>> 24 & 255), this._bufferBuilder.append((e & 16711680) >>> 16), this._bufferBuilder.append((e & 65280) >>> 8), this._bufferBuilder.append(e & 255);
		}
		pack_int64(e) {
			let t = Math.floor(e / 2 ** 32), n = e % 2 ** 32;
			this._bufferBuilder.append((t & 4278190080) >>> 24), this._bufferBuilder.append((t & 16711680) >>> 16), this._bufferBuilder.append((t & 65280) >>> 8), this._bufferBuilder.append(t & 255), this._bufferBuilder.append((n & 4278190080) >>> 24), this._bufferBuilder.append((n & 16711680) >>> 16), this._bufferBuilder.append((n & 65280) >>> 8), this._bufferBuilder.append(n & 255);
		}
		constructor() {
			this._bufferBuilder = new le(), this._textEncoder = new TextEncoder();
		}
	};
}));
//#endregion
//#region node_modules/webrtc-adapter/src/js/utils.js
function m(e, t, n) {
	let r = e.match(t);
	return r && r.length >= n && parseFloat(r[n], 10);
}
function h(e, t, n) {
	if (!e.RTCPeerConnection) return;
	if (!Object.getOwnPropertyDescriptor(EventTarget.prototype, "addEventListener").writable) {
		he("Unable to polyfill events");
		return;
	}
	let r = e.RTCPeerConnection.prototype, i = r.addEventListener;
	r.addEventListener = function(e, r) {
		if (e !== t) return i.apply(this, arguments);
		let a = (e) => {
			let t = n(e);
			t && (r.handleEvent ? r.handleEvent(t) : r(t));
		};
		return this._eventMap = this._eventMap || {}, this._eventMap[t] || (this._eventMap[t] = /* @__PURE__ */ new Map()), this._eventMap[t].set(r, a), i.apply(this, [e, a]);
	};
	let a = r.removeEventListener;
	r.removeEventListener = function(e, n) {
		if (e !== t || !this._eventMap || !this._eventMap[t] || !this._eventMap[t].has(n)) return a.apply(this, arguments);
		let r = this._eventMap[t].get(n);
		return this._eventMap[t].delete(n), this._eventMap[t].size === 0 && delete this._eventMap[t], Object.keys(this._eventMap).length === 0 && delete this._eventMap, a.apply(this, [e, r]);
	}, Object.defineProperty(r, "on" + t, {
		get() {
			return this["_on" + t];
		},
		set(e) {
			this["_on" + t] && (this.removeEventListener(t, this["_on" + t]), delete this["_on" + t]), e && this.addEventListener(t, this["_on" + t] = e);
		},
		enumerable: !0,
		configurable: !0
	});
}
function pe(e) {
	return typeof e == "boolean" ? (Se = e, e ? "adapter.js logging disabled" : "adapter.js logging enabled") : /* @__PURE__ */ Error("Argument type: " + typeof e + ". Please use a boolean.");
}
function me(e) {
	return typeof e == "boolean" ? (Ce = !e, "adapter.js deprecation warnings " + (e ? "disabled" : "enabled")) : /* @__PURE__ */ Error("Argument type: " + typeof e + ". Please use a boolean.");
}
function he() {
	if (typeof window == "object") {
		if (Se) return;
		typeof console < "u" && typeof console.log == "function" && console.log.apply(console, arguments);
	}
}
function ge(e, t) {
	Ce && console.warn(e + " is deprecated, please use " + t + " instead.");
}
function _e(e) {
	let t = {
		browser: null,
		version: null
	};
	if (e === void 0 || !e.navigator || !e.navigator.userAgent) return t.browser = "Not a browser.", t;
	let { navigator: n } = e;
	if (n.userAgentData && n.userAgentData.brands) {
		let e = n.userAgentData.brands.find((e) => e.brand === "Chromium");
		if (e) {
			let t = parseInt(e.version, 10);
			if (t >= 90) return {
				browser: "chrome",
				version: t
			};
		}
	}
	if (n.mozGetUserMedia) t.browser = "firefox", t.version = parseInt(m(n.userAgent, /Firefox\/(\d+)\./, 1));
	else if (n.webkitGetUserMedia || e.isSecureContext === !1 && e.webkitRTCPeerConnection) t.browser = "chrome", t.version = parseInt(m(n.userAgent, /Chrom(e|ium)\/(\d+)\./, 2)) || null;
	else if (e.RTCPeerConnection && n.userAgent.match(/AppleWebKit\/(\d+)\./)) t.browser = "safari", t.version = parseInt(m(n.userAgent, /AppleWebKit\/(\d+)\./, 1)), t.supportsUnifiedPlan = e.RTCRtpTransceiver && "currentDirection" in e.RTCRtpTransceiver.prototype, t._safariVersion = m(n.userAgent, /Version\/(\d+(\.?\d+))/, 1);
	else return t.browser = "Not a supported browser.", t;
	return t;
}
function ve(e) {
	return Object.prototype.toString.call(e) === "[object Object]";
}
function ye(e) {
	return ve(e) ? Object.keys(e).reduce(function(t, n) {
		let r = ve(e[n]), i = r ? ye(e[n]) : e[n], a = r && !Object.keys(i).length;
		return i === void 0 || a ? t : Object.assign(t, { [n]: i });
	}, {}) : e;
}
function be(e, t, n) {
	!t || n.has(t.id) || (n.set(t.id, t), Object.keys(t).forEach((r) => {
		r.endsWith("Id") ? be(e, e.get(t[r]), n) : r.endsWith("Ids") && t[r].forEach((t) => {
			be(e, e.get(t), n);
		});
	}));
}
function xe(e, t, n) {
	let r = n ? "outbound-rtp" : "inbound-rtp", i = /* @__PURE__ */ new Map();
	if (t === null) return i;
	let a = [];
	return e.forEach((e) => {
		e.type === "track" && e.trackIdentifier === t.id && a.push(e);
	}), a.forEach((t) => {
		e.forEach((n) => {
			n.type === r && n.trackId === t.id && be(e, n, i);
		});
	}), i;
}
var Se, Ce, g = o((() => {
	Se = !0, Ce = !0;
}));
//#endregion
//#region node_modules/webrtc-adapter/src/js/chrome/getusermedia.js
function we(e, t) {
	if (t.version >= 64) return;
	let n = e && e.navigator;
	if (!n.mediaDevices) return;
	let r = function(e) {
		if (typeof e != "object" || e.mandatory || e.optional) return e;
		let t = {};
		return Object.keys(e).forEach((n) => {
			if (n === "require" || n === "advanced" || n === "mediaSource") return;
			let r = typeof e[n] == "object" ? e[n] : { ideal: e[n] };
			r.exact !== void 0 && typeof r.exact == "number" && (r.min = r.max = r.exact);
			let i = function(e, t) {
				return e ? e + t.charAt(0).toUpperCase() + t.slice(1) : t === "deviceId" ? "sourceId" : t;
			};
			if (r.ideal !== void 0) {
				t.optional = t.optional || [];
				let e = {};
				typeof r.ideal == "number" ? (e[i("min", n)] = r.ideal, t.optional.push(e), e = {}, e[i("max", n)] = r.ideal, t.optional.push(e)) : (e[i("", n)] = r.ideal, t.optional.push(e));
			}
			r.exact !== void 0 && typeof r.exact != "number" ? (t.mandatory = t.mandatory || {}, t.mandatory[i("", n)] = r.exact) : ["min", "max"].forEach((e) => {
				r[e] !== void 0 && (t.mandatory = t.mandatory || {}, t.mandatory[i(e, n)] = r[e]);
			});
		}), e.advanced && (t.optional = (t.optional || []).concat(e.advanced)), t;
	}, i = function(e, i) {
		if (t.version >= 61) return i(e);
		if (e = JSON.parse(JSON.stringify(e)), e && typeof e.audio == "object") {
			let t = function(e, t, n) {
				t in e && !(n in e) && (e[n] = e[t], delete e[t]);
			};
			e = JSON.parse(JSON.stringify(e)), t(e.audio, "autoGainControl", "googAutoGainControl"), t(e.audio, "noiseSuppression", "googNoiseSuppression"), e.audio = r(e.audio);
		}
		if (e && typeof e.video == "object") {
			let a = e.video.facingMode;
			a &&= typeof a == "object" ? a : { ideal: a };
			let o = t.version < 66;
			if (a && (a.exact === "user" || a.exact === "environment" || a.ideal === "user" || a.ideal === "environment") && !(n.mediaDevices.getSupportedConstraints && n.mediaDevices.getSupportedConstraints().facingMode && !o)) {
				delete e.video.facingMode;
				let t;
				if (a.exact === "environment" || a.ideal === "environment" ? t = ["back", "rear"] : (a.exact === "user" || a.ideal === "user") && (t = ["front"]), t) return n.mediaDevices.enumerateDevices().then((n) => {
					n = n.filter((e) => e.kind === "videoinput");
					let o = n.find((e) => t.some((t) => e.label.toLowerCase().includes(t)));
					return !o && n.length && t.includes("back") && (o = n[n.length - 1]), o && (e.video.deviceId = a.exact ? { exact: o.deviceId } : { ideal: o.deviceId }), e.video = r(e.video), Te("chrome: " + JSON.stringify(e)), i(e);
				});
			}
			e.video = r(e.video);
		}
		return Te("chrome: " + JSON.stringify(e)), i(e);
	}, a = function(e) {
		return t.version >= 64 ? e : {
			name: {
				PermissionDeniedError: "NotAllowedError",
				PermissionDismissedError: "NotAllowedError",
				InvalidStateError: "NotAllowedError",
				DevicesNotFoundError: "NotFoundError",
				ConstraintNotSatisfiedError: "OverconstrainedError",
				TrackStartError: "NotReadableError",
				MediaDeviceFailedDueToShutdown: "NotAllowedError",
				MediaDeviceKillSwitchOn: "NotAllowedError",
				TabCaptureError: "AbortError",
				ScreenCaptureError: "AbortError",
				DeviceCaptureError: "AbortError"
			}[e.name] || e.name,
			message: e.message,
			constraint: e.constraint || e.constraintName,
			toString() {
				return this.name + (this.message && ": ") + this.message;
			}
		};
	};
	if (n.getUserMedia = function(e, t, r) {
		i(e, (e) => {
			n.webkitGetUserMedia(e, t, (e) => {
				r && r(a(e));
			});
		});
	}.bind(n), n.mediaDevices.getUserMedia) {
		let e = n.mediaDevices.getUserMedia.bind(n.mediaDevices);
		n.mediaDevices.getUserMedia = function(t) {
			return i(t, (t) => e(t).then((e) => {
				if (t.audio && !e.getAudioTracks().length || t.video && !e.getVideoTracks().length) throw e.getTracks().forEach((e) => {
					e.stop();
				}), new DOMException("", "NotFoundError");
				return e;
			}, (e) => Promise.reject(a(e))));
		};
	}
}
var Te, Ee = o((() => {
	g(), Te = he;
})), De = /* @__PURE__ */ c({
	fixNegotiationNeeded: () => Fe,
	shimAddTrackRemoveTrack: () => Ne,
	shimAddTrackRemoveTrackWithNative: () => Me,
	shimGetSendersWithDtmf: () => Ae,
	shimGetUserMedia: () => we,
	shimMediaStream: () => Oe,
	shimOnTrack: () => ke,
	shimPeerConnection: () => Pe,
	shimSenderReceiverGetStats: () => je
});
function Oe(e) {
	e.MediaStream = e.MediaStream || e.webkitMediaStream;
}
function ke(e, t) {
	if (!(t.version > 102)) {
		if (typeof e == "object" && e.RTCPeerConnection && !("ontrack" in e.RTCPeerConnection.prototype)) {
			Object.defineProperty(e.RTCPeerConnection.prototype, "ontrack", {
				get() {
					return this._ontrack;
				},
				set(e) {
					this._ontrack && this.removeEventListener("track", this._ontrack), this.addEventListener("track", this._ontrack = e);
				},
				enumerable: !0,
				configurable: !0
			});
			let t = e.RTCPeerConnection.prototype.setRemoteDescription;
			e.RTCPeerConnection.prototype.setRemoteDescription = function() {
				return this._ontrackpoly || (this._ontrackpoly = (t) => {
					t.stream.addEventListener("addtrack", (n) => {
						let r;
						r = e.RTCPeerConnection.prototype.getReceivers ? this.getReceivers().find((e) => e.track && e.track.id === n.track.id) : { track: n.track };
						let i = new Event("track");
						i.track = n.track, i.receiver = r, i.transceiver = { receiver: r }, i.streams = [t.stream], this.dispatchEvent(i);
					}), t.stream.getTracks().forEach((n) => {
						let r;
						r = e.RTCPeerConnection.prototype.getReceivers ? this.getReceivers().find((e) => e.track && e.track.id === n.id) : { track: n };
						let i = new Event("track");
						i.track = n, i.receiver = r, i.transceiver = { receiver: r }, i.streams = [t.stream], this.dispatchEvent(i);
					});
				}, this.addEventListener("addstream", this._ontrackpoly)), t.apply(this, arguments);
			};
		} else h(e, "track", (e) => (e.transceiver || Object.defineProperty(e, "transceiver", { value: { receiver: e.receiver } }), e));
	}
}
function Ae(e) {
	if (typeof e == "object" && e.RTCPeerConnection && !("getSenders" in e.RTCPeerConnection.prototype) && "createDTMFSender" in e.RTCPeerConnection.prototype) {
		let t = function(e, t) {
			return {
				track: t,
				get dtmf() {
					return this._dtmf === void 0 && (this._dtmf = t.kind === "audio" ? e.createDTMFSender(t) : null), this._dtmf;
				},
				_pc: e
			};
		};
		if (!e.RTCPeerConnection.prototype.getSenders) {
			e.RTCPeerConnection.prototype.getSenders = function() {
				return this._senders = this._senders || [], this._senders.slice();
			};
			let n = e.RTCPeerConnection.prototype.addTrack;
			e.RTCPeerConnection.prototype.addTrack = function(e, r) {
				let i = n.apply(this, arguments);
				return i || (i = t(this, e), this._senders.push(i)), i;
			};
			let r = e.RTCPeerConnection.prototype.removeTrack;
			e.RTCPeerConnection.prototype.removeTrack = function(e) {
				r.apply(this, arguments);
				let t = this._senders.indexOf(e);
				t !== -1 && this._senders.splice(t, 1);
			};
		}
		let n = e.RTCPeerConnection.prototype.addStream;
		e.RTCPeerConnection.prototype.addStream = function(e) {
			this._senders = this._senders || [], n.apply(this, [e]), e.getTracks().forEach((e) => {
				this._senders.push(t(this, e));
			});
		};
		let r = e.RTCPeerConnection.prototype.removeStream;
		e.RTCPeerConnection.prototype.removeStream = function(e) {
			this._senders = this._senders || [], r.apply(this, [e]), e.getTracks().forEach((e) => {
				let t = this._senders.find((t) => t.track === e);
				t && this._senders.splice(this._senders.indexOf(t), 1);
			});
		};
	} else if (typeof e == "object" && e.RTCPeerConnection && "getSenders" in e.RTCPeerConnection.prototype && "createDTMFSender" in e.RTCPeerConnection.prototype && e.RTCRtpSender && !("dtmf" in e.RTCRtpSender.prototype)) {
		let t = e.RTCPeerConnection.prototype.getSenders;
		e.RTCPeerConnection.prototype.getSenders = function() {
			let e = t.apply(this, []);
			return e.forEach((e) => e._pc = this), e;
		}, Object.defineProperty(e.RTCRtpSender.prototype, "dtmf", { get() {
			return this._dtmf === void 0 && (this._dtmf = this.track.kind === "audio" ? this._pc.createDTMFSender(this.track) : null), this._dtmf;
		} });
	}
}
function je(e, t) {
	if (t.version >= 67 || !(typeof e == "object" && e.RTCPeerConnection && e.RTCRtpSender && e.RTCRtpReceiver)) return;
	if (!("getStats" in e.RTCRtpSender.prototype)) {
		let t = e.RTCPeerConnection.prototype.getSenders;
		t && (e.RTCPeerConnection.prototype.getSenders = function() {
			let e = t.apply(this, []);
			return e.forEach((e) => e._pc = this), e;
		});
		let n = e.RTCPeerConnection.prototype.addTrack;
		n && (e.RTCPeerConnection.prototype.addTrack = function() {
			let e = n.apply(this, arguments);
			return e._pc = this, e;
		}), e.RTCRtpSender.prototype.getStats = function() {
			let e = this;
			return this._pc.getStats().then((t) => xe(t, e.track, !0));
		};
	}
	if (!("getStats" in e.RTCRtpReceiver.prototype)) {
		let t = e.RTCPeerConnection.prototype.getReceivers;
		t && (e.RTCPeerConnection.prototype.getReceivers = function() {
			let e = t.apply(this, []);
			return e.forEach((e) => e._pc = this), e;
		}), h(e, "track", (e) => (e.receiver._pc = e.srcElement, e)), e.RTCRtpReceiver.prototype.getStats = function() {
			let e = this;
			return this._pc.getStats().then((t) => xe(t, e.track, !1));
		};
	}
	if (!("getStats" in e.RTCRtpSender.prototype && "getStats" in e.RTCRtpReceiver.prototype)) return;
	let n = e.RTCPeerConnection.prototype.getStats;
	e.RTCPeerConnection.prototype.getStats = function() {
		if (arguments.length > 0 && arguments[0] instanceof e.MediaStreamTrack) {
			let e = arguments[0], t, n, r;
			return this.getSenders().forEach((n) => {
				n.track === e && (t ? r = !0 : t = n);
			}), this.getReceivers().forEach((t) => (t.track === e && (n ? r = !0 : n = t), t.track === e)), r || t && n ? Promise.reject(new DOMException("There are more than one sender or receiver for the track.", "InvalidAccessError")) : t ? t.getStats() : n ? n.getStats() : Promise.reject(new DOMException("There is no sender or receiver for the track.", "InvalidAccessError"));
		}
		return n.apply(this, arguments);
	};
}
function Me(e) {
	e.RTCPeerConnection.prototype.getLocalStreams = function() {
		return this._shimmedLocalStreams = this._shimmedLocalStreams || {}, Object.keys(this._shimmedLocalStreams).map((e) => this._shimmedLocalStreams[e][0]);
	};
	let t = e.RTCPeerConnection.prototype.addTrack;
	e.RTCPeerConnection.prototype.addTrack = function(e, n) {
		if (!n) return t.apply(this, arguments);
		this._shimmedLocalStreams = this._shimmedLocalStreams || {};
		let r = t.apply(this, arguments);
		return this._shimmedLocalStreams[n.id] ? this._shimmedLocalStreams[n.id].indexOf(r) === -1 && this._shimmedLocalStreams[n.id].push(r) : this._shimmedLocalStreams[n.id] = [n, r], r;
	};
	let n = e.RTCPeerConnection.prototype.addStream;
	e.RTCPeerConnection.prototype.addStream = function(e) {
		this._shimmedLocalStreams = this._shimmedLocalStreams || {}, e.getTracks().forEach((e) => {
			if (this.getSenders().find((t) => t.track === e)) throw new DOMException("Track already exists.", "InvalidAccessError");
		});
		let t = this.getSenders();
		n.apply(this, arguments);
		let r = this.getSenders().filter((e) => t.indexOf(e) === -1);
		this._shimmedLocalStreams[e.id] = [e].concat(r);
	};
	let r = e.RTCPeerConnection.prototype.removeStream;
	e.RTCPeerConnection.prototype.removeStream = function(e) {
		return this._shimmedLocalStreams = this._shimmedLocalStreams || {}, delete this._shimmedLocalStreams[e.id], r.apply(this, arguments);
	};
	let i = e.RTCPeerConnection.prototype.removeTrack;
	e.RTCPeerConnection.prototype.removeTrack = function(e) {
		return this._shimmedLocalStreams = this._shimmedLocalStreams || {}, e && Object.keys(this._shimmedLocalStreams).forEach((t) => {
			let n = this._shimmedLocalStreams[t].indexOf(e);
			n !== -1 && this._shimmedLocalStreams[t].splice(n, 1), this._shimmedLocalStreams[t].length === 1 && delete this._shimmedLocalStreams[t];
		}), i.apply(this, arguments);
	};
}
function Ne(e, t) {
	if (!e.RTCPeerConnection) return;
	if (e.RTCPeerConnection.prototype.addTrack && t.version >= 65) return Me(e);
	let n = e.RTCPeerConnection.prototype.getLocalStreams;
	e.RTCPeerConnection.prototype.getLocalStreams = function() {
		let e = n.apply(this);
		return this._reverseStreams = this._reverseStreams || {}, e.map((e) => this._reverseStreams[e.id]);
	};
	let r = e.RTCPeerConnection.prototype.addStream;
	e.RTCPeerConnection.prototype.addStream = function(t) {
		if (this._streams = this._streams || {}, this._reverseStreams = this._reverseStreams || {}, t.getTracks().forEach((e) => {
			if (this.getSenders().find((t) => t.track === e)) throw new DOMException("Track already exists.", "InvalidAccessError");
		}), !this._reverseStreams[t.id]) {
			let n = new e.MediaStream(t.getTracks());
			this._streams[t.id] = n, this._reverseStreams[n.id] = t, t = n;
		}
		r.apply(this, [t]);
	};
	let i = e.RTCPeerConnection.prototype.removeStream;
	e.RTCPeerConnection.prototype.removeStream = function(e) {
		this._streams = this._streams || {}, this._reverseStreams = this._reverseStreams || {}, i.apply(this, [this._streams[e.id] || e]), delete this._reverseStreams[this._streams[e.id] ? this._streams[e.id].id : e.id], delete this._streams[e.id];
	}, e.RTCPeerConnection.prototype.addTrack = function(t, n) {
		if (this.signalingState === "closed") throw new DOMException("The RTCPeerConnection's signalingState is 'closed'.", "InvalidStateError");
		let r = [].slice.call(arguments, 1);
		if (r.length !== 1 || !r[0].getTracks().find((e) => e === t)) throw new DOMException("The adapter.js addTrack polyfill only supports a single  stream which is associated with the specified track.", "NotSupportedError");
		if (this.getSenders().find((e) => e.track === t)) throw new DOMException("Track already exists.", "InvalidAccessError");
		this._streams = this._streams || {}, this._reverseStreams = this._reverseStreams || {};
		let i = this._streams[n.id];
		if (i) i.addTrack(t), Promise.resolve().then(() => {
			this.dispatchEvent(new Event("negotiationneeded"));
		});
		else {
			let r = new e.MediaStream([t]);
			this._streams[n.id] = r, this._reverseStreams[r.id] = n, this.addStream(r);
		}
		return this.getSenders().find((e) => e.track === t);
	};
	function a(e, t) {
		let n = t.sdp;
		return Object.keys(e._reverseStreams || []).forEach((t) => {
			let r = e._reverseStreams[t], i = e._streams[r.id];
			n = n.replace(new RegExp(i.id, "g"), r.id);
		}), new RTCSessionDescription({
			type: t.type,
			sdp: n
		});
	}
	function o(e, t) {
		let n = t.sdp;
		return Object.keys(e._reverseStreams || []).forEach((t) => {
			let r = e._reverseStreams[t], i = e._streams[r.id];
			n = n.replace(new RegExp(r.id, "g"), i.id);
		}), new RTCSessionDescription({
			type: t.type,
			sdp: n
		});
	}
	["createOffer", "createAnswer"].forEach(function(t) {
		let n = e.RTCPeerConnection.prototype[t], r = { [t]() {
			let e = arguments;
			return arguments.length && typeof arguments[0] == "function" ? n.apply(this, [
				(t) => {
					let n = a(this, t);
					e[0].apply(null, [n]);
				},
				(t) => {
					e[1] && e[1].apply(null, t);
				},
				arguments[2]
			]) : n.apply(this, arguments).then((e) => a(this, e));
		} };
		e.RTCPeerConnection.prototype[t] = r[t];
	});
	let s = e.RTCPeerConnection.prototype.setLocalDescription;
	e.RTCPeerConnection.prototype.setLocalDescription = function() {
		return !arguments.length || !arguments[0].type || (arguments[0] = o(this, arguments[0])), s.apply(this, arguments);
	};
	let c = Object.getOwnPropertyDescriptor(e.RTCPeerConnection.prototype, "localDescription");
	Object.defineProperty(e.RTCPeerConnection.prototype, "localDescription", { get() {
		let e = c.get.apply(this);
		return e.type === "" ? e : a(this, e);
	} }), e.RTCPeerConnection.prototype.removeTrack = function(e) {
		if (this.signalingState === "closed") throw new DOMException("The RTCPeerConnection's signalingState is 'closed'.", "InvalidStateError");
		if (!e._pc) throw new DOMException("Argument 1 of RTCPeerConnection.removeTrack does not implement interface RTCRtpSender.", "TypeError");
		if (e._pc !== this) throw new DOMException("Sender was not created by this connection.", "InvalidAccessError");
		this._streams = this._streams || {};
		let t;
		Object.keys(this._streams).forEach((n) => {
			this._streams[n].getTracks().find((t) => e.track === t) && (t = this._streams[n]);
		}), t && (t.getTracks().length === 1 ? this.removeStream(this._reverseStreams[t.id]) : t.removeTrack(e.track), this.dispatchEvent(new Event("negotiationneeded")));
	};
}
function Pe(e, t) {
	!e.RTCPeerConnection && e.webkitRTCPeerConnection && (e.RTCPeerConnection = e.webkitRTCPeerConnection), e.RTCPeerConnection && t.version < 53 && [
		"setLocalDescription",
		"setRemoteDescription",
		"addIceCandidate"
	].forEach(function(t) {
		let n = e.RTCPeerConnection.prototype[t], r = { [t]() {
			return arguments[0] = new (t === "addIceCandidate" ? e.RTCIceCandidate : e.RTCSessionDescription)(arguments[0]), n.apply(this, arguments);
		} };
		e.RTCPeerConnection.prototype[t] = r[t];
	});
}
function Fe(e, t) {
	t.version > 102 || h(e, "negotiationneeded", (e) => {
		let n = e.target;
		if (!((t.version < 72 || n.getConfiguration && n.getConfiguration().sdpSemantics === "plan-b") && n.signalingState !== "stable")) return e;
	});
}
var Ie = o((() => {
	g(), Ee();
}));
//#endregion
//#region node_modules/webrtc-adapter/src/js/firefox/getusermedia.js
function Le(e, t) {
	let n = e && e.navigator;
	if (!n.mediaDevices) return;
	let r = e && e.MediaStreamTrack;
	if (n.getUserMedia = function(e, t, r) {
		ge("navigator.getUserMedia", "navigator.mediaDevices.getUserMedia"), n.mediaDevices.getUserMedia(e).then(t, r);
	}, !(t.version > 55 && "autoGainControl" in n.mediaDevices.getSupportedConstraints())) {
		let e = function(e, t, n) {
			t in e && !(n in e) && (e[n] = e[t], delete e[t]);
		}, t = n.mediaDevices.getUserMedia.bind(n.mediaDevices);
		if (n.mediaDevices.getUserMedia = function(n) {
			return typeof n == "object" && typeof n.audio == "object" && (n = JSON.parse(JSON.stringify(n)), e(n.audio, "autoGainControl", "mozAutoGainControl"), e(n.audio, "noiseSuppression", "mozNoiseSuppression")), t(n);
		}, r && r.prototype.getSettings) {
			let t = r.prototype.getSettings;
			r.prototype.getSettings = function() {
				let n = t.apply(this, arguments);
				return e(n, "mozAutoGainControl", "autoGainControl"), e(n, "mozNoiseSuppression", "noiseSuppression"), n;
			};
		}
		if (r && r.prototype.applyConstraints) {
			let t = r.prototype.applyConstraints;
			r.prototype.applyConstraints = function(n) {
				return this.kind === "audio" && typeof n == "object" && (n = JSON.parse(JSON.stringify(n)), e(n, "autoGainControl", "mozAutoGainControl"), e(n, "noiseSuppression", "mozNoiseSuppression")), t.apply(this, [n]);
			};
		}
	}
}
var Re = o((() => {
	g();
}));
//#endregion
//#region node_modules/webrtc-adapter/src/js/firefox/getdisplaymedia.js
function ze(e, t) {
	e.navigator.mediaDevices && (e.navigator.mediaDevices && "getDisplayMedia" in e.navigator.mediaDevices || (e.navigator.mediaDevices.getDisplayMedia = function(n) {
		if (!(n && n.video)) {
			let e = new DOMException("getDisplayMedia without video constraints is undefined");
			return e.name = "NotFoundError", e.code = 8, Promise.reject(e);
		}
		return n.video === !0 ? n.video = { mediaSource: t } : n.video.mediaSource = t, e.navigator.mediaDevices.getUserMedia(n);
	}));
}
var Be = o((() => {})), Ve = /* @__PURE__ */ c({
	shimAddTransceiver: () => Ye,
	shimCreateAnswer: () => Qe,
	shimCreateOffer: () => Ze,
	shimGetDisplayMedia: () => ze,
	shimGetParameters: () => Xe,
	shimGetStats: () => We,
	shimGetUserMedia: () => Le,
	shimOnTrack: () => He,
	shimPeerConnection: () => Ue,
	shimRTCDataChannel: () => Je,
	shimReceiverGetStats: () => Ke,
	shimRemoveStream: () => qe,
	shimSenderGetStats: () => Ge
});
function He(e) {
	typeof e == "object" && e.RTCTrackEvent && "receiver" in e.RTCTrackEvent.prototype && !("transceiver" in e.RTCTrackEvent.prototype) && Object.defineProperty(e.RTCTrackEvent.prototype, "transceiver", { get() {
		return { receiver: this.receiver };
	} });
}
function Ue(e, t) {
	typeof e != "object" || !(e.RTCPeerConnection || e.mozRTCPeerConnection) || (!e.RTCPeerConnection && e.mozRTCPeerConnection && (e.RTCPeerConnection = e.mozRTCPeerConnection), t.version < 53 && [
		"setLocalDescription",
		"setRemoteDescription",
		"addIceCandidate"
	].forEach(function(t) {
		let n = e.RTCPeerConnection.prototype[t], r = { [t]() {
			return arguments[0] = new (t === "addIceCandidate" ? e.RTCIceCandidate : e.RTCSessionDescription)(arguments[0]), n.apply(this, arguments);
		} };
		e.RTCPeerConnection.prototype[t] = r[t];
	}));
}
function We(e, t) {
	if (typeof e != "object" || !(e.RTCPeerConnection || e.mozRTCPeerConnection) || t.version >= 151) return;
	let n = {
		inboundrtp: "inbound-rtp",
		outboundrtp: "outbound-rtp",
		candidatepair: "candidate-pair",
		localcandidate: "local-candidate",
		remotecandidate: "remote-candidate"
	}, r = e.RTCPeerConnection.prototype.getStats;
	e.RTCPeerConnection.prototype.getStats = function() {
		let [e, i, a] = arguments;
		return this.signalingState === "closed" ? Promise.resolve(/* @__PURE__ */ new Map()) : r.apply(this, [e || null]).then((e) => {
			if (t.version < 53 && !i) try {
				e.forEach((e) => {
					e.type = n[e.type] || e.type;
				});
			} catch (t) {
				if (t.name !== "TypeError") throw t;
				e.forEach((t, r) => {
					e.set(r, Object.assign({}, t, { type: n[t.type] || t.type }));
				});
			}
			return e;
		}).then(i, a);
	};
}
function Ge(e) {
	if (!(typeof e == "object" && e.RTCPeerConnection && e.RTCRtpSender) || e.RTCRtpSender && "getStats" in e.RTCRtpSender.prototype) return;
	let t = e.RTCPeerConnection.prototype.getSenders;
	t && (e.RTCPeerConnection.prototype.getSenders = function() {
		let e = t.apply(this, []);
		return e.forEach((e) => e._pc = this), e;
	});
	let n = e.RTCPeerConnection.prototype.addTrack;
	n && (e.RTCPeerConnection.prototype.addTrack = function() {
		let e = n.apply(this, arguments);
		return e._pc = this, e;
	}), e.RTCRtpSender.prototype.getStats = function() {
		return this.track ? this._pc.getStats(this.track) : Promise.resolve(/* @__PURE__ */ new Map());
	};
}
function Ke(e) {
	if (!(typeof e == "object" && e.RTCPeerConnection && e.RTCRtpSender) || e.RTCRtpSender && "getStats" in e.RTCRtpReceiver.prototype) return;
	let t = e.RTCPeerConnection.prototype.getReceivers;
	t && (e.RTCPeerConnection.prototype.getReceivers = function() {
		let e = t.apply(this, []);
		return e.forEach((e) => e._pc = this), e;
	}), h(e, "track", (e) => (e.receiver._pc = e.srcElement, e)), e.RTCRtpReceiver.prototype.getStats = function() {
		return this._pc.getStats(this.track);
	};
}
function qe(e) {
	!e.RTCPeerConnection || "removeStream" in e.RTCPeerConnection.prototype || (e.RTCPeerConnection.prototype.removeStream = function(e) {
		ge("removeStream", "removeTrack"), this.getSenders().forEach((t) => {
			t.track && e.getTracks().includes(t.track) && this.removeTrack(t);
		});
	});
}
function Je(e) {
	e.DataChannel && !e.RTCDataChannel && (e.RTCDataChannel = e.DataChannel);
}
function Ye(e, t) {
	if (!(typeof e == "object" && e.RTCPeerConnection) || t.version >= 110) return;
	let n = e.RTCPeerConnection.prototype.addTransceiver;
	n && (e.RTCPeerConnection.prototype.addTransceiver = function() {
		this.setParametersPromises = [];
		let e = arguments[1] && arguments[1].sendEncodings;
		e === void 0 && (e = []), e = [...e];
		let t = e.length > 0;
		t && e.forEach((e) => {
			if ("rid" in e && !/^[a-z0-9]{0,16}$/i.test(e.rid)) throw TypeError("Invalid RID value provided.");
			if ("scaleResolutionDownBy" in e && !(parseFloat(e.scaleResolutionDownBy) >= 1)) throw RangeError("scale_resolution_down_by must be >= 1.0");
			if ("maxFramerate" in e && !(parseFloat(e.maxFramerate) >= 0)) throw RangeError("max_framerate must be >= 0.0");
		});
		let r = n.apply(this, arguments);
		if (t) {
			let { sender: t } = r, n = t.getParameters();
			(!("encodings" in n) || n.encodings.length === 1 && Object.keys(n.encodings[0]).length === 0) && (n.encodings = e, t.sendEncodings = e, this.setParametersPromises.push(t.setParameters(n).then(() => {
				delete t.sendEncodings;
			}).catch(() => {
				delete t.sendEncodings;
			})));
		}
		return r;
	});
}
function Xe(e, t) {
	if (!(typeof e == "object" && e.RTCRtpSender) || t.version >= 110) return;
	let n = e.RTCRtpSender.prototype.getParameters;
	n && (e.RTCRtpSender.prototype.getParameters = function() {
		let e = n.apply(this, arguments);
		return "encodings" in e || (e.encodings = [].concat(this.sendEncodings || [{}])), e;
	});
}
function Ze(e, t) {
	if (!(typeof e == "object" && e.RTCPeerConnection) || t.version >= 110) return;
	let n = e.RTCPeerConnection.prototype.createOffer;
	e.RTCPeerConnection.prototype.createOffer = function() {
		return this.setParametersPromises && this.setParametersPromises.length ? Promise.all(this.setParametersPromises).then(() => n.apply(this, arguments)).finally(() => {
			this.setParametersPromises = [];
		}) : n.apply(this, arguments);
	};
}
function Qe(e, t) {
	if (!(typeof e == "object" && e.RTCPeerConnection) || t.version >= 110) return;
	let n = e.RTCPeerConnection.prototype.createAnswer;
	e.RTCPeerConnection.prototype.createAnswer = function() {
		return this.setParametersPromises && this.setParametersPromises.length ? Promise.all(this.setParametersPromises).then(() => n.apply(this, arguments)).finally(() => {
			this.setParametersPromises = [];
		}) : n.apply(this, arguments);
	};
}
var $e = o((() => {
	g(), Re(), Be();
})), et = /* @__PURE__ */ c({
	shimAudioContext: () => lt,
	shimCallbacksAPI: () => rt,
	shimConstraints: () => at,
	shimCreateOfferLegacy: () => ct,
	shimGetUserMedia: () => it,
	shimLocalStreamsAPI: () => tt,
	shimRTCIceServerUrls: () => ot,
	shimRemoteStreamsAPI: () => nt,
	shimTrackEventTransceiver: () => st
});
function tt(e) {
	if (!(typeof e != "object" || !e.RTCPeerConnection)) {
		if ("getLocalStreams" in e.RTCPeerConnection.prototype || (e.RTCPeerConnection.prototype.getLocalStreams = function() {
			return this._localStreams ||= [], this._localStreams;
		}), !("addStream" in e.RTCPeerConnection.prototype)) {
			let t = e.RTCPeerConnection.prototype.addTrack;
			e.RTCPeerConnection.prototype.addStream = function(e) {
				this._localStreams ||= [], this._localStreams.includes(e) || this._localStreams.push(e), e.getAudioTracks().forEach((n) => t.call(this, n, e)), e.getVideoTracks().forEach((n) => t.call(this, n, e));
			}, e.RTCPeerConnection.prototype.addTrack = function(e, ...n) {
				return n && n.forEach((e) => {
					this._localStreams ? this._localStreams.includes(e) || this._localStreams.push(e) : this._localStreams = [e];
				}), t.apply(this, arguments);
			};
		}
		"removeStream" in e.RTCPeerConnection.prototype || (e.RTCPeerConnection.prototype.removeStream = function(e) {
			this._localStreams ||= [];
			let t = this._localStreams.indexOf(e);
			if (t === -1) return;
			this._localStreams.splice(t, 1);
			let n = e.getTracks();
			this.getSenders().forEach((e) => {
				n.includes(e.track) && this.removeTrack(e);
			});
		});
	}
}
function nt(e) {
	if (!(typeof e != "object" || !e.RTCPeerConnection) && ("getRemoteStreams" in e.RTCPeerConnection.prototype || (e.RTCPeerConnection.prototype.getRemoteStreams = function() {
		return this._remoteStreams ? this._remoteStreams : [];
	}), !("onaddstream" in e.RTCPeerConnection.prototype))) {
		Object.defineProperty(e.RTCPeerConnection.prototype, "onaddstream", {
			get() {
				return this._onaddstream;
			},
			set(e) {
				this._onaddstream && (this.removeEventListener("addstream", this._onaddstream), this.removeEventListener("track", this._onaddstreampoly)), this.addEventListener("addstream", this._onaddstream = e), this.addEventListener("track", this._onaddstreampoly = (e) => {
					e.streams.forEach((e) => {
						if (this._remoteStreams ||= [], this._remoteStreams.includes(e)) return;
						this._remoteStreams.push(e);
						let t = new Event("addstream");
						t.stream = e, this.dispatchEvent(t);
					});
				});
			}
		});
		let t = e.RTCPeerConnection.prototype.setRemoteDescription;
		e.RTCPeerConnection.prototype.setRemoteDescription = function() {
			let e = this;
			return this._onaddstreampoly || this.addEventListener("track", this._onaddstreampoly = function(t) {
				t.streams.forEach((t) => {
					if (e._remoteStreams ||= [], e._remoteStreams.indexOf(t) >= 0) return;
					e._remoteStreams.push(t);
					let n = new Event("addstream");
					n.stream = t, e.dispatchEvent(n);
				});
			}), t.apply(e, arguments);
		};
	}
}
function rt(e) {
	if (typeof e != "object" || !e.RTCPeerConnection) return;
	let t = e.RTCPeerConnection.prototype, n = t.createOffer, r = t.createAnswer, i = t.setLocalDescription, a = t.setRemoteDescription, o = t.addIceCandidate;
	t.createOffer = function(e, t) {
		let r = arguments.length >= 2 ? arguments[2] : arguments[0], i = n.apply(this, [r]);
		return t ? (i.then(e, t), Promise.resolve()) : i;
	}, t.createAnswer = function(e, t) {
		let n = arguments.length >= 2 ? arguments[2] : arguments[0], i = r.apply(this, [n]);
		return t ? (i.then(e, t), Promise.resolve()) : i;
	};
	let s = function(e, t, n) {
		let r = i.apply(this, [e]);
		return n ? (r.then(t, n), Promise.resolve()) : r;
	};
	t.setLocalDescription = s, s = function(e, t, n) {
		let r = a.apply(this, [e]);
		return n ? (r.then(t, n), Promise.resolve()) : r;
	}, t.setRemoteDescription = s, s = function(e, t, n) {
		let r = o.apply(this, [e]);
		return n ? (r.then(t, n), Promise.resolve()) : r;
	}, t.addIceCandidate = s;
}
function it(e) {
	let t = e && e.navigator;
	if (t.mediaDevices && t.mediaDevices.getUserMedia) {
		let e = t.mediaDevices, n = e.getUserMedia.bind(e);
		t.mediaDevices.getUserMedia = (e) => n(at(e));
	}
	!t.getUserMedia && t.mediaDevices && t.mediaDevices.getUserMedia && (t.getUserMedia = function(e, n, r) {
		t.mediaDevices.getUserMedia(e).then(n, r);
	}.bind(t));
}
function at(e) {
	return e && e.video !== void 0 ? Object.assign({}, e, { video: ye(e.video) }) : e;
}
function ot(e) {
	if (!e.RTCPeerConnection) return;
	let t = e.RTCPeerConnection;
	e.RTCPeerConnection = function(e, n) {
		if (e && e.iceServers) {
			let t = [];
			for (let n = 0; n < e.iceServers.length; n++) {
				let r = e.iceServers[n];
				r.urls === void 0 && r.url ? (ge("RTCIceServer.url", "RTCIceServer.urls"), r = JSON.parse(JSON.stringify(r)), r.urls = r.url, delete r.url, t.push(r)) : t.push(e.iceServers[n]);
			}
			e.iceServers = t;
		}
		return new t(e, n);
	}, e.RTCPeerConnection.prototype = t.prototype, "generateCertificate" in t && Object.defineProperty(e.RTCPeerConnection, "generateCertificate", { get() {
		return t.generateCertificate;
	} });
}
function st(e) {
	typeof e == "object" && e.RTCTrackEvent && "receiver" in e.RTCTrackEvent.prototype && !("transceiver" in e.RTCTrackEvent.prototype) && Object.defineProperty(e.RTCTrackEvent.prototype, "transceiver", { get() {
		return { receiver: this.receiver };
	} });
}
function ct(e) {
	let t = e.RTCPeerConnection.prototype.createOffer;
	e.RTCPeerConnection.prototype.createOffer = function(e) {
		if (e) {
			e.offerToReceiveAudio !== void 0 && (e.offerToReceiveAudio = !!e.offerToReceiveAudio);
			let t = this.getTransceivers().find((e) => e.receiver.track.kind === "audio");
			e.offerToReceiveAudio === !1 && t ? t.direction === "sendrecv" ? t.setDirection ? t.setDirection("sendonly") : t.direction = "sendonly" : t.direction === "recvonly" && (t.setDirection ? t.setDirection("inactive") : t.direction = "inactive") : e.offerToReceiveAudio === !0 && !t && this.addTransceiver("audio", { direction: "recvonly" }), e.offerToReceiveVideo !== void 0 && (e.offerToReceiveVideo = !!e.offerToReceiveVideo);
			let n = this.getTransceivers().find((e) => e.receiver.track.kind === "video");
			e.offerToReceiveVideo === !1 && n ? n.direction === "sendrecv" ? n.setDirection ? n.setDirection("sendonly") : n.direction = "sendonly" : n.direction === "recvonly" && (n.setDirection ? n.setDirection("inactive") : n.direction = "inactive") : e.offerToReceiveVideo === !0 && !n && this.addTransceiver("video", { direction: "recvonly" });
		}
		return t.apply(this, arguments);
	};
}
function lt(e) {
	typeof e != "object" || e.AudioContext || (e.AudioContext = e.webkitAudioContext);
}
var ut = o((() => {
	g();
})), dt = /* @__PURE__ */ s(((e, t) => {
	var n = {};
	n.generateIdentifier = function() {
		return Math.random().toString(36).substring(2, 12);
	}, n.localCName = n.generateIdentifier(), n.splitLines = function(e) {
		return e.trim().split("\n").map((e) => e.trim());
	}, n.splitSections = function(e) {
		return e.split("\nm=").map((e, t) => (t > 0 ? "m=" + e : e).trim() + "\r\n");
	}, n.getDescription = function(e) {
		let t = n.splitSections(e);
		return t && t[0];
	}, n.getMediaSections = function(e) {
		let t = n.splitSections(e);
		return t.shift(), t;
	}, n.matchPrefix = function(e, t) {
		return n.splitLines(e).filter((e) => e.indexOf(t) === 0);
	}, n.parseCandidate = function(e) {
		let t;
		t = e.indexOf("a=candidate:") === 0 ? e.substring(12).split(" ") : e.substring(10).split(" ");
		let n = {
			foundation: t[0],
			component: {
				1: "rtp",
				2: "rtcp"
			}[t[1]] || t[1],
			protocol: t[2].toLowerCase(),
			priority: parseInt(t[3], 10),
			ip: t[4],
			address: t[4],
			port: parseInt(t[5], 10),
			type: t[7]
		};
		for (let e = 8; e < t.length; e += 2) switch (t[e]) {
			case "raddr":
				n.relatedAddress = t[e + 1];
				break;
			case "rport":
				n.relatedPort = parseInt(t[e + 1], 10);
				break;
			case "tcptype":
				n.tcpType = t[e + 1];
				break;
			case "ufrag":
				n.ufrag = t[e + 1], n.usernameFragment = t[e + 1];
				break;
			default: n[t[e]] === void 0 && (n[t[e]] = t[e + 1]);
		}
		return n;
	}, n.writeCandidate = function(e) {
		let t = [];
		t.push(e.foundation);
		let n = e.component;
		n === "rtp" ? t.push(1) : n === "rtcp" ? t.push(2) : t.push(n), t.push(e.protocol.toUpperCase()), t.push(e.priority), t.push(e.address || e.ip), t.push(e.port);
		let r = e.type;
		return t.push("typ"), t.push(r), r !== "host" && e.relatedAddress && e.relatedPort !== void 0 && (t.push("raddr"), t.push(e.relatedAddress), t.push("rport"), t.push(e.relatedPort)), e.tcpType && e.protocol.toLowerCase() === "tcp" && (t.push("tcptype"), t.push(e.tcpType)), (e.usernameFragment || e.ufrag) && (t.push("ufrag"), t.push(e.usernameFragment || e.ufrag)), "candidate:" + t.join(" ");
	}, n.parseIceOptions = function(e) {
		return e.substring(14).split(" ");
	}, n.parseRtpMap = function(e) {
		let t = e.substring(9).split(" "), n = { payloadType: parseInt(t.shift(), 10) };
		return t = t[0].split("/"), n.name = t[0], n.clockRate = parseInt(t[1], 10), n.channels = t.length === 3 ? parseInt(t[2], 10) : 1, n.numChannels = n.channels, n;
	}, n.writeRtpMap = function(e) {
		let t = e.payloadType;
		e.preferredPayloadType !== void 0 && (t = e.preferredPayloadType);
		let n = e.channels || e.numChannels || 1;
		return "a=rtpmap:" + t + " " + e.name + "/" + e.clockRate + (n === 1 ? "" : "/" + n) + "\r\n";
	}, n.parseExtmap = function(e) {
		let t = e.substring(9).split(" ");
		return {
			id: parseInt(t[0], 10),
			direction: t[0].indexOf("/") > 0 ? t[0].split("/")[1] : "sendrecv",
			uri: t[1],
			attributes: t.slice(2).join(" ")
		};
	}, n.writeExtmap = function(e) {
		return "a=extmap:" + (e.id || e.preferredId) + (e.direction && e.direction !== "sendrecv" ? "/" + e.direction : "") + " " + e.uri + (e.attributes ? " " + e.attributes : "") + "\r\n";
	}, n.parseFmtp = function(e) {
		let t = {}, n, r = e.substring(e.indexOf(" ") + 1).split(";");
		for (let e = 0; e < r.length; e++) n = r[e].trim().split("="), t[n[0].trim()] = n[1];
		return t;
	}, n.writeFmtp = function(e) {
		let t = "", n = e.payloadType;
		if (e.preferredPayloadType !== void 0 && (n = e.preferredPayloadType), e.parameters && Object.keys(e.parameters).length) {
			let r = [];
			Object.keys(e.parameters).forEach((t) => {
				e.parameters[t] === void 0 ? r.push(t) : r.push(t + "=" + e.parameters[t]);
			}), t += "a=fmtp:" + n + " " + r.join(";") + "\r\n";
		}
		return t;
	}, n.parseRtcpFb = function(e) {
		let t = e.substring(e.indexOf(" ") + 1).split(" ");
		return {
			type: t.shift(),
			parameter: t.join(" ")
		};
	}, n.writeRtcpFb = function(e) {
		let t = "", n = e.payloadType;
		return e.preferredPayloadType !== void 0 && (n = e.preferredPayloadType), e.rtcpFeedback && e.rtcpFeedback.length && e.rtcpFeedback.forEach((e) => {
			t += "a=rtcp-fb:" + n + " " + e.type + (e.parameter && e.parameter.length ? " " + e.parameter : "") + "\r\n";
		}), t;
	}, n.parseSsrcMedia = function(e) {
		let t = e.indexOf(" "), n = { ssrc: parseInt(e.substring(7, t), 10) }, r = e.indexOf(":", t);
		return r > -1 ? (n.attribute = e.substring(t + 1, r), n.value = e.substring(r + 1)) : n.attribute = e.substring(t + 1), n;
	}, n.parseSsrcGroup = function(e) {
		let t = e.substring(13).split(" ");
		return {
			semantics: t.shift(),
			ssrcs: t.map((e) => parseInt(e, 10))
		};
	}, n.getMid = function(e) {
		let t = n.matchPrefix(e, "a=mid:")[0];
		if (t) return t.substring(6);
	}, n.parseFingerprint = function(e) {
		let t = e.substring(14).split(" ");
		return {
			algorithm: t[0].toLowerCase(),
			value: t[1].toUpperCase()
		};
	}, n.getDtlsParameters = function(e, t) {
		return {
			role: "auto",
			fingerprints: n.matchPrefix(e + t, "a=fingerprint:").map(n.parseFingerprint)
		};
	}, n.writeDtlsParameters = function(e, t) {
		let n = "a=setup:" + t + "\r\n";
		return e.fingerprints.forEach((e) => {
			n += "a=fingerprint:" + e.algorithm + " " + e.value + "\r\n";
		}), n;
	}, n.parseCryptoLine = function(e) {
		let t = e.substring(9).split(" ");
		return {
			tag: parseInt(t[0], 10),
			cryptoSuite: t[1],
			keyParams: t[2],
			sessionParams: t.slice(3)
		};
	}, n.writeCryptoLine = function(e) {
		return "a=crypto:" + e.tag + " " + e.cryptoSuite + " " + (typeof e.keyParams == "object" ? n.writeCryptoKeyParams(e.keyParams) : e.keyParams) + (e.sessionParams ? " " + e.sessionParams.join(" ") : "") + "\r\n";
	}, n.parseCryptoKeyParams = function(e) {
		if (e.indexOf("inline:") !== 0) return null;
		let t = e.substring(7).split("|");
		return {
			keyMethod: "inline",
			keySalt: t[0],
			lifeTime: t[1],
			mkiValue: t[2] ? t[2].split(":")[0] : void 0,
			mkiLength: t[2] ? t[2].split(":")[1] : void 0
		};
	}, n.writeCryptoKeyParams = function(e) {
		return e.keyMethod + ":" + e.keySalt + (e.lifeTime ? "|" + e.lifeTime : "") + (e.mkiValue && e.mkiLength ? "|" + e.mkiValue + ":" + e.mkiLength : "");
	}, n.getCryptoParameters = function(e, t) {
		return n.matchPrefix(e + t, "a=crypto:").map(n.parseCryptoLine);
	}, n.getIceParameters = function(e, t) {
		let r = n.matchPrefix(e + t, "a=ice-ufrag:")[0], i = n.matchPrefix(e + t, "a=ice-pwd:")[0];
		return r && i ? {
			usernameFragment: r.substring(12),
			password: i.substring(10)
		} : null;
	}, n.writeIceParameters = function(e) {
		let t = "a=ice-ufrag:" + e.usernameFragment + "\r\na=ice-pwd:" + e.password + "\r\n";
		return e.iceLite && (t += "a=ice-lite\r\n"), t;
	}, n.parseRtpParameters = function(e) {
		let t = {
			codecs: [],
			headerExtensions: [],
			fecMechanisms: [],
			rtcp: []
		}, r = n.splitLines(e)[0].split(" ");
		t.profile = r[2];
		for (let i = 3; i < r.length; i++) {
			let a = r[i], o = n.matchPrefix(e, "a=rtpmap:" + a + " ")[0];
			if (o) {
				let r = n.parseRtpMap(o), i = n.matchPrefix(e, "a=fmtp:" + a + " ");
				switch (r.parameters = i.length ? n.parseFmtp(i[0]) : {}, r.rtcpFeedback = n.matchPrefix(e, "a=rtcp-fb:" + a + " ").map(n.parseRtcpFb), t.codecs.push(r), r.name.toUpperCase()) {
					case "RED":
					case "ULPFEC": t.fecMechanisms.push(r.name.toUpperCase());
				}
			}
		}
		n.matchPrefix(e, "a=extmap:").forEach((e) => {
			t.headerExtensions.push(n.parseExtmap(e));
		});
		let i = n.matchPrefix(e, "a=rtcp-fb:* ").map(n.parseRtcpFb);
		return t.codecs.forEach((e) => {
			i.forEach((t) => {
				e.rtcpFeedback.find((e) => e.type === t.type && e.parameter === t.parameter) || e.rtcpFeedback.push(t);
			});
		}), t;
	}, n.writeRtpDescription = function(e, t) {
		let r = "";
		r += "m=" + e + " ", r += t.codecs.length > 0 ? "9" : "0", r += " " + (t.profile || "UDP/TLS/RTP/SAVPF") + " ", r += t.codecs.map((e) => e.preferredPayloadType === void 0 ? e.payloadType : e.preferredPayloadType).join(" ") + "\r\n", r += "c=IN IP4 0.0.0.0\r\n", r += "a=rtcp:9 IN IP4 0.0.0.0\r\n", t.codecs.forEach((e) => {
			r += n.writeRtpMap(e), r += n.writeFmtp(e), r += n.writeRtcpFb(e);
		});
		let i = 0;
		return t.codecs.forEach((e) => {
			e.maxptime > i && (i = e.maxptime);
		}), i > 0 && (r += "a=maxptime:" + i + "\r\n"), t.headerExtensions && t.headerExtensions.forEach((e) => {
			r += n.writeExtmap(e);
		}), r;
	}, n.parseRtpEncodingParameters = function(e) {
		let t = [], r = n.parseRtpParameters(e), i = r.fecMechanisms.indexOf("RED") !== -1, a = r.fecMechanisms.indexOf("ULPFEC") !== -1, o = n.matchPrefix(e, "a=ssrc:").map((e) => n.parseSsrcMedia(e)).filter((e) => e.attribute === "cname"), s = o.length > 0 && o[0].ssrc, c, l = n.matchPrefix(e, "a=ssrc-group:FID").map((e) => e.substring(17).split(" ").map((e) => parseInt(e, 10)));
		l.length > 0 && l[0].length > 1 && l[0][0] === s && (c = l[0][1]), r.codecs.forEach((e) => {
			if (e.name.toUpperCase() === "RTX" && e.parameters.apt) {
				let n = {
					ssrc: s,
					codecPayloadType: parseInt(e.parameters.apt, 10)
				};
				s && c && (n.rtx = { ssrc: c }), t.push(n), i && (n = JSON.parse(JSON.stringify(n)), n.fec = {
					ssrc: s,
					mechanism: a ? "red+ulpfec" : "red"
				}, t.push(n));
			}
		}), t.length === 0 && s && t.push({ ssrc: s });
		let u = n.matchPrefix(e, "b=");
		return u.length && (u = u[0].indexOf("b=TIAS:") === 0 ? parseInt(u[0].substring(7), 10) : u[0].indexOf("b=AS:") === 0 ? parseInt(u[0].substring(5), 10) * 1e3 * .95 - 16e3 : void 0, t.forEach((e) => {
			e.maxBitrate = u;
		})), t;
	}, n.parseRtcpParameters = function(e) {
		let t = {}, r = n.matchPrefix(e, "a=ssrc:").map((e) => n.parseSsrcMedia(e)).filter((e) => e.attribute === "cname")[0];
		r && (t.cname = r.value, t.ssrc = r.ssrc);
		let i = n.matchPrefix(e, "a=rtcp-rsize");
		return t.reducedSize = i.length > 0, t.compound = i.length === 0, t.mux = n.matchPrefix(e, "a=rtcp-mux").length > 0, t;
	}, n.writeRtcpParameters = function(e) {
		let t = "";
		return e.reducedSize && (t += "a=rtcp-rsize\r\n"), e.mux && (t += "a=rtcp-mux\r\n"), e.ssrc !== void 0 && e.cname && (t += "a=ssrc:" + e.ssrc + " cname:" + e.cname + "\r\n"), t;
	}, n.parseMsid = function(e) {
		let t, r = n.matchPrefix(e, "a=msid:");
		if (r.length === 1) return t = r[0].substring(7).split(" "), {
			stream: t[0],
			track: t[1]
		};
		let i = n.matchPrefix(e, "a=ssrc:").map((e) => n.parseSsrcMedia(e)).filter((e) => e.attribute === "msid");
		if (i.length > 0) return t = i[0].value.split(" "), {
			stream: t[0],
			track: t[1]
		};
	}, n.parseSctpDescription = function(e) {
		let t = n.parseMLine(e), r = n.matchPrefix(e, "a=max-message-size:"), i;
		r.length > 0 && (i = parseInt(r[0].substring(19), 10)), isNaN(i) && (i = 65536);
		let a = n.matchPrefix(e, "a=sctp-port:");
		if (a.length > 0) return {
			port: parseInt(a[0].substring(12), 10),
			protocol: t.fmt,
			maxMessageSize: i
		};
		let o = n.matchPrefix(e, "a=sctpmap:");
		if (o.length > 0) {
			let e = o[0].substring(10).split(" ");
			return {
				port: parseInt(e[0], 10),
				protocol: e[1],
				maxMessageSize: i
			};
		}
	}, n.writeSctpDescription = function(e, t) {
		let n = [];
		return n = e.protocol === "DTLS/SCTP" ? [
			"m=" + e.kind + " 9 " + e.protocol + " " + t.port + "\r\n",
			"c=IN IP4 0.0.0.0\r\n",
			"a=sctpmap:" + t.port + " " + t.protocol + " 65535\r\n"
		] : [
			"m=" + e.kind + " 9 " + e.protocol + " " + t.protocol + "\r\n",
			"c=IN IP4 0.0.0.0\r\n",
			"a=sctp-port:" + t.port + "\r\n"
		], t.maxMessageSize !== void 0 && n.push("a=max-message-size:" + t.maxMessageSize + "\r\n"), n.join("");
	}, n.generateSessionId = function() {
		return Math.random().toString().substr(2, 22);
	}, n.writeSessionBoilerplate = function(e, t, r) {
		let i, a = t === void 0 ? 2 : t;
		return i = e || n.generateSessionId(), "v=0\r\no=" + (r || "thisisadapterortc") + " " + i + " " + a + " IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n";
	}, n.getDirection = function(e, t) {
		let r = n.splitLines(e);
		for (let e = 0; e < r.length; e++) switch (r[e]) {
			case "a=sendrecv":
			case "a=sendonly":
			case "a=recvonly":
			case "a=inactive": return r[e].substring(2);
		}
		return t ? n.getDirection(t) : "sendrecv";
	}, n.getKind = function(e) {
		return n.splitLines(e)[0].split(" ")[0].substring(2);
	}, n.isRejected = function(e) {
		return e.split(" ", 2)[1] === "0";
	}, n.parseMLine = function(e) {
		let t = n.splitLines(e)[0].substring(2).split(" ");
		return {
			kind: t[0],
			port: parseInt(t[1], 10),
			protocol: t[2],
			fmt: t.slice(3).join(" ")
		};
	}, n.parseOLine = function(e) {
		let t = n.matchPrefix(e, "o=")[0].substring(2).split(" ");
		return {
			username: t[0],
			sessionId: t[1],
			sessionVersion: parseInt(t[2], 10),
			netType: t[3],
			addressType: t[4],
			address: t[5]
		};
	}, n.isValidSDP = function(e) {
		if (typeof e != "string" || e.length === 0) return !1;
		let t = n.splitLines(e);
		for (let e = 0; e < t.length; e++) if (t[e].length < 2 || t[e].charAt(1) !== "=") return !1;
		return !0;
	}, typeof t == "object" && (t.exports = n);
})), ft = /* @__PURE__ */ c({
	removeExtmapAllowMixed: () => gt,
	shimAddIceCandidateNullOrEmpty: () => _t,
	shimConnectionState: () => ht,
	shimMaxMessageSize: () => _,
	shimParameterlessSetLocalDescription: () => vt,
	shimRTCIceCandidate: () => pt,
	shimRTCIceCandidateRelayProtocol: () => mt,
	shimSendThrowTypeError: () => v
});
function pt(e) {
	if (!e.RTCIceCandidate || e.RTCIceCandidate && "foundation" in e.RTCIceCandidate.prototype) return;
	let t = e.RTCIceCandidate;
	e.RTCIceCandidate = function(e) {
		if (typeof e == "object" && e.candidate && e.candidate.indexOf("a=") === 0 && (e = JSON.parse(JSON.stringify(e)), e.candidate = e.candidate.substring(2)), e.candidate && e.candidate.length) {
			let n = new t(e), r = y.default.parseCandidate(e.candidate);
			for (let e in r) e in n || Object.defineProperty(n, e, { value: r[e] });
			return n.toJSON = function() {
				return {
					candidate: n.candidate,
					sdpMid: n.sdpMid,
					sdpMLineIndex: n.sdpMLineIndex,
					usernameFragment: n.usernameFragment
				};
			}, n;
		}
		return new t(e);
	}, e.RTCIceCandidate.prototype = t.prototype, h(e, "icecandidate", (t) => (t.candidate && Object.defineProperty(t, "candidate", {
		value: new e.RTCIceCandidate(t.candidate),
		writable: "false"
	}), t));
}
function mt(e) {
	!e.RTCIceCandidate || e.RTCIceCandidate && "relayProtocol" in e.RTCIceCandidate.prototype || h(e, "icecandidate", (e) => {
		if (e.candidate) {
			let t = y.default.parseCandidate(e.candidate.candidate);
			t.type === "relay" && (e.candidate.relayProtocol = {
				0: "tls",
				1: "tcp",
				2: "udp"
			}[t.priority >> 24]);
		}
		return e;
	});
}
function _(e, t) {
	if (!e.RTCPeerConnection || t.browser === "chrome" && t.version > 102 || t.browser === "firefox" && t.version >= 113) return;
	"sctp" in e.RTCPeerConnection.prototype || Object.defineProperty(e.RTCPeerConnection.prototype, "sctp", { get() {
		return this._sctp === void 0 ? null : this._sctp;
	} });
	let n = function(e) {
		if (!e || !e.sdp) return !1;
		let t = y.default.splitSections(e.sdp);
		return t.shift(), t.some((e) => {
			let t = y.default.parseMLine(e);
			return t && t.kind === "application" && t.protocol.indexOf("SCTP") !== -1;
		});
	}, r = function(e) {
		let t = e.sdp.match(/mozilla...THIS_IS_SDPARTA-(\d+)/);
		if (t === null || t.length < 2) return -1;
		let n = parseInt(t[1], 10);
		return n === n ? n : -1;
	}, i = function(e) {
		let n = 65536;
		return t.browser === "firefox" && (n = t.version < 57 ? e === -1 ? 16384 : 2147483637 : t.version < 60 ? t.version === 57 ? 65535 : 65536 : 2147483637), n;
	}, a = function(e, n) {
		let r = 65536;
		t.browser === "firefox" && t.version === 57 && (r = 65535);
		let i = y.default.matchPrefix(e.sdp, "a=max-message-size:");
		return i.length > 0 ? r = parseInt(i[0].substring(19), 10) : t.browser === "firefox" && n !== -1 && (r = 2147483637), r;
	}, o = e.RTCPeerConnection.prototype.setRemoteDescription;
	e.RTCPeerConnection.prototype.setRemoteDescription = function() {
		if (this._sctp = null, t.browser === "chrome" && t.version >= 76) {
			let { sdpSemantics: e } = this.getConfiguration();
			e === "plan-b" && Object.defineProperty(this, "sctp", {
				get() {
					return this._sctp === void 0 ? null : this._sctp;
				},
				enumerable: !0,
				configurable: !0
			});
		}
		if (n(arguments[0])) {
			let e = r(arguments[0]), t = i(e), n = a(arguments[0], e), o;
			o = t === 0 && n === 0 ? Infinity : t === 0 || n === 0 ? Math.max(t, n) : Math.min(t, n);
			let s = {};
			Object.defineProperty(s, "maxMessageSize", { get() {
				return o;
			} }), this._sctp = s;
		}
		return o.apply(this, arguments);
	};
}
function v(e, t) {
	if (!(e.RTCPeerConnection && "createDataChannel" in e.RTCPeerConnection.prototype) || t.browser === "chrome" && t.version >= 149 || t.browser === "firefox" && t.version > 60) return;
	function n(e, t) {
		let n = e.send;
		e.send = function() {
			let r = arguments[0], i = r.length || r.size || r.byteLength;
			if (e.readyState === "open" && t.sctp && i > t.sctp.maxMessageSize) throw TypeError("Message too large (can send a maximum of " + t.sctp.maxMessageSize + " bytes)");
			return n.apply(e, arguments);
		};
	}
	let r = e.RTCPeerConnection.prototype.createDataChannel;
	e.RTCPeerConnection.prototype.createDataChannel = function() {
		let e = r.apply(this, arguments);
		return n(e, this), e;
	}, h(e, "datachannel", (e) => (n(e.channel, e.target), e));
}
function ht(e) {
	if (!e.RTCPeerConnection || "connectionState" in e.RTCPeerConnection.prototype) return;
	let t = e.RTCPeerConnection.prototype;
	Object.defineProperty(t, "connectionState", {
		get() {
			return {
				completed: "connected",
				checking: "connecting"
			}[this.iceConnectionState] || this.iceConnectionState;
		},
		enumerable: !0,
		configurable: !0
	}), Object.defineProperty(t, "onconnectionstatechange", {
		get() {
			return this._onconnectionstatechange || null;
		},
		set(e) {
			this._onconnectionstatechange && (this.removeEventListener("connectionstatechange", this._onconnectionstatechange), delete this._onconnectionstatechange), e && this.addEventListener("connectionstatechange", this._onconnectionstatechange = e);
		},
		enumerable: !0,
		configurable: !0
	}), ["setLocalDescription", "setRemoteDescription"].forEach((e) => {
		let n = t[e];
		t[e] = function() {
			return this._connectionstatechangepoly || (this._connectionstatechangepoly = (e) => {
				let t = e.target;
				if (t._lastConnectionState !== t.connectionState) {
					t._lastConnectionState = t.connectionState;
					let n = new Event("connectionstatechange", e);
					t.dispatchEvent(n);
				}
				return e;
			}, this.addEventListener("iceconnectionstatechange", this._connectionstatechangepoly)), n.apply(this, arguments);
		};
	});
}
function gt(e, t) {
	if (!e.RTCPeerConnection || t.browser === "chrome" && t.version >= 71 || t.browser === "safari" && t._safariVersion >= 13.1) return;
	let n = e.RTCPeerConnection.prototype.setRemoteDescription;
	e.RTCPeerConnection.prototype.setRemoteDescription = function(t) {
		if (t && t.sdp && t.sdp.indexOf("\na=extmap-allow-mixed") !== -1) {
			let n = t.sdp.split("\n").filter((e) => e.trim() !== "a=extmap-allow-mixed").join("\n");
			e.RTCSessionDescription && t instanceof e.RTCSessionDescription ? arguments[0] = new e.RTCSessionDescription({
				type: t.type,
				sdp: n
			}) : t.sdp = n;
		}
		return n.apply(this, arguments);
	};
}
function _t(e, t) {
	if (!(e.RTCPeerConnection && e.RTCPeerConnection.prototype)) return;
	let n = e.RTCPeerConnection.prototype.addIceCandidate;
	!n || n.length === 0 || (e.RTCPeerConnection.prototype.addIceCandidate = function() {
		return arguments[0] ? (t.browser === "chrome" && t.version < 78 || t.browser === "firefox" && t.version < 68 || t.browser === "safari") && arguments[0] && arguments[0].candidate === "" ? Promise.resolve() : n.apply(this, arguments) : (arguments[1] && arguments[1].apply(null), Promise.resolve());
	});
}
function vt(e, t) {
	if (!(e.RTCPeerConnection && e.RTCPeerConnection.prototype)) return;
	let n = e.RTCPeerConnection.prototype.setLocalDescription;
	!n || n.length === 0 || (e.RTCPeerConnection.prototype.setLocalDescription = function() {
		let e = arguments[0] || {};
		if (typeof e != "object" || e.type && e.sdp) return n.apply(this, arguments);
		if (e = {
			type: e.type,
			sdp: e.sdp
		}, !e.type) switch (this.signalingState) {
			case "stable":
			case "have-local-offer":
			case "have-remote-pranswer":
				e.type = "offer";
				break;
			default: e.type = "answer";
		}
		return e.sdp || e.type !== "offer" && e.type !== "answer" ? n.apply(this, [e]) : (e.type === "offer" ? this.createOffer : this.createAnswer).apply(this).then((e) => n.apply(this, [e]));
	});
}
var y, yt = o((() => {
	y = /* @__PURE__ */ u(dt()), g();
}));
//#endregion
//#region node_modules/webrtc-adapter/src/js/adapter_factory.js
function bt({ window: e } = {}, t = {
	shimChrome: !0,
	shimFirefox: !0,
	shimSafari: !0
}) {
	let n = he, r = _e(e), i = {
		browserDetails: r,
		commonShim: ft,
		extractVersion: m,
		disableLog: pe,
		disableWarnings: me,
		sdp: xt
	};
	switch (r.browser) {
		case "chrome":
			if (!De || !Pe || !t.shimChrome) return n("Chrome shim is not included in this adapter release."), i;
			if (r.version === null) return n("Chrome shim can not determine version, not shimming."), i;
			n("adapter.js shimming chrome."), i.browserShim = De, _t(e, r), vt(e, r), we(e, r), Oe(e, r), Pe(e, r), ke(e, r), Ne(e, r), Ae(e, r), je(e, r), Fe(e, r), pt(e, r), mt(e, r), ht(e, r), _(e, r), v(e, r), gt(e, r);
			break;
		case "firefox":
			if (!Ve || !Ue || !t.shimFirefox) return n("Firefox shim is not included in this adapter release."), i;
			n("adapter.js shimming firefox."), i.browserShim = Ve, _t(e, r), vt(e, r), Le(e, r), Ue(e, r), We(e, r), He(e, r), qe(e, r), Ge(e, r), Ke(e, r), Je(e, r), Ye(e, r), Xe(e, r), Ze(e, r), Qe(e, r), pt(e, r), ht(e, r), _(e, r), v(e, r);
			break;
		case "safari":
			if (!et || !t.shimSafari) return n("Safari shim is not included in this adapter release."), i;
			n("adapter.js shimming safari."), i.browserShim = et, _t(e, r), vt(e, r), ot(e, r), ct(e, r), rt(e, r), tt(e, r), nt(e, r), st(e, r), it(e, r), lt(e, r), pt(e, r), mt(e, r), _(e, r), v(e, r), gt(e, r);
			break;
		default: n("Unsupported browser!");
	}
	return i;
}
var xt, St = o((() => {
	g(), Ie(), $e(), ut(), yt(), xt = /* @__PURE__ */ u(dt());
})), Ct, wt = o((() => {
	St(), Ct = bt({ window: typeof window > "u" ? void 0 : window });
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/utils/int.mjs
function Tt(e, t, n) {
	var r = n / 4294967296, i = n;
	e.setUint32(t, r), e.setUint32(t + 4, i);
}
function Et(e, t, n) {
	var r = Math.floor(n / 4294967296), i = n;
	e.setUint32(t, r), e.setUint32(t + 4, i);
}
function Dt(e, t) {
	var n = e.getInt32(t), r = e.getUint32(t + 4);
	return n * 4294967296 + r;
}
function Ot(e, t) {
	var n = e.getUint32(t), r = e.getUint32(t + 4);
	return n * 4294967296 + r;
}
var b, x = o((() => {
	b = 4294967295;
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/utils/utf8.mjs
function kt(e) {
	for (var t = e.length, n = 0, r = 0; r < t;) {
		var i = e.charCodeAt(r++);
		if (!(i & 4294967168)) {
			n++;
			continue;
		}
		if (!(i & 4294965248)) n += 2;
		else {
			if (i >= 55296 && i <= 56319 && r < t) {
				var a = e.charCodeAt(r);
				(a & 64512) == 56320 && (++r, i = ((i & 1023) << 10) + (a & 1023) + 65536);
			}
			i & 4294901760 ? n += 4 : n += 3;
		}
	}
	return n;
}
function At(e, t, n) {
	for (var r = e.length, i = n, a = 0; a < r;) {
		var o = e.charCodeAt(a++);
		if (!(o & 4294967168)) {
			t[i++] = o;
			continue;
		}
		if (!(o & 4294965248)) t[i++] = o >> 6 & 31 | 192;
		else {
			if (o >= 55296 && o <= 56319 && a < r) {
				var s = e.charCodeAt(a);
				(s & 64512) == 56320 && (++a, o = ((o & 1023) << 10) + (s & 1023) + 65536);
			}
			o & 4294901760 ? (t[i++] = o >> 18 & 7 | 240, t[i++] = o >> 12 & 63 | 128, t[i++] = o >> 6 & 63 | 128) : (t[i++] = o >> 12 & 15 | 224, t[i++] = o >> 6 & 63 | 128);
		}
		t[i++] = o & 63 | 128;
	}
}
function jt(e, t, n) {
	t.set(C.encode(e), n);
}
function Mt(e, t, n) {
	C.encodeInto(e, t.subarray(n));
}
function Nt(e, t, n) {
	for (var r = t, i = r + n, a = [], o = ""; r < i;) {
		var s = e[r++];
		if (!(s & 128)) a.push(s);
		else if ((s & 224) == 192) {
			var c = e[r++] & 63;
			a.push((s & 31) << 6 | c);
		} else if ((s & 240) == 224) {
			var c = e[r++] & 63, l = e[r++] & 63;
			a.push((s & 31) << 12 | c << 6 | l);
		} else if ((s & 248) == 240) {
			var c = e[r++] & 63, l = e[r++] & 63, u = e[r++] & 63, d = (s & 7) << 18 | c << 12 | l << 6 | u;
			d > 65535 && (d -= 65536, a.push(d >>> 10 & 1023 | 55296), d = 56320 | d & 1023), a.push(d);
		} else a.push(s);
		a.length >= Lt && (o += String.fromCharCode.apply(String, a), a.length = 0);
	}
	return a.length > 0 && (o += String.fromCharCode.apply(String, a)), o;
}
function Pt(e, t, n) {
	var r = e.subarray(t, t + n);
	return Rt.decode(r);
}
var S, C, Ft, It, Lt, Rt, zt, Bt = o((() => {
	x(), S = (typeof process > "u" || (process == null ? void 0 : process.env)?.TEXT_ENCODING !== "never") && typeof TextEncoder < "u" && typeof TextDecoder < "u", C = S ? new TextEncoder() : void 0, Ft = S ? typeof process < "u" && (process == null ? void 0 : process.env)?.TEXT_ENCODING !== "force" ? 200 : 0 : b, It = C?.encodeInto ? Mt : jt, Lt = 4096, Rt = S ? new TextDecoder() : null, zt = S ? typeof process < "u" && (process == null ? void 0 : process.env)?.TEXT_DECODER !== "force" ? 200 : 0 : b;
})), w, Vt = o((() => {
	w = function() {
		function e(e, t) {
			this.type = e, this.data = t;
		}
		return e;
	}();
})), Ht, T, Ut = o((() => {
	Ht = (function() {
		var e = function(t, n) {
			return e = Object.setPrototypeOf || { __proto__: [] } instanceof Array && function(e, t) {
				e.__proto__ = t;
			} || function(e, t) {
				for (var n in t) Object.prototype.hasOwnProperty.call(t, n) && (e[n] = t[n]);
			}, e(t, n);
		};
		return function(t, n) {
			if (typeof n != "function" && n !== null) throw TypeError("Class extends value " + String(n) + " is not a constructor or null");
			e(t, n);
			function r() {
				this.constructor = t;
			}
			t.prototype = n === null ? Object.create(n) : (r.prototype = n.prototype, new r());
		};
	})(), T = function(e) {
		Ht(t, e);
		function t(n) {
			var r = e.call(this, n) || this, i = Object.create(t.prototype);
			return Object.setPrototypeOf(r, i), Object.defineProperty(r, "name", {
				configurable: !0,
				enumerable: !1,
				value: t.name
			}), r;
		}
		return t;
	}(Error);
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/timestamp.mjs
function Wt(e) {
	var t = e.sec, n = e.nsec;
	if (t >= 0 && n >= 0 && t <= Xt) {
		if (n === 0 && t <= Yt) {
			var r = /* @__PURE__ */ new Uint8Array(4), i = new DataView(r.buffer);
			return i.setUint32(0, t), r;
		}
		var a = t / 4294967296, o = t & 4294967295, r = /* @__PURE__ */ new Uint8Array(8), i = new DataView(r.buffer);
		return i.setUint32(0, n << 2 | a & 3), i.setUint32(4, o), r;
	}
	var r = /* @__PURE__ */ new Uint8Array(12), i = new DataView(r.buffer);
	return i.setUint32(0, n), Et(i, 4, t), r;
}
function Gt(e) {
	var t = e.getTime(), n = Math.floor(t / 1e3), r = (t - n * 1e3) * 1e6, i = Math.floor(r / 1e9);
	return {
		sec: n + i,
		nsec: r - i * 1e9
	};
}
function Kt(e) {
	return e instanceof Date ? Wt(Gt(e)) : null;
}
function qt(e) {
	var t = new DataView(e.buffer, e.byteOffset, e.byteLength);
	switch (e.byteLength) {
		case 4:
			var n = t.getUint32(0), r = 0;
			return {
				sec: n,
				nsec: r
			};
		case 8:
			var i = t.getUint32(0), a = t.getUint32(4), n = (i & 3) * 4294967296 + a, r = i >>> 2;
			return {
				sec: n,
				nsec: r
			};
		case 12:
			var n = Dt(t, 4), r = t.getUint32(0);
			return {
				sec: n,
				nsec: r
			};
		default: throw new T(`Unrecognized data size for timestamp (expected 4, 8, or 12): ${e.length}`);
	}
}
function Jt(e) {
	var t = qt(e);
	return /* @__PURE__ */ new Date(t.sec * 1e3 + t.nsec / 1e6);
}
var Yt, Xt, Zt, Qt = o((() => {
	Ut(), x(), Yt = 4294967295, Xt = 17179869183, Zt = {
		type: -1,
		encode: Kt,
		decode: Jt
	};
})), $t, en = o((() => {
	Vt(), Qt(), $t = function() {
		function e() {
			this.builtInEncoders = [], this.builtInDecoders = [], this.encoders = [], this.decoders = [], this.register(Zt);
		}
		return e.prototype.register = function(e) {
			var t = e.type, n = e.encode, r = e.decode;
			if (t >= 0) this.encoders[t] = n, this.decoders[t] = r;
			else {
				var i = 1 + t;
				this.builtInEncoders[i] = n, this.builtInDecoders[i] = r;
			}
		}, e.prototype.tryToEncode = function(e, t) {
			for (var n = 0; n < this.builtInEncoders.length; n++) {
				var r = this.builtInEncoders[n];
				if (r != null) {
					var i = r(e, t);
					if (i != null) {
						var a = -1 - n;
						return new w(a, i);
					}
				}
			}
			for (var n = 0; n < this.encoders.length; n++) {
				var r = this.encoders[n];
				if (r != null) {
					var i = r(e, t);
					if (i != null) {
						var a = n;
						return new w(a, i);
					}
				}
			}
			return e instanceof w ? e : null;
		}, e.prototype.decode = function(e, t, n) {
			var r = t < 0 ? this.builtInDecoders[-1 - t] : this.decoders[t];
			return r ? r(e, t, n) : new w(t, e);
		}, e.defaultCodec = new e(), e;
	}();
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/utils/typedArrays.mjs
function E(e) {
	return e instanceof Uint8Array ? e : ArrayBuffer.isView(e) ? new Uint8Array(e.buffer, e.byteOffset, e.byteLength) : e instanceof ArrayBuffer ? new Uint8Array(e) : Uint8Array.from(e);
}
function tn(e) {
	if (e instanceof ArrayBuffer) return new DataView(e);
	var t = E(e);
	return new DataView(t.buffer, t.byteOffset, t.byteLength);
}
var nn = o((() => {})), rn, an, on = o((() => {
	Bt(), en(), x(), nn(), rn = 2048, an = function() {
		function e(e, t, n, r, i, a, o, s) {
			e === void 0 && (e = $t.defaultCodec), t === void 0 && (t = void 0), n === void 0 && (n = 100), r === void 0 && (r = rn), i === void 0 && (i = !1), a === void 0 && (a = !1), o === void 0 && (o = !1), s === void 0 && (s = !1), this.extensionCodec = e, this.context = t, this.maxDepth = n, this.initialBufferSize = r, this.sortKeys = i, this.forceFloat32 = a, this.ignoreUndefined = o, this.forceIntegerToFloat = s, this.pos = 0, this.view = new DataView(new ArrayBuffer(this.initialBufferSize)), this.bytes = new Uint8Array(this.view.buffer);
		}
		return e.prototype.reinitializeState = function() {
			this.pos = 0;
		}, e.prototype.encodeSharedRef = function(e) {
			return this.reinitializeState(), this.doEncode(e, 1), this.bytes.subarray(0, this.pos);
		}, e.prototype.encode = function(e) {
			return this.reinitializeState(), this.doEncode(e, 1), this.bytes.slice(0, this.pos);
		}, e.prototype.doEncode = function(e, t) {
			if (t > this.maxDepth) throw Error(`Too deep objects in depth ${t}`);
			e == null ? this.encodeNil() : typeof e == "boolean" ? this.encodeBoolean(e) : typeof e == "number" ? this.encodeNumber(e) : typeof e == "string" ? this.encodeString(e) : this.encodeObject(e, t);
		}, e.prototype.ensureBufferSizeToWrite = function(e) {
			var t = this.pos + e;
			this.view.byteLength < t && this.resizeBuffer(t * 2);
		}, e.prototype.resizeBuffer = function(e) {
			var t = new ArrayBuffer(e), n = new Uint8Array(t), r = new DataView(t);
			n.set(this.bytes), this.view = r, this.bytes = n;
		}, e.prototype.encodeNil = function() {
			this.writeU8(192);
		}, e.prototype.encodeBoolean = function(e) {
			e === !1 ? this.writeU8(194) : this.writeU8(195);
		}, e.prototype.encodeNumber = function(e) {
			Number.isSafeInteger(e) && !this.forceIntegerToFloat ? e >= 0 ? e < 128 ? this.writeU8(e) : e < 256 ? (this.writeU8(204), this.writeU8(e)) : e < 65536 ? (this.writeU8(205), this.writeU16(e)) : e < 4294967296 ? (this.writeU8(206), this.writeU32(e)) : (this.writeU8(207), this.writeU64(e)) : e >= -32 ? this.writeU8(224 | e + 32) : e >= -128 ? (this.writeU8(208), this.writeI8(e)) : e >= -32768 ? (this.writeU8(209), this.writeI16(e)) : e >= -2147483648 ? (this.writeU8(210), this.writeI32(e)) : (this.writeU8(211), this.writeI64(e)) : this.forceFloat32 ? (this.writeU8(202), this.writeF32(e)) : (this.writeU8(203), this.writeF64(e));
		}, e.prototype.writeStringHeader = function(e) {
			if (e < 32) this.writeU8(160 + e);
			else if (e < 256) this.writeU8(217), this.writeU8(e);
			else if (e < 65536) this.writeU8(218), this.writeU16(e);
			else if (e < 4294967296) this.writeU8(219), this.writeU32(e);
			else throw Error(`Too long string: ${e} bytes in UTF-8`);
		}, e.prototype.encodeString = function(e) {
			var t = 5;
			if (e.length > Ft) {
				var n = kt(e);
				this.ensureBufferSizeToWrite(t + n), this.writeStringHeader(n), It(e, this.bytes, this.pos), this.pos += n;
			} else {
				var n = kt(e);
				this.ensureBufferSizeToWrite(t + n), this.writeStringHeader(n), At(e, this.bytes, this.pos), this.pos += n;
			}
		}, e.prototype.encodeObject = function(e, t) {
			var n = this.extensionCodec.tryToEncode(e, this.context);
			if (n != null) this.encodeExtension(n);
			else if (Array.isArray(e)) this.encodeArray(e, t);
			else if (ArrayBuffer.isView(e)) this.encodeBinary(e);
			else if (typeof e == "object") this.encodeMap(e, t);
			else throw Error(`Unrecognized object: ${Object.prototype.toString.apply(e)}`);
		}, e.prototype.encodeBinary = function(e) {
			var t = e.byteLength;
			if (t < 256) this.writeU8(196), this.writeU8(t);
			else if (t < 65536) this.writeU8(197), this.writeU16(t);
			else if (t < 4294967296) this.writeU8(198), this.writeU32(t);
			else throw Error(`Too large binary: ${t}`);
			var n = E(e);
			this.writeU8a(n);
		}, e.prototype.encodeArray = function(e, t) {
			var n = e.length;
			if (n < 16) this.writeU8(144 + n);
			else if (n < 65536) this.writeU8(220), this.writeU16(n);
			else if (n < 4294967296) this.writeU8(221), this.writeU32(n);
			else throw Error(`Too large array: ${n}`);
			for (var r = 0, i = e; r < i.length; r++) {
				var a = i[r];
				this.doEncode(a, t + 1);
			}
		}, e.prototype.countWithoutUndefined = function(e, t) {
			for (var n = 0, r = 0, i = t; r < i.length; r++) e[i[r]] !== void 0 && n++;
			return n;
		}, e.prototype.encodeMap = function(e, t) {
			var n = Object.keys(e);
			this.sortKeys && n.sort();
			var r = this.ignoreUndefined ? this.countWithoutUndefined(e, n) : n.length;
			if (r < 16) this.writeU8(128 + r);
			else if (r < 65536) this.writeU8(222), this.writeU16(r);
			else if (r < 4294967296) this.writeU8(223), this.writeU32(r);
			else throw Error(`Too large map object: ${r}`);
			for (var i = 0, a = n; i < a.length; i++) {
				var o = a[i], s = e[o];
				this.ignoreUndefined && s === void 0 || (this.encodeString(o), this.doEncode(s, t + 1));
			}
		}, e.prototype.encodeExtension = function(e) {
			var t = e.data.length;
			if (t === 1) this.writeU8(212);
			else if (t === 2) this.writeU8(213);
			else if (t === 4) this.writeU8(214);
			else if (t === 8) this.writeU8(215);
			else if (t === 16) this.writeU8(216);
			else if (t < 256) this.writeU8(199), this.writeU8(t);
			else if (t < 65536) this.writeU8(200), this.writeU16(t);
			else if (t < 4294967296) this.writeU8(201), this.writeU32(t);
			else throw Error(`Too large extension object: ${t}`);
			this.writeI8(e.type), this.writeU8a(e.data);
		}, e.prototype.writeU8 = function(e) {
			this.ensureBufferSizeToWrite(1), this.view.setUint8(this.pos, e), this.pos++;
		}, e.prototype.writeU8a = function(e) {
			var t = e.length;
			this.ensureBufferSizeToWrite(t), this.bytes.set(e, this.pos), this.pos += t;
		}, e.prototype.writeI8 = function(e) {
			this.ensureBufferSizeToWrite(1), this.view.setInt8(this.pos, e), this.pos++;
		}, e.prototype.writeU16 = function(e) {
			this.ensureBufferSizeToWrite(2), this.view.setUint16(this.pos, e), this.pos += 2;
		}, e.prototype.writeI16 = function(e) {
			this.ensureBufferSizeToWrite(2), this.view.setInt16(this.pos, e), this.pos += 2;
		}, e.prototype.writeU32 = function(e) {
			this.ensureBufferSizeToWrite(4), this.view.setUint32(this.pos, e), this.pos += 4;
		}, e.prototype.writeI32 = function(e) {
			this.ensureBufferSizeToWrite(4), this.view.setInt32(this.pos, e), this.pos += 4;
		}, e.prototype.writeF32 = function(e) {
			this.ensureBufferSizeToWrite(4), this.view.setFloat32(this.pos, e), this.pos += 4;
		}, e.prototype.writeF64 = function(e) {
			this.ensureBufferSizeToWrite(8), this.view.setFloat64(this.pos, e), this.pos += 8;
		}, e.prototype.writeU64 = function(e) {
			this.ensureBufferSizeToWrite(8), Tt(this.view, this.pos, e), this.pos += 8;
		}, e.prototype.writeI64 = function(e) {
			this.ensureBufferSizeToWrite(8), Et(this.view, this.pos, e), this.pos += 8;
		}, e;
	}();
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/utils/prettyByte.mjs
function sn(e) {
	return `${e < 0 ? "-" : ""}0x${Math.abs(e).toString(16).padStart(2, "0")}`;
}
var cn = o((() => {})), ln, un, dn, fn = o((() => {
	Bt(), ln = 16, un = 16, dn = function() {
		function e(e, t) {
			e === void 0 && (e = ln), t === void 0 && (t = un), this.maxKeyLength = e, this.maxLengthPerKey = t, this.hit = 0, this.miss = 0, this.caches = [];
			for (var n = 0; n < this.maxKeyLength; n++) this.caches.push([]);
		}
		return e.prototype.canBeCached = function(e) {
			return e > 0 && e <= this.maxKeyLength;
		}, e.prototype.find = function(e, t, n) {
			var r = this.caches[n - 1];
			FIND_CHUNK: for (var i = 0, a = r; i < a.length; i++) {
				for (var o = a[i], s = o.bytes, c = 0; c < n; c++) if (s[c] !== e[t + c]) continue FIND_CHUNK;
				return o.str;
			}
			return null;
		}, e.prototype.store = function(e, t) {
			var n = this.caches[e.length - 1], r = {
				bytes: e,
				str: t
			};
			n.length >= this.maxLengthPerKey ? n[Math.random() * n.length | 0] = r : n.push(r);
		}, e.prototype.decode = function(e, t, n) {
			var r = this.find(e, t, n);
			if (r != null) return this.hit++, r;
			this.miss++;
			var i = Nt(e, t, n), a = Uint8Array.prototype.slice.call(e, t, t + n);
			return this.store(a, i), i;
		}, e;
	}();
})), pn, D, mn, O, hn, gn, k, _n, vn, yn, bn, xn, Sn, Cn = o((() => {
	cn(), en(), x(), Bt(), nn(), fn(), Ut(), pn = function(e, t, n, r) {
		function i(e) {
			return e instanceof n ? e : new n(function(t) {
				t(e);
			});
		}
		return new (n ||= Promise)(function(n, a) {
			function o(e) {
				try {
					c(r.next(e));
				} catch (e) {
					a(e);
				}
			}
			function s(e) {
				try {
					c(r.throw(e));
				} catch (e) {
					a(e);
				}
			}
			function c(e) {
				e.done ? n(e.value) : i(e.value).then(o, s);
			}
			c((r = r.apply(e, t || [])).next());
		});
	}, D = function(e, t) {
		var n = {
			label: 0,
			sent: function() {
				if (a[0] & 1) throw a[1];
				return a[1];
			},
			trys: [],
			ops: []
		}, r, i, a, o;
		return o = {
			next: s(0),
			throw: s(1),
			return: s(2)
		}, typeof Symbol == "function" && (o[Symbol.iterator] = function() {
			return this;
		}), o;
		function s(e) {
			return function(t) {
				return c([e, t]);
			};
		}
		function c(o) {
			if (r) throw TypeError("Generator is already executing.");
			for (; n;) try {
				if (r = 1, i && (a = o[0] & 2 ? i.return : o[0] ? i.throw || ((a = i.return) && a.call(i), 0) : i.next) && !(a = a.call(i, o[1])).done) return a;
				switch (i = 0, a && (o = [o[0] & 2, a.value]), o[0]) {
					case 0:
					case 1:
						a = o;
						break;
					case 4: return n.label++, {
						value: o[1],
						done: !1
					};
					case 5:
						n.label++, i = o[1], o = [0];
						continue;
					case 7:
						o = n.ops.pop(), n.trys.pop();
						continue;
					default:
						if (a = n.trys, !(a = a.length > 0 && a[a.length - 1]) && (o[0] === 6 || o[0] === 2)) {
							n = 0;
							continue;
						}
						if (o[0] === 3 && (!a || o[1] > a[0] && o[1] < a[3])) {
							n.label = o[1];
							break;
						}
						if (o[0] === 6 && n.label < a[1]) {
							n.label = a[1], a = o;
							break;
						}
						if (a && n.label < a[2]) {
							n.label = a[2], n.ops.push(o);
							break;
						}
						a[2] && n.ops.pop(), n.trys.pop();
						continue;
				}
				o = t.call(e, n);
			} catch (e) {
				o = [6, e], i = 0;
			} finally {
				r = a = 0;
			}
			if (o[0] & 5) throw o[1];
			return {
				value: o[0] ? o[1] : void 0,
				done: !0
			};
		}
	}, mn = function(e) {
		if (!Symbol.asyncIterator) throw TypeError("Symbol.asyncIterator is not defined.");
		var t = e[Symbol.asyncIterator], n;
		return t ? t.call(e) : (e = typeof __values == "function" ? __values(e) : e[Symbol.iterator](), n = {}, r("next"), r("throw"), r("return"), n[Symbol.asyncIterator] = function() {
			return this;
		}, n);
		function r(t) {
			n[t] = e[t] && function(n) {
				return new Promise(function(r, a) {
					n = e[t](n), i(r, a, n.done, n.value);
				});
			};
		}
		function i(e, t, n, r) {
			Promise.resolve(r).then(function(t) {
				e({
					value: t,
					done: n
				});
			}, t);
		}
	}, O = function(e) {
		return this instanceof O ? (this.v = e, this) : new O(e);
	}, hn = function(e, t, n) {
		if (!Symbol.asyncIterator) throw TypeError("Symbol.asyncIterator is not defined.");
		var r = n.apply(e, t || []), i, a = [];
		return i = {}, o("next"), o("throw"), o("return"), i[Symbol.asyncIterator] = function() {
			return this;
		}, i;
		function o(e) {
			r[e] && (i[e] = function(t) {
				return new Promise(function(n, r) {
					a.push([
						e,
						t,
						n,
						r
					]) > 1 || s(e, t);
				});
			});
		}
		function s(e, t) {
			try {
				c(r[e](t));
			} catch (e) {
				d(a[0][3], e);
			}
		}
		function c(e) {
			e.value instanceof O ? Promise.resolve(e.value.v).then(l, u) : d(a[0][2], e);
		}
		function l(e) {
			s("next", e);
		}
		function u(e) {
			s("throw", e);
		}
		function d(e, t) {
			e(t), a.shift(), a.length && s(a[0][0], a[0][1]);
		}
	}, gn = function(e) {
		var t = typeof e;
		return t === "string" || t === "number";
	}, k = -1, _n = /* @__PURE__ */ new DataView(/* @__PURE__ */ new ArrayBuffer(0)), vn = new Uint8Array(_n.buffer), yn = (function() {
		try {
			_n.getInt8(0);
		} catch (e) {
			return e.constructor;
		}
		throw Error("never reached");
	})(), bn = new yn("Insufficient data"), xn = new dn(), Sn = function() {
		function e(e, t, n, r, i, a, o, s) {
			e === void 0 && (e = $t.defaultCodec), t === void 0 && (t = void 0), n === void 0 && (n = b), r === void 0 && (r = b), i === void 0 && (i = b), a === void 0 && (a = b), o === void 0 && (o = b), s === void 0 && (s = xn), this.extensionCodec = e, this.context = t, this.maxStrLength = n, this.maxBinLength = r, this.maxArrayLength = i, this.maxMapLength = a, this.maxExtLength = o, this.keyDecoder = s, this.totalPos = 0, this.pos = 0, this.view = _n, this.bytes = vn, this.headByte = k, this.stack = [];
		}
		return e.prototype.reinitializeState = function() {
			this.totalPos = 0, this.headByte = k, this.stack.length = 0;
		}, e.prototype.setBuffer = function(e) {
			this.bytes = E(e), this.view = tn(this.bytes), this.pos = 0;
		}, e.prototype.appendBuffer = function(e) {
			if (this.headByte === k && !this.hasRemaining(1)) this.setBuffer(e);
			else {
				var t = this.bytes.subarray(this.pos), n = E(e), r = new Uint8Array(t.length + n.length);
				r.set(t), r.set(n, t.length), this.setBuffer(r);
			}
		}, e.prototype.hasRemaining = function(e) {
			return this.view.byteLength - this.pos >= e;
		}, e.prototype.createExtraByteError = function(e) {
			var t = this, n = t.view, r = t.pos;
			return /* @__PURE__ */ RangeError(`Extra ${n.byteLength - r} of ${n.byteLength} byte(s) found at buffer[${e}]`);
		}, e.prototype.decode = function(e) {
			this.reinitializeState(), this.setBuffer(e);
			var t = this.doDecodeSync();
			if (this.hasRemaining(1)) throw this.createExtraByteError(this.pos);
			return t;
		}, e.prototype.decodeMulti = function(e) {
			return D(this, function(t) {
				switch (t.label) {
					case 0: this.reinitializeState(), this.setBuffer(e), t.label = 1;
					case 1: return this.hasRemaining(1) ? [4, this.doDecodeSync()] : [3, 3];
					case 2: return t.sent(), [3, 1];
					case 3: return [2];
				}
			});
		}, e.prototype.decodeAsync = function(e) {
			var t, n, r, i;
			return pn(this, void 0, void 0, function() {
				var a, o, s, c, l, u, d, f;
				return D(this, function(p) {
					switch (p.label) {
						case 0: a = !1, p.label = 1;
						case 1: p.trys.push([
							1,
							6,
							7,
							12
						]), t = mn(e), p.label = 2;
						case 2: return [4, t.next()];
						case 3:
							if (n = p.sent(), n.done) return [3, 5];
							if (s = n.value, a) throw this.createExtraByteError(this.totalPos);
							this.appendBuffer(s);
							try {
								o = this.doDecodeSync(), a = !0;
							} catch (e) {
								if (!(e instanceof yn)) throw e;
							}
							this.totalPos += this.pos, p.label = 4;
						case 4: return [3, 2];
						case 5: return [3, 12];
						case 6: return c = p.sent(), r = { error: c }, [3, 12];
						case 7: return p.trys.push([
							7,
							,
							10,
							11
						]), n && !n.done && (i = t.return) ? [4, i.call(t)] : [3, 9];
						case 8: p.sent(), p.label = 9;
						case 9: return [3, 11];
						case 10:
							if (r) throw r.error;
							return [7];
						case 11: return [7];
						case 12:
							if (a) {
								if (this.hasRemaining(1)) throw this.createExtraByteError(this.totalPos);
								return [2, o];
							}
							throw l = this, u = l.headByte, d = l.pos, f = l.totalPos, RangeError(`Insufficient data in parsing ${sn(u)} at ${f} (${d} in the current buffer)`);
					}
				});
			});
		}, e.prototype.decodeArrayStream = function(e) {
			return this.decodeMultiAsync(e, !0);
		}, e.prototype.decodeStream = function(e) {
			return this.decodeMultiAsync(e, !1);
		}, e.prototype.decodeMultiAsync = function(e, t) {
			return hn(this, arguments, function() {
				var n, r, i, a, o, s, c, l, u;
				return D(this, function(d) {
					switch (d.label) {
						case 0: n = t, r = -1, d.label = 1;
						case 1: d.trys.push([
							1,
							13,
							14,
							19
						]), i = mn(e), d.label = 2;
						case 2: return [4, O(i.next())];
						case 3:
							if (a = d.sent(), a.done) return [3, 12];
							if (o = a.value, t && r === 0) throw this.createExtraByteError(this.totalPos);
							this.appendBuffer(o), n && (r = this.readArraySize(), n = !1, this.complete()), d.label = 4;
						case 4: d.trys.push([
							4,
							9,
							,
							10
						]), d.label = 5;
						case 5: return [4, O(this.doDecodeSync())];
						case 6: return [4, d.sent()];
						case 7: return d.sent(), --r === 0 ? [3, 8] : [3, 5];
						case 8: return [3, 10];
						case 9:
							if (s = d.sent(), !(s instanceof yn)) throw s;
							return [3, 10];
						case 10: this.totalPos += this.pos, d.label = 11;
						case 11: return [3, 2];
						case 12: return [3, 19];
						case 13: return c = d.sent(), l = { error: c }, [3, 19];
						case 14: return d.trys.push([
							14,
							,
							17,
							18
						]), a && !a.done && (u = i.return) ? [4, O(u.call(i))] : [3, 16];
						case 15: d.sent(), d.label = 16;
						case 16: return [3, 18];
						case 17:
							if (l) throw l.error;
							return [7];
						case 18: return [7];
						case 19: return [2];
					}
				});
			});
		}, e.prototype.doDecodeSync = function() {
			DECODE: for (;;) {
				var e = this.readHeadByte(), t = void 0;
				if (e >= 224) t = e - 256;
				else if (e < 192) {
					if (e < 128) t = e;
					else if (e < 144) {
						var n = e - 128;
						if (n !== 0) {
							this.pushMapState(n), this.complete();
							continue DECODE;
						}
						t = {};
					} else if (e < 160) {
						var n = e - 144;
						if (n !== 0) {
							this.pushArrayState(n), this.complete();
							continue DECODE;
						}
						t = [];
					} else {
						var r = e - 160;
						t = this.decodeUtf8String(r, 0);
					}
				} else if (e === 192) t = null;
				else if (e === 194) t = !1;
				else if (e === 195) t = !0;
				else if (e === 202) t = this.readF32();
				else if (e === 203) t = this.readF64();
				else if (e === 204) t = this.readU8();
				else if (e === 205) t = this.readU16();
				else if (e === 206) t = this.readU32();
				else if (e === 207) t = this.readU64();
				else if (e === 208) t = this.readI8();
				else if (e === 209) t = this.readI16();
				else if (e === 210) t = this.readI32();
				else if (e === 211) t = this.readI64();
				else if (e === 217) {
					var r = this.lookU8();
					t = this.decodeUtf8String(r, 1);
				} else if (e === 218) {
					var r = this.lookU16();
					t = this.decodeUtf8String(r, 2);
				} else if (e === 219) {
					var r = this.lookU32();
					t = this.decodeUtf8String(r, 4);
				} else if (e === 220) {
					var n = this.readU16();
					if (n !== 0) {
						this.pushArrayState(n), this.complete();
						continue DECODE;
					}
					t = [];
				} else if (e === 221) {
					var n = this.readU32();
					if (n !== 0) {
						this.pushArrayState(n), this.complete();
						continue DECODE;
					}
					t = [];
				} else if (e === 222) {
					var n = this.readU16();
					if (n !== 0) {
						this.pushMapState(n), this.complete();
						continue DECODE;
					}
					t = {};
				} else if (e === 223) {
					var n = this.readU32();
					if (n !== 0) {
						this.pushMapState(n), this.complete();
						continue DECODE;
					}
					t = {};
				} else if (e === 196) {
					var n = this.lookU8();
					t = this.decodeBinary(n, 1);
				} else if (e === 197) {
					var n = this.lookU16();
					t = this.decodeBinary(n, 2);
				} else if (e === 198) {
					var n = this.lookU32();
					t = this.decodeBinary(n, 4);
				} else if (e === 212) t = this.decodeExtension(1, 0);
				else if (e === 213) t = this.decodeExtension(2, 0);
				else if (e === 214) t = this.decodeExtension(4, 0);
				else if (e === 215) t = this.decodeExtension(8, 0);
				else if (e === 216) t = this.decodeExtension(16, 0);
				else if (e === 199) {
					var n = this.lookU8();
					t = this.decodeExtension(n, 1);
				} else if (e === 200) {
					var n = this.lookU16();
					t = this.decodeExtension(n, 2);
				} else if (e === 201) {
					var n = this.lookU32();
					t = this.decodeExtension(n, 4);
				} else throw new T(`Unrecognized type byte: ${sn(e)}`);
				this.complete();
				for (var i = this.stack; i.length > 0;) {
					var a = i[i.length - 1];
					if (a.type === 0) {
						if (a.array[a.position] = t, a.position++, a.position === a.size) i.pop(), t = a.array;
						else continue DECODE;
					} else if (a.type === 1) {
						if (!gn(t)) throw new T("The type of key must be string or number but " + typeof t);
						if (t === "__proto__") throw new T("The key __proto__ is not allowed");
						a.key = t, a.type = 2;
						continue DECODE;
					} else if (a.map[a.key] = t, a.readCount++, a.readCount === a.size) i.pop(), t = a.map;
					else {
						a.key = null, a.type = 1;
						continue DECODE;
					}
				}
				return t;
			}
		}, e.prototype.readHeadByte = function() {
			return this.headByte === k && (this.headByte = this.readU8()), this.headByte;
		}, e.prototype.complete = function() {
			this.headByte = k;
		}, e.prototype.readArraySize = function() {
			var e = this.readHeadByte();
			switch (e) {
				case 220: return this.readU16();
				case 221: return this.readU32();
				default:
					if (e < 160) return e - 144;
					throw new T(`Unrecognized array type byte: ${sn(e)}`);
			}
		}, e.prototype.pushMapState = function(e) {
			if (e > this.maxMapLength) throw new T(`Max length exceeded: map length (${e}) > maxMapLengthLength (${this.maxMapLength})`);
			this.stack.push({
				type: 1,
				size: e,
				key: null,
				readCount: 0,
				map: {}
			});
		}, e.prototype.pushArrayState = function(e) {
			if (e > this.maxArrayLength) throw new T(`Max length exceeded: array length (${e}) > maxArrayLength (${this.maxArrayLength})`);
			this.stack.push({
				type: 0,
				size: e,
				array: Array(e),
				position: 0
			});
		}, e.prototype.decodeUtf8String = function(e, t) {
			if (e > this.maxStrLength) throw new T(`Max length exceeded: UTF-8 byte length (${e}) > maxStrLength (${this.maxStrLength})`);
			if (this.bytes.byteLength < this.pos + t + e) throw bn;
			var n = this.pos + t, r = this.stateIsMapKey() && this.keyDecoder?.canBeCached(e) ? this.keyDecoder.decode(this.bytes, n, e) : e > zt ? Pt(this.bytes, n, e) : Nt(this.bytes, n, e);
			return this.pos += t + e, r;
		}, e.prototype.stateIsMapKey = function() {
			return this.stack.length > 0 && this.stack[this.stack.length - 1].type === 1;
		}, e.prototype.decodeBinary = function(e, t) {
			if (e > this.maxBinLength) throw new T(`Max length exceeded: bin length (${e}) > maxBinLength (${this.maxBinLength})`);
			if (!this.hasRemaining(e + t)) throw bn;
			var n = this.pos + t, r = this.bytes.subarray(n, n + e);
			return this.pos += t + e, r;
		}, e.prototype.decodeExtension = function(e, t) {
			if (e > this.maxExtLength) throw new T(`Max length exceeded: ext length (${e}) > maxExtLength (${this.maxExtLength})`);
			var n = this.view.getInt8(this.pos + t), r = this.decodeBinary(e, t + 1);
			return this.extensionCodec.decode(r, n, this.context);
		}, e.prototype.lookU8 = function() {
			return this.view.getUint8(this.pos);
		}, e.prototype.lookU16 = function() {
			return this.view.getUint16(this.pos);
		}, e.prototype.lookU32 = function() {
			return this.view.getUint32(this.pos);
		}, e.prototype.readU8 = function() {
			var e = this.view.getUint8(this.pos);
			return this.pos++, e;
		}, e.prototype.readI8 = function() {
			var e = this.view.getInt8(this.pos);
			return this.pos++, e;
		}, e.prototype.readU16 = function() {
			var e = this.view.getUint16(this.pos);
			return this.pos += 2, e;
		}, e.prototype.readI16 = function() {
			var e = this.view.getInt16(this.pos);
			return this.pos += 2, e;
		}, e.prototype.readU32 = function() {
			var e = this.view.getUint32(this.pos);
			return this.pos += 4, e;
		}, e.prototype.readI32 = function() {
			var e = this.view.getInt32(this.pos);
			return this.pos += 4, e;
		}, e.prototype.readU64 = function() {
			var e = Ot(this.view, this.pos);
			return this.pos += 8, e;
		}, e.prototype.readI64 = function() {
			var e = Dt(this.view, this.pos);
			return this.pos += 8, e;
		}, e.prototype.readF32 = function() {
			var e = this.view.getFloat32(this.pos);
			return this.pos += 4, e;
		}, e.prototype.readF64 = function() {
			var e = this.view.getFloat64(this.pos);
			return this.pos += 8, e;
		}, e;
	}();
})), wn, Tn = o((() => {
	wn = {};
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/utils/stream.mjs
function En(e) {
	return e[Symbol.asyncIterator] != null;
}
function Dn(e) {
	if (e == null) throw Error("Assertion Failure: value must not be null nor undefined");
}
function On(e) {
	return jn(this, arguments, function() {
		var t, n, r, i;
		return An(this, function(a) {
			switch (a.label) {
				case 0: t = e.getReader(), a.label = 1;
				case 1: a.trys.push([
					1,
					,
					9,
					10
				]), a.label = 2;
				case 2: return [4, A(t.read())];
				case 3: return n = a.sent(), r = n.done, i = n.value, r ? [4, A(void 0)] : [3, 5];
				case 4: return [2, a.sent()];
				case 5: return Dn(i), [4, A(i)];
				case 6: return [4, a.sent()];
				case 7: return a.sent(), [3, 2];
				case 8: return [3, 10];
				case 9: return t.releaseLock(), [7];
				case 10: return [2];
			}
		});
	});
}
function kn(e) {
	return En(e) ? e : On(e);
}
var An, A, jn, Mn = o((() => {
	An = function(e, t) {
		var n = {
			label: 0,
			sent: function() {
				if (a[0] & 1) throw a[1];
				return a[1];
			},
			trys: [],
			ops: []
		}, r, i, a, o;
		return o = {
			next: s(0),
			throw: s(1),
			return: s(2)
		}, typeof Symbol == "function" && (o[Symbol.iterator] = function() {
			return this;
		}), o;
		function s(e) {
			return function(t) {
				return c([e, t]);
			};
		}
		function c(o) {
			if (r) throw TypeError("Generator is already executing.");
			for (; n;) try {
				if (r = 1, i && (a = o[0] & 2 ? i.return : o[0] ? i.throw || ((a = i.return) && a.call(i), 0) : i.next) && !(a = a.call(i, o[1])).done) return a;
				switch (i = 0, a && (o = [o[0] & 2, a.value]), o[0]) {
					case 0:
					case 1:
						a = o;
						break;
					case 4: return n.label++, {
						value: o[1],
						done: !1
					};
					case 5:
						n.label++, i = o[1], o = [0];
						continue;
					case 7:
						o = n.ops.pop(), n.trys.pop();
						continue;
					default:
						if (a = n.trys, !(a = a.length > 0 && a[a.length - 1]) && (o[0] === 6 || o[0] === 2)) {
							n = 0;
							continue;
						}
						if (o[0] === 3 && (!a || o[1] > a[0] && o[1] < a[3])) {
							n.label = o[1];
							break;
						}
						if (o[0] === 6 && n.label < a[1]) {
							n.label = a[1], a = o;
							break;
						}
						if (a && n.label < a[2]) {
							n.label = a[2], n.ops.push(o);
							break;
						}
						a[2] && n.ops.pop(), n.trys.pop();
						continue;
				}
				o = t.call(e, n);
			} catch (e) {
				o = [6, e], i = 0;
			} finally {
				r = a = 0;
			}
			if (o[0] & 5) throw o[1];
			return {
				value: o[0] ? o[1] : void 0,
				done: !0
			};
		}
	}, A = function(e) {
		return this instanceof A ? (this.v = e, this) : new A(e);
	}, jn = function(e, t, n) {
		if (!Symbol.asyncIterator) throw TypeError("Symbol.asyncIterator is not defined.");
		var r = n.apply(e, t || []), i, a = [];
		return i = {}, o("next"), o("throw"), o("return"), i[Symbol.asyncIterator] = function() {
			return this;
		}, i;
		function o(e) {
			r[e] && (i[e] = function(t) {
				return new Promise(function(n, r) {
					a.push([
						e,
						t,
						n,
						r
					]) > 1 || s(e, t);
				});
			});
		}
		function s(e, t) {
			try {
				c(r[e](t));
			} catch (e) {
				d(a[0][3], e);
			}
		}
		function c(e) {
			e.value instanceof A ? Promise.resolve(e.value.v).then(l, u) : d(a[0][2], e);
		}
		function l(e) {
			s("next", e);
		}
		function u(e) {
			s("throw", e);
		}
		function d(e, t) {
			e(t), a.shift(), a.length && s(a[0][0], a[0][1]);
		}
	};
}));
//#endregion
//#region node_modules/@msgpack/msgpack/dist.es5+esm/decodeAsync.mjs
function Nn(e, t) {
	t === void 0 && (t = wn);
	var n = kn(e);
	return new Sn(t.extensionCodec, t.context, t.maxStrLength, t.maxBinLength, t.maxArrayLength, t.maxMapLength, t.maxExtLength).decodeStream(n);
}
var Pn = o((() => {
	Cn(), Mn(), Tn();
})), Fn = o((() => {
	Pn(), on();
})), In = /* @__PURE__ */ c({
	BaseConnectionErrorType: () => H,
	BufferedConnection: () => J,
	ConnectionType: () => B,
	DataConnectionErrorType: () => U,
	MsgPack: () => lr,
	MsgPackPeer: () => ur,
	Peer: () => sr,
	PeerError: () => er,
	PeerErrorType: () => V,
	SerializationType: () => W,
	ServerMessageType: () => K,
	SocketEventType: () => G,
	StreamConnection: () => cr,
	default: () => dr,
	util: () => F
});
function j(e, t, n, r) {
	Object.defineProperty(e, t, {
		get: n,
		set: r,
		enumerable: !0,
		configurable: !0
	});
}
function Ln(e) {
	let t = 0;
	for (let n of e) t += n.byteLength;
	let n = new Uint8Array(t), r = 0;
	for (let t of e) n.set(t, r), r += t.byteLength;
	return n;
}
function M() {}
function Rn(e, t, n) {
	this.fn = e, this.context = t, this.once = n || !1;
}
function zn(e, t, n, r, i) {
	if (typeof n != "function") throw TypeError("The listener must be a function");
	var a = new Rn(n, r || e, i), o = R ? R + t : t;
	return e._events[o] ? e._events[o].fn ? e._events[o] = [e._events[o], a] : e._events[o].push(a) : (e._events[o] = a, e._eventsCount++), e;
}
function Bn(e, t) {
	--e._eventsCount === 0 ? e._events = new M() : delete e._events[t];
}
function N() {
	this._events = new M(), this._eventsCount = 0;
}
var Vn, Hn, P, Un, Wn, Gn, Kn, F, qn, Jn, I, L, Yn, R, z, B, V, H, U, W, G, K, Xn, Zn, Qn, $n, er, tr, nr, rr, q, J, ir, ar, or, sr, cr, lr, ur, dr, fr = o((() => {
	fe(), wt(), Fn(), Vn = class {
		constructor() {
			this.chunkedMTU = 16300, this._dataCount = 1, this.chunk = (e) => {
				let t = [], n = e.byteLength, r = Math.ceil(n / this.chunkedMTU), i = 0, a = 0;
				for (; a < n;) {
					let o = Math.min(n, a + this.chunkedMTU), s = e.slice(a, o), c = {
						__peerData: this._dataCount,
						n: i,
						data: s,
						total: r
					};
					t.push(c), a = o, i++;
				}
				return this._dataCount++, t;
			};
		}
	}, Hn = Ct.default || Ct, P = new class {
		isWebRTCSupported() {
			return typeof RTCPeerConnection < "u";
		}
		isBrowserSupported() {
			let e = this.getBrowser(), t = this.getVersion();
			return this.supportedBrowsers.includes(e) ? e === "chrome" ? t >= this.minChromeVersion : e === "firefox" ? t >= this.minFirefoxVersion : e === "safari" && !this.isIOS && t >= this.minSafariVersion : !1;
		}
		getBrowser() {
			return Hn.browserDetails.browser;
		}
		getVersion() {
			return Hn.browserDetails.version || 0;
		}
		isUnifiedPlanSupported() {
			let e = this.getBrowser(), t = Hn.browserDetails.version || 0;
			if (e === "chrome" && t < this.minChromeVersion) return !1;
			if (e === "firefox" && t >= this.minFirefoxVersion) return !0;
			if (!window.RTCRtpTransceiver || !("currentDirection" in RTCRtpTransceiver.prototype)) return !1;
			let n, r = !1;
			try {
				n = new RTCPeerConnection(), n.addTransceiver("audio"), r = !0;
			} catch {} finally {
				n && n.close();
			}
			return r;
		}
		toString() {
			return `Supports:
    browser:${this.getBrowser()}
    version:${this.getVersion()}
    isIOS:${this.isIOS}
    isWebRTCSupported:${this.isWebRTCSupported()}
    isBrowserSupported:${this.isBrowserSupported()}
    isUnifiedPlanSupported:${this.isUnifiedPlanSupported()}`;
		}
		constructor() {
			this.isIOS = typeof navigator < "u" && [
				"iPad",
				"iPhone",
				"iPod"
			].includes(navigator.platform), this.supportedBrowsers = [
				"firefox",
				"chrome",
				"safari"
			], this.minFirefoxVersion = 59, this.minChromeVersion = 72, this.minSafariVersion = 605;
		}
	}(), Un = (e) => !e || /^[A-Za-z0-9]+(?:[ _-][A-Za-z0-9]+)*$/.test(e), Wn = () => Math.random().toString(36).slice(2), Gn = {
		iceServers: [{ urls: "stun:stun.l.google.com:19302" }, {
			urls: ["turn:eu-0.turn.peerjs.com:3478", "turn:us-0.turn.peerjs.com:3478"],
			username: "peerjs",
			credential: "peerjsp"
		}],
		sdpSemantics: "unified-plan"
	}, Kn = class extends Vn {
		noop() {}
		blobToArrayBuffer(e, t) {
			let n = new FileReader();
			return n.onload = function(e) {
				e.target && t(e.target.result);
			}, n.readAsArrayBuffer(e), n;
		}
		binaryStringToArrayBuffer(e) {
			let t = new Uint8Array(e.length);
			for (let n = 0; n < e.length; n++) t[n] = e.charCodeAt(n) & 255;
			return t.buffer;
		}
		isSecure() {
			return location.protocol === "https:";
		}
		constructor(...e) {
			super(...e), this.CLOUD_HOST = "0.peerjs.com", this.CLOUD_PORT = 443, this.chunkedBrowsers = {
				Chrome: 1,
				chrome: 1
			}, this.defaultConfig = Gn, this.browser = P.getBrowser(), this.browserVersion = P.getVersion(), this.pack = ce, this.unpack = se, this.supports = function() {
				let e = {
					browser: P.isBrowserSupported(),
					webRTC: P.isWebRTCSupported(),
					audioVideo: !1,
					data: !1,
					binaryBlob: !1,
					reliable: !1
				};
				if (!e.webRTC) return e;
				let t;
				try {
					t = new RTCPeerConnection(Gn), e.audioVideo = !0;
					let n;
					try {
						n = t.createDataChannel("_PEERJSTEST", { ordered: !0 }), e.data = !0, e.reliable = !!n.ordered;
						try {
							n.binaryType = "blob", e.binaryBlob = !P.isIOS;
						} catch {}
					} catch {} finally {
						n && n.close();
					}
				} catch {} finally {
					t && t.close();
				}
				return e;
			}(), this.validateId = Un, this.randomToken = Wn;
		}
	}, F = new Kn(), qn = "PeerJS: ", Jn = class {
		get logLevel() {
			return this._logLevel;
		}
		set logLevel(e) {
			this._logLevel = e;
		}
		log(...e) {
			this._logLevel >= 3 && this._print(3, ...e);
		}
		warn(...e) {
			this._logLevel >= 2 && this._print(2, ...e);
		}
		error(...e) {
			this._logLevel >= 1 && this._print(1, ...e);
		}
		setLogFunction(e) {
			this._print = e;
		}
		_print(e, ...t) {
			let n = [qn, ...t];
			for (let e in n) n[e] instanceof Error && (n[e] = "(" + n[e].name + ") " + n[e].message);
			e >= 3 ? console.log(...n) : e >= 2 ? console.warn("WARNING", ...n) : e >= 1 && console.error("ERROR", ...n);
		}
		constructor() {
			this._logLevel = 0;
		}
	}, I = new Jn(), L = {}, Yn = Object.prototype.hasOwnProperty, R = "~", Object.create && (M.prototype = Object.create(null), new M().__proto__ || (R = !1)), N.prototype.eventNames = function() {
		var e = [], t, n;
		if (this._eventsCount === 0) return e;
		for (n in t = this._events) Yn.call(t, n) && e.push(R ? n.slice(1) : n);
		return Object.getOwnPropertySymbols ? e.concat(Object.getOwnPropertySymbols(t)) : e;
	}, N.prototype.listeners = function(e) {
		var t = R ? R + e : e, n = this._events[t];
		if (!n) return [];
		if (n.fn) return [n.fn];
		for (var r = 0, i = n.length, a = Array(i); r < i; r++) a[r] = n[r].fn;
		return a;
	}, N.prototype.listenerCount = function(e) {
		var t = R ? R + e : e, n = this._events[t];
		return n ? n.fn ? 1 : n.length : 0;
	}, N.prototype.emit = function(e, t, n, r, i, a) {
		var o = R ? R + e : e;
		if (!this._events[o]) return !1;
		var s = this._events[o], c = arguments.length, l, u;
		if (s.fn) {
			switch (s.once && this.removeListener(e, s.fn, void 0, !0), c) {
				case 1: return s.fn.call(s.context), !0;
				case 2: return s.fn.call(s.context, t), !0;
				case 3: return s.fn.call(s.context, t, n), !0;
				case 4: return s.fn.call(s.context, t, n, r), !0;
				case 5: return s.fn.call(s.context, t, n, r, i), !0;
				case 6: return s.fn.call(s.context, t, n, r, i, a), !0;
			}
			for (u = 1, l = Array(c - 1); u < c; u++) l[u - 1] = arguments[u];
			s.fn.apply(s.context, l);
		} else {
			var d = s.length, f;
			for (u = 0; u < d; u++) switch (s[u].once && this.removeListener(e, s[u].fn, void 0, !0), c) {
				case 1:
					s[u].fn.call(s[u].context);
					break;
				case 2:
					s[u].fn.call(s[u].context, t);
					break;
				case 3:
					s[u].fn.call(s[u].context, t, n);
					break;
				case 4:
					s[u].fn.call(s[u].context, t, n, r);
					break;
				default:
					if (!l) for (f = 1, l = Array(c - 1); f < c; f++) l[f - 1] = arguments[f];
					s[u].fn.apply(s[u].context, l);
			}
		}
		return !0;
	}, N.prototype.on = function(e, t, n) {
		return zn(this, e, t, n, !1);
	}, N.prototype.once = function(e, t, n) {
		return zn(this, e, t, n, !0);
	}, N.prototype.removeListener = function(e, t, n, r) {
		var i = R ? R + e : e;
		if (!this._events[i]) return this;
		if (!t) return Bn(this, i), this;
		var a = this._events[i];
		if (a.fn) a.fn === t && (!r || a.once) && (!n || a.context === n) && Bn(this, i);
		else {
			for (var o = 0, s = [], c = a.length; o < c; o++) (a[o].fn !== t || r && !a[o].once || n && a[o].context !== n) && s.push(a[o]);
			s.length ? this._events[i] = s.length === 1 ? s[0] : s : Bn(this, i);
		}
		return this;
	}, N.prototype.removeAllListeners = function(e) {
		var t;
		return e ? (t = R ? R + e : e, this._events[t] && Bn(this, t)) : (this._events = new M(), this._eventsCount = 0), this;
	}, N.prototype.off = N.prototype.removeListener, N.prototype.addListener = N.prototype.on, N.prefixed = R, N.EventEmitter = N, L = N, z = {}, j(z, "ConnectionType", () => B), j(z, "PeerErrorType", () => V), j(z, "BaseConnectionErrorType", () => H), j(z, "DataConnectionErrorType", () => U), j(z, "SerializationType", () => W), j(z, "SocketEventType", () => G), j(z, "ServerMessageType", () => K), B = /*#__PURE__*/ function(e) {
		return e.Data = "data", e.Media = "media", e;
	}({}), V = /*#__PURE__*/ function(e) {
		return e.BrowserIncompatible = "browser-incompatible", e.Disconnected = "disconnected", e.InvalidID = "invalid-id", e.InvalidKey = "invalid-key", e.Network = "network", e.PeerUnavailable = "peer-unavailable", e.SslUnavailable = "ssl-unavailable", e.ServerError = "server-error", e.SocketError = "socket-error", e.SocketClosed = "socket-closed", e.UnavailableID = "unavailable-id", e.WebRTC = "webrtc", e;
	}({}), H = /*#__PURE__*/ function(e) {
		return e.NegotiationFailed = "negotiation-failed", e.ConnectionClosed = "connection-closed", e;
	}({}), U = /*#__PURE__*/ function(e) {
		return e.NotOpenYet = "not-open-yet", e.MessageToBig = "message-too-big", e;
	}({}), W = /*#__PURE__*/ function(e) {
		return e.Binary = "binary", e.BinaryUTF8 = "binary-utf8", e.JSON = "json", e.None = "raw", e;
	}({}), G = /*#__PURE__*/ function(e) {
		return e.Message = "message", e.Disconnected = "disconnected", e.Error = "error", e.Close = "close", e;
	}({}), K = /*#__PURE__*/ function(e) {
		return e.Heartbeat = "HEARTBEAT", e.Candidate = "CANDIDATE", e.Offer = "OFFER", e.Answer = "ANSWER", e.Open = "OPEN", e.Error = "ERROR", e.IdTaken = "ID-TAKEN", e.InvalidKey = "INVALID-KEY", e.Leave = "LEAVE", e.Expire = "EXPIRE", e;
	}({}), Xn = "1.5.5", Zn = class extends L.EventEmitter {
		constructor(e, t, n, r, i, a = 5e3) {
			super(), this.pingInterval = a, this._disconnected = !0, this._messagesQueue = [];
			let o = e ? "wss://" : "ws://";
			this._baseUrl = o + t + ":" + n + r + "peerjs?key=" + i;
		}
		start(e, t) {
			this._id = e;
			let n = `${this._baseUrl}&id=${e}&token=${t}`;
			this._socket || !this._disconnected || (this._socket = new WebSocket(n + "&version=1.5.5"), this._disconnected = !1, this._socket.onmessage = (e) => {
				let t;
				try {
					t = JSON.parse(e.data), I.log("Server message received:", t);
				} catch {
					I.log("Invalid server message", e.data);
					return;
				}
				this.emit(G.Message, t);
			}, this._socket.onclose = (e) => {
				this._disconnected || (I.log("Socket closed.", e), this._cleanup(), this._disconnected = !0, this.emit(G.Disconnected));
			}, this._socket.onopen = () => {
				this._disconnected || (this._sendQueuedMessages(), I.log("Socket open"), this._scheduleHeartbeat());
			});
		}
		_scheduleHeartbeat() {
			this._wsPingTimer = setTimeout(() => {
				this._sendHeartbeat();
			}, this.pingInterval);
		}
		_sendHeartbeat() {
			if (!this._wsOpen()) {
				I.log("Cannot send heartbeat, because socket closed");
				return;
			}
			let e = JSON.stringify({ type: K.Heartbeat });
			this._socket.send(e), this._scheduleHeartbeat();
		}
		_wsOpen() {
			return !!this._socket && this._socket.readyState === 1;
		}
		_sendQueuedMessages() {
			let e = [...this._messagesQueue];
			this._messagesQueue = [];
			for (let t of e) this.send(t);
		}
		send(e) {
			if (this._disconnected) return;
			if (!this._id) {
				this._messagesQueue.push(e);
				return;
			}
			if (!e.type) {
				this.emit(G.Error, "Invalid message");
				return;
			}
			if (!this._wsOpen()) return;
			let t = JSON.stringify(e);
			this._socket.send(t);
		}
		close() {
			this._disconnected ||= (this._cleanup(), !0);
		}
		_cleanup() {
			this._socket &&= (this._socket.onopen = this._socket.onmessage = this._socket.onclose = null, this._socket.close(), void 0), clearTimeout(this._wsPingTimer);
		}
	}, Qn = class {
		constructor(e) {
			this.connection = e;
		}
		startConnection(e) {
			let t = this._startPeerConnection();
			if (this.connection.peerConnection = t, this.connection.type === B.Media && e._stream && this._addTracksToConnection(e._stream, t), e.originator) {
				let n = this.connection, r = { ordered: !!e.reliable }, i = t.createDataChannel(n.label, r);
				n._initializeDataChannel(i), this._makeOffer();
			} else this.handleSDP("OFFER", e.sdp);
		}
		_startPeerConnection() {
			I.log("Creating RTCPeerConnection.");
			let e = new RTCPeerConnection(this.connection.provider.options.config);
			return this._setupListeners(e), e;
		}
		_setupListeners(e) {
			let t = this.connection.peer, n = this.connection.connectionId, r = this.connection.type, i = this.connection.provider;
			I.log("Listening for ICE candidates."), e.onicecandidate = (e) => {
				!e.candidate || !e.candidate.candidate || (I.log(`Received ICE candidates for ${t}:`, e.candidate), i.socket.send({
					type: K.Candidate,
					payload: {
						candidate: e.candidate,
						type: r,
						connectionId: n
					},
					dst: t
				}));
			}, e.oniceconnectionstatechange = () => {
				switch (e.iceConnectionState) {
					case "failed":
						I.log("iceConnectionState is failed, closing connections to " + t), this.connection.emitError(H.NegotiationFailed, "Negotiation of connection to " + t + " failed."), this.connection.close();
						break;
					case "closed":
						I.log("iceConnectionState is closed, closing connections to " + t), this.connection.emitError(H.ConnectionClosed, "Connection to " + t + " closed."), this.connection.close();
						break;
					case "disconnected":
						I.log("iceConnectionState changed to disconnected on the connection with " + t);
						break;
					case "completed": e.onicecandidate = () => {};
				}
				this.connection.emit("iceStateChanged", e.iceConnectionState);
			}, I.log("Listening for data channel"), e.ondatachannel = (e) => {
				I.log("Received data channel");
				let r = e.channel;
				i.getConnection(t, n)._initializeDataChannel(r);
			}, I.log("Listening for remote stream"), e.ontrack = (e) => {
				I.log("Received remote stream");
				let r = e.streams[0], a = i.getConnection(t, n);
				if (a.type === B.Media) {
					let e = a;
					this._addStreamToMediaConnection(r, e);
				}
			};
		}
		cleanup() {
			I.log("Cleaning up PeerConnection to " + this.connection.peer);
			let e = this.connection.peerConnection;
			if (!e) return;
			this.connection.peerConnection = null, e.onicecandidate = e.oniceconnectionstatechange = e.ondatachannel = e.ontrack = () => {};
			let t = e.signalingState !== "closed", n = !1, r = this.connection.dataChannel;
			r && (n = !!r.readyState && r.readyState !== "closed"), (t || n) && e.close();
		}
		async _makeOffer() {
			let e = this.connection.peerConnection, t = this.connection.provider;
			try {
				let n = await e.createOffer(this.connection.options.constraints);
				I.log("Created offer."), this.connection.options.sdpTransform && typeof this.connection.options.sdpTransform == "function" && (n.sdp = this.connection.options.sdpTransform(n.sdp) || n.sdp);
				try {
					await e.setLocalDescription(n), I.log("Set localDescription:", n, `for:${this.connection.peer}`);
					let r = {
						sdp: n,
						type: this.connection.type,
						connectionId: this.connection.connectionId,
						metadata: this.connection.metadata
					};
					if (this.connection.type === B.Data) {
						let e = this.connection;
						r = {
							...r,
							label: e.label,
							reliable: e.reliable,
							serialization: e.serialization
						};
					}
					t.socket.send({
						type: K.Offer,
						payload: r,
						dst: this.connection.peer
					});
				} catch (e) {
					e != "OperationError: Failed to set local offer sdp: Called in wrong state: kHaveRemoteOffer" && (t.emitError(V.WebRTC, e), I.log("Failed to setLocalDescription, ", e));
				}
			} catch (e) {
				t.emitError(V.WebRTC, e), I.log("Failed to createOffer, ", e);
			}
		}
		async _makeAnswer() {
			let e = this.connection.peerConnection, t = this.connection.provider;
			try {
				let n = await e.createAnswer();
				I.log("Created answer."), this.connection.options.sdpTransform && typeof this.connection.options.sdpTransform == "function" && (n.sdp = this.connection.options.sdpTransform(n.sdp) || n.sdp);
				try {
					await e.setLocalDescription(n), I.log("Set localDescription:", n, `for:${this.connection.peer}`), t.socket.send({
						type: K.Answer,
						payload: {
							sdp: n,
							type: this.connection.type,
							connectionId: this.connection.connectionId
						},
						dst: this.connection.peer
					});
				} catch (e) {
					t.emitError(V.WebRTC, e), I.log("Failed to setLocalDescription, ", e);
				}
			} catch (e) {
				t.emitError(V.WebRTC, e), I.log("Failed to create answer, ", e);
			}
		}
		async handleSDP(e, t) {
			t = new RTCSessionDescription(t);
			let n = this.connection.peerConnection, r = this.connection.provider;
			I.log("Setting remote description", t);
			let i = this;
			try {
				await n.setRemoteDescription(t), I.log(`Set remoteDescription:${e} for:${this.connection.peer}`), e === "OFFER" && await i._makeAnswer();
			} catch (e) {
				r.emitError(V.WebRTC, e), I.log("Failed to setRemoteDescription, ", e);
			}
		}
		async handleCandidate(e) {
			I.log("handleCandidate:", e);
			try {
				await this.connection.peerConnection.addIceCandidate(e), I.log(`Added ICE candidate for:${this.connection.peer}`);
			} catch (e) {
				this.connection.provider.emitError(V.WebRTC, e), I.log("Failed to handleCandidate, ", e);
			}
		}
		_addTracksToConnection(e, t) {
			if (I.log(`add tracks from stream ${e.id} to peer connection`), !t.addTrack) return I.error("Your browser does't support RTCPeerConnection#addTrack. Ignored.");
			e.getTracks().forEach((n) => {
				t.addTrack(n, e);
			});
		}
		_addStreamToMediaConnection(e, t) {
			I.log(`add stream ${e.id} to media connection ${t.connectionId}`), t.addStream(e);
		}
	}, $n = class extends L.EventEmitter {
		emitError(e, t) {
			I.error("Error:", t), this.emit("error", new er(`${e}`, t));
		}
	}, er = class extends Error {
		constructor(e, t) {
			typeof t == "string" ? super(t) : (super(), Object.assign(this, t)), this.type = e;
		}
	}, tr = class extends $n {
		get open() {
			return this._open;
		}
		constructor(e, t, n) {
			super(), this.peer = e, this.provider = t, this.options = n, this._open = !1, this.metadata = n.metadata;
		}
	}, nr = class e extends tr {
		static #e = this.ID_PREFIX = "mc_";
		get type() {
			return B.Media;
		}
		get localStream() {
			return this._localStream;
		}
		get remoteStream() {
			return this._remoteStream;
		}
		constructor(t, n, r) {
			super(t, n, r), this._localStream = this.options._stream, this.connectionId = this.options.connectionId || e.ID_PREFIX + F.randomToken(), this._negotiator = new Qn(this), this._localStream && this._negotiator.startConnection({
				_stream: this._localStream,
				originator: !0
			});
		}
		_initializeDataChannel(e) {
			this.dataChannel = e, this.dataChannel.onopen = () => {
				I.log(`DC#${this.connectionId} dc connection success`), this.emit("willCloseOnRemote");
			}, this.dataChannel.onclose = () => {
				I.log(`DC#${this.connectionId} dc closed for:`, this.peer), this.close();
			};
		}
		addStream(e) {
			I.log("Receiving stream", e), this._remoteStream = e, super.emit("stream", e);
		}
		handleMessage(e) {
			let t = e.type, n = e.payload;
			switch (e.type) {
				case K.Answer:
					this._negotiator.handleSDP(t, n.sdp), this._open = !0;
					break;
				case K.Candidate:
					this._negotiator.handleCandidate(n.candidate);
					break;
				default: I.warn(`Unrecognized message type:${t} from peer:${this.peer}`);
			}
		}
		answer(e, t = {}) {
			if (this._localStream) {
				I.warn("Local stream already exists on this MediaConnection. Are you answering a call twice?");
				return;
			}
			this._localStream = e, t && t.sdpTransform && (this.options.sdpTransform = t.sdpTransform), this._negotiator.startConnection({
				...this.options._payload,
				_stream: e
			});
			let n = this.provider._getMessages(this.connectionId);
			for (let e of n) this.handleMessage(e);
			this._open = !0;
		}
		close() {
			this._negotiator &&= (this._negotiator.cleanup(), null), this._localStream = null, this._remoteStream = null, this.provider &&= (this.provider._removeConnection(this), null), this.options && this.options._stream && (this.options._stream = null), this.open && (this._open = !1, super.emit("close"));
		}
	}, rr = class {
		constructor(e) {
			this._options = e;
		}
		_buildRequest(e) {
			let t = this._options.secure ? "https" : "http", { host: n, port: r, path: i, key: a } = this._options, o = new URL(`${t}://${n}:${r}${i}${a}/${e}`);
			return o.searchParams.set("ts", `${Date.now()}${Math.random()}`), o.searchParams.set("version", Xn), fetch(o.href, { referrerPolicy: this._options.referrerPolicy });
		}
		async retrieveId() {
			try {
				let e = await this._buildRequest("id");
				if (e.status !== 200) throw Error(`Error. Status:${e.status}`);
				return e.text();
			} catch (e) {
				I.error("Error retrieving ID", e);
				let t = "";
				throw this._options.path === "/" && this._options.host !== F.CLOUD_HOST && (t = " If you passed in a `path` to your self-hosted PeerServer, you'll also need to pass in that same path when creating a new Peer."), Error("Could not get an ID from the server." + t);
			}
		}
		async listAllPeers() {
			try {
				let e = await this._buildRequest("peers");
				if (e.status !== 200) {
					if (e.status === 401) {
						let e = "";
						throw e = this._options.host === F.CLOUD_HOST ? "It looks like you're using the cloud server. You can email team@peerjs.com to enable peer listing for your API key." : "You need to enable `allow_discovery` on your self-hosted PeerServer to use this feature.", Error("It doesn't look like you have permission to list peers IDs. " + e);
					}
					throw Error(`Error. Status:${e.status}`);
				}
				return e.json();
			} catch (e) {
				throw I.error("Error retrieving list peers", e), Error("Could not get list peers from the server." + e);
			}
		}
	}, q = class e extends tr {
		static #e = this.ID_PREFIX = "dc_";
		static #t = this.MAX_BUFFERED_AMOUNT = 8388608;
		get type() {
			return B.Data;
		}
		constructor(t, n, r) {
			super(t, n, r), this.connectionId = this.options.connectionId || e.ID_PREFIX + Wn(), this.label = this.options.label || this.connectionId, this.reliable = !!this.options.reliable, this._negotiator = new Qn(this), this._negotiator.startConnection(this.options._payload || {
				originator: !0,
				reliable: this.reliable
			});
		}
		_initializeDataChannel(e) {
			this.dataChannel = e, this.dataChannel.onopen = () => {
				I.log(`DC#${this.connectionId} dc connection success`), this._open = !0, this.emit("open");
			}, this.dataChannel.onmessage = (e) => {
				I.log(`DC#${this.connectionId} dc onmessage:`, e.data);
			}, this.dataChannel.onclose = () => {
				I.log(`DC#${this.connectionId} dc closed for:`, this.peer), this.close();
			};
		}
		close(e) {
			if (e?.flush) {
				this.send({ __peerData: { type: "close" } });
				return;
			}
			this._negotiator &&= (this._negotiator.cleanup(), null), this.provider &&= (this.provider._removeConnection(this), null), this.dataChannel &&= (this.dataChannel.onopen = null, this.dataChannel.onmessage = null, this.dataChannel.onclose = null, null), this.open && (this._open = !1, super.emit("close"));
		}
		send(e, t = !1) {
			if (!this.open) {
				this.emitError(U.NotOpenYet, "Connection is not open. You should listen for the `open` event before sending messages.");
				return;
			}
			return this._send(e, t);
		}
		async handleMessage(e) {
			let t = e.payload;
			switch (e.type) {
				case K.Answer:
					await this._negotiator.handleSDP(e.type, t.sdp);
					break;
				case K.Candidate:
					await this._negotiator.handleCandidate(t.candidate);
					break;
				default: I.warn("Unrecognized message type:", e.type, "from peer:", this.peer);
			}
		}
	}, J = class extends q {
		get bufferSize() {
			return this._bufferSize;
		}
		_initializeDataChannel(e) {
			super._initializeDataChannel(e), this.dataChannel.binaryType = "arraybuffer", this.dataChannel.addEventListener("message", (e) => this._handleDataMessage(e));
		}
		_bufferedSend(e) {
			(this._buffering || !this._trySend(e)) && (this._buffer.push(e), this._bufferSize = this._buffer.length);
		}
		_trySend(e) {
			if (!this.open) return !1;
			if (this.dataChannel.bufferedAmount > q.MAX_BUFFERED_AMOUNT) return this._buffering = !0, setTimeout(() => {
				this._buffering = !1, this._tryBuffer();
			}, 50), !1;
			try {
				this.dataChannel.send(e);
			} catch (e) {
				return I.error(`DC#:${this.connectionId} Error when sending:`, e), this._buffering = !0, this.close(), !1;
			}
			return !0;
		}
		_tryBuffer() {
			if (!this.open || this._buffer.length === 0) return;
			let e = this._buffer[0];
			this._trySend(e) && (this._buffer.shift(), this._bufferSize = this._buffer.length, this._tryBuffer());
		}
		close(e) {
			if (e?.flush) {
				this.send({ __peerData: { type: "close" } });
				return;
			}
			this._buffer = [], this._bufferSize = 0, super.close();
		}
		constructor(...e) {
			super(...e), this._buffer = [], this._bufferSize = 0, this._buffering = !1;
		}
	}, ir = class extends J {
		close(e) {
			super.close(e), this._chunkedData = {};
		}
		constructor(e, t, n) {
			super(e, t, n), this.chunker = new Vn(), this.serialization = W.Binary, this._chunkedData = {};
		}
		_handleDataMessage({ data: e }) {
			let t = se(e), n = t.__peerData;
			if (n) {
				if (n.type === "close") {
					this.close();
					return;
				}
				this._handleChunk(t);
				return;
			}
			this.emit("data", t);
		}
		_handleChunk(e) {
			let t = e.__peerData, n = this._chunkedData[t] || {
				data: [],
				count: 0,
				total: e.total
			};
			if (n.data[e.n] = new Uint8Array(e.data), n.count++, this._chunkedData[t] = n, n.total === n.count) {
				delete this._chunkedData[t];
				let e = Ln(n.data);
				this._handleDataMessage({ data: e });
			}
		}
		_send(e, t) {
			let n = ce(e);
			if (n instanceof Promise) return this._send_blob(n);
			if (!t && n.byteLength > this.chunker.chunkedMTU) {
				this._sendChunks(n);
				return;
			}
			this._bufferedSend(n);
		}
		async _send_blob(e) {
			let t = await e;
			if (t.byteLength > this.chunker.chunkedMTU) {
				this._sendChunks(t);
				return;
			}
			this._bufferedSend(t);
		}
		_sendChunks(e) {
			let t = this.chunker.chunk(e);
			I.log(`DC#${this.connectionId} Try to send ${t.length} chunks...`);
			for (let e of t) this.send(e, !0);
		}
	}, ar = class extends J {
		_handleDataMessage({ data: e }) {
			super.emit("data", e);
		}
		_send(e, t) {
			this._bufferedSend(e);
		}
		constructor(...e) {
			super(...e), this.serialization = W.None;
		}
	}, or = class extends J {
		_handleDataMessage({ data: e }) {
			let t = this.parse(this.decoder.decode(e)), n = t.__peerData;
			if (n && n.type === "close") {
				this.close();
				return;
			}
			this.emit("data", t);
		}
		_send(e, t) {
			let n = this.encoder.encode(this.stringify(e));
			if (n.byteLength >= F.chunkedMTU) {
				this.emitError(U.MessageToBig, "Message too big for JSON channel");
				return;
			}
			this._bufferedSend(n);
		}
		constructor(...e) {
			super(...e), this.serialization = W.JSON, this.encoder = new TextEncoder(), this.decoder = new TextDecoder(), this.stringify = JSON.stringify, this.parse = JSON.parse;
		}
	}, sr = class e extends $n {
		static #e = this.DEFAULT_KEY = "peerjs";
		get id() {
			return this._id;
		}
		get options() {
			return this._options;
		}
		get open() {
			return this._open;
		}
		get socket() {
			return this._socket;
		}
		get connections() {
			let e = Object.create(null);
			for (let [t, n] of this._connections) e[t] = n;
			return e;
		}
		get destroyed() {
			return this._destroyed;
		}
		get disconnected() {
			return this._disconnected;
		}
		constructor(t, n) {
			super(), this._serializers = {
				raw: ar,
				json: or,
				binary: ir,
				"binary-utf8": ir,
				default: ir
			}, this._id = null, this._lastServerId = null, this._destroyed = !1, this._disconnected = !1, this._open = !1, this._connections = /* @__PURE__ */ new Map(), this._lostMessages = /* @__PURE__ */ new Map();
			let r;
			if (t && t.constructor == Object ? n = t : t && (r = t.toString()), n = {
				debug: 0,
				host: F.CLOUD_HOST,
				port: F.CLOUD_PORT,
				path: "/",
				key: e.DEFAULT_KEY,
				token: F.randomToken(),
				config: F.defaultConfig,
				referrerPolicy: "strict-origin-when-cross-origin",
				serializers: {},
				...n
			}, this._options = n, this._serializers = {
				...this._serializers,
				...this.options.serializers
			}, this._options.host === "/" && (this._options.host = window.location.hostname), this._options.path && (this._options.path[0] !== "/" && (this._options.path = "/" + this._options.path), this._options.path[this._options.path.length - 1] !== "/" && (this._options.path += "/")), this._options.secure === void 0 && this._options.host !== F.CLOUD_HOST ? this._options.secure = F.isSecure() : this._options.host == F.CLOUD_HOST && (this._options.secure = !0), this._options.logFunction && I.setLogFunction(this._options.logFunction), I.logLevel = this._options.debug || 0, this._api = new rr(n), this._socket = this._createServerConnection(), !F.supports.audioVideo && !F.supports.data) {
				this._delayedAbort(V.BrowserIncompatible, "The current browser does not support WebRTC");
				return;
			}
			if (r && !F.validateId(r)) {
				this._delayedAbort(V.InvalidID, `ID "${r}" is invalid`);
				return;
			}
			r ? this._initialize(r) : this._api.retrieveId().then((e) => this._initialize(e)).catch((e) => this._abort(V.ServerError, e));
		}
		_createServerConnection() {
			let e = new Zn(this._options.secure, this._options.host, this._options.port, this._options.path, this._options.key, this._options.pingInterval);
			return e.on(G.Message, (e) => {
				this._handleMessage(e);
			}), e.on(G.Error, (e) => {
				this._abort(V.SocketError, e);
			}), e.on(G.Disconnected, () => {
				this.disconnected || (this.emitError(V.Network, "Lost connection to server."), this.disconnect());
			}), e.on(G.Close, () => {
				this.disconnected || this._abort(V.SocketClosed, "Underlying socket is already closed.");
			}), e;
		}
		_initialize(e) {
			this._id = e, this.socket.start(e, this._options.token);
		}
		_handleMessage(e) {
			let t = e.type, n = e.payload, r = e.src;
			switch (t) {
				case K.Open:
					this._lastServerId = this.id, this._open = !0, this.emit("open", this.id);
					break;
				case K.Error:
					this._abort(V.ServerError, n.msg);
					break;
				case K.IdTaken:
					this._abort(V.UnavailableID, `ID "${this.id}" is taken`);
					break;
				case K.InvalidKey:
					this._abort(V.InvalidKey, `API KEY "${this._options.key}" is invalid`);
					break;
				case K.Leave:
					I.log(`Received leave message from ${r}`), this._cleanupPeer(r), this._connections.delete(r);
					break;
				case K.Expire:
					this.emitError(V.PeerUnavailable, `Could not connect to peer ${r}`);
					break;
				case K.Offer: {
					let e = n.connectionId, t = this.getConnection(r, e);
					if (t && (t.close(), I.warn(`Offer received for existing Connection ID:${e}`)), n.type === B.Media) {
						let i = new nr(r, this, {
							connectionId: e,
							_payload: n,
							metadata: n.metadata
						});
						t = i, this._addConnection(r, t), this.emit("call", i);
					} else if (n.type === B.Data) {
						let i = new this._serializers[n.serialization](r, this, {
							connectionId: e,
							_payload: n,
							metadata: n.metadata,
							label: n.label,
							serialization: n.serialization,
							reliable: n.reliable
						});
						t = i, this._addConnection(r, t), this.emit("connection", i);
					} else {
						I.warn(`Received malformed connection type:${n.type}`);
						return;
					}
					let i = this._getMessages(e);
					for (let e of i) t.handleMessage(e);
					break;
				}
				default: {
					if (!n) {
						I.warn(`You received a malformed message from ${r} of type ${t}`);
						return;
					}
					let i = n.connectionId, a = this.getConnection(r, i);
					a && a.peerConnection ? a.handleMessage(e) : i ? this._storeMessage(i, e) : I.warn("You received an unrecognized message:", e);
					break;
				}
			}
		}
		_storeMessage(e, t) {
			this._lostMessages.has(e) || this._lostMessages.set(e, []), this._lostMessages.get(e).push(t);
		}
		_getMessages(e) {
			let t = this._lostMessages.get(e);
			return t ? (this._lostMessages.delete(e), t) : [];
		}
		connect(e, t = {}) {
			if (t = {
				serialization: "default",
				...t
			}, this.disconnected) {
				I.warn("You cannot connect to a new Peer because you called .disconnect() on this Peer and ended your connection with the server. You can create a new Peer to reconnect, or call reconnect on this peer if you believe its ID to still be available."), this.emitError(V.Disconnected, "Cannot connect to new Peer after disconnecting from server.");
				return;
			}
			let n = new this._serializers[t.serialization](e, this, t);
			return this._addConnection(e, n), n;
		}
		call(e, t, n = {}) {
			if (this.disconnected) {
				I.warn("You cannot connect to a new Peer because you called .disconnect() on this Peer and ended your connection with the server. You can create a new Peer to reconnect."), this.emitError(V.Disconnected, "Cannot connect to new Peer after disconnecting from server.");
				return;
			}
			if (!t) {
				I.error("To call a peer, you must provide a stream from your browser's `getUserMedia`.");
				return;
			}
			let r = new nr(e, this, {
				...n,
				_stream: t
			});
			return this._addConnection(e, r), r;
		}
		_addConnection(e, t) {
			I.log(`add connection ${t.type}:${t.connectionId} to peerId:${e}`), this._connections.has(e) || this._connections.set(e, []), this._connections.get(e).push(t);
		}
		_removeConnection(e) {
			let t = this._connections.get(e.peer);
			if (t) {
				let n = t.indexOf(e);
				n !== -1 && t.splice(n, 1);
			}
			this._lostMessages.delete(e.connectionId);
		}
		getConnection(e, t) {
			let n = this._connections.get(e);
			if (!n) return null;
			for (let e of n) if (e.connectionId === t) return e;
			return null;
		}
		_delayedAbort(e, t) {
			setTimeout(() => {
				this._abort(e, t);
			}, 0);
		}
		_abort(e, t) {
			I.error("Aborting!"), this.emitError(e, t), this._lastServerId ? this.disconnect() : this.destroy();
		}
		destroy() {
			this.destroyed || (I.log(`Destroy peer with ID:${this.id}`), this.disconnect(), this._cleanup(), this._destroyed = !0, this.emit("close"));
		}
		_cleanup() {
			for (let e of this._connections.keys()) this._cleanupPeer(e), this._connections.delete(e);
			this.socket.removeAllListeners();
		}
		_cleanupPeer(e) {
			let t = this._connections.get(e);
			if (t) for (let e of t) e.close();
		}
		disconnect() {
			if (this.disconnected) return;
			let e = this.id;
			I.log(`Disconnect peer with ID:${e}`), this._disconnected = !0, this._open = !1, this.socket.close(), this._lastServerId = e, this._id = null, this.emit("disconnected", e);
		}
		reconnect() {
			if (this.disconnected && !this.destroyed) I.log(`Attempting reconnection to server with ID ${this._lastServerId}`), this._disconnected = !1, this._initialize(this._lastServerId);
			else if (this.destroyed) throw Error("This peer cannot reconnect to the server. It has already been destroyed.");
			else if (!this.disconnected && !this.open) I.error("In a hurry? We're still trying to make the initial connection!");
			else throw Error(`Peer ${this.id} cannot reconnect because it is not disconnected from the server!`);
		}
		listAllPeers(e = (e) => {}) {
			this._api.listAllPeers().then((t) => e(t)).catch((e) => this._abort(V.ServerError, e));
		}
	}, cr = class extends q {
		constructor(e, t, n) {
			super(e, t, {
				...n,
				reliable: !0
			}), this._CHUNK_SIZE = 32768, this._splitStream = new TransformStream({ transform: (e, t) => {
				for (let n = 0; n < e.length; n += this._CHUNK_SIZE) t.enqueue(e.subarray(n, n + this._CHUNK_SIZE));
			} }), this._rawSendStream = new WritableStream({ write: async (e, t) => {
				let n = new Promise((e) => this.dataChannel.addEventListener("bufferedamountlow", e, { once: !0 }));
				await (this.dataChannel.bufferedAmount <= q.MAX_BUFFERED_AMOUNT - e.byteLength || n);
				try {
					this.dataChannel.send(e);
				} catch (e) {
					I.error(`DC#:${this.connectionId} Error when sending:`, e), t.error(e), this.close();
				}
			} }), this.writer = this._splitStream.writable.getWriter(), this._rawReadStream = new ReadableStream({ start: (e) => {
				this.once("open", () => {
					this.dataChannel.addEventListener("message", (t) => {
						e.enqueue(t.data);
					});
				});
			} }), this._splitStream.readable.pipeTo(this._rawSendStream);
		}
		_initializeDataChannel(e) {
			super._initializeDataChannel(e), this.dataChannel.binaryType = "arraybuffer", this.dataChannel.bufferedAmountLowThreshold = q.MAX_BUFFERED_AMOUNT / 2;
		}
	}, lr = class extends cr {
		constructor(e, t, n) {
			super(e, t, n), this.serialization = "MsgPack", this._encoder = new an(), (async () => {
				for await (let e of Nn(this._rawReadStream)) {
					if (e.__peerData?.type === "close") {
						this.close();
						return;
					}
					this.emit("data", e);
				}
			})();
		}
		_send(e) {
			return this.writer.write(this._encoder.encode(e));
		}
	}, ur = class extends sr {
		constructor(...e) {
			super(...e), this._serializers = {
				MsgPack: lr,
				default: lr
			};
		}
	}, dr = sr;
})), pr = globalThis.Peer || null, mr = null;
function hr() {
	return mr ||= (async () => {
		try {
			let e = await Promise.resolve().then(() => (fr(), In)), t = e.default || e;
			pr = e.Peer || t.Peer || t.default && t.default.Peer || globalThis.Peer || null;
		} catch {
			pr = globalThis.Peer || null;
		}
		return pr;
	})(), mr;
}
var Y = {
	host: "0.peerjs.com",
	port: 443,
	secure: !0,
	key: "peerjs",
	path: "/"
}, gr = 5e3;
function _r(e) {
	return `${e.host}:${e.port}:${e.key}:${e.path || "/"}`;
}
var vr = class {
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
		let i = ee(), a, o = !1;
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
			a.send(te(e, i));
		} catch (e) {
			this.pending.delete(i), s.cleanup(), n(e);
		}
		else {
			let t = () => {
				try {
					a.send(te(e, i));
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
		let e = this.signaling, t = typeof window < "u" && window.__PDM_DEBUG ? 3 : 0, n = globalThis.Peer || await hr();
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
		this.lastActive = Date.now(), this._kaTicks = 0, typeof e != "string" || re(e)?.type;
	}
	handleFileData(e, t) {
		this.lastActive = Date.now(), this._kaTicks = 0;
		let n = this.pending.get(e);
		if (!n) return;
		if (ie(t)) {
			let e = ae(t);
			n.chunks.push(e), n.got += e.length;
			return;
		}
		let r = re(t);
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
		}, gr), this.kaTimer.unref?.();
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
}, X = new class {
	constructor() {
		this.slots = /* @__PURE__ */ new Map();
	}
	async load(e, { peer: t, signaling: n = Y, signal: r } = {}) {
		if (!t) throw Error("peerdrive-media: peer (node peer id) is required");
		if (!e || typeof e != "string") throw Error("peerdrive-media: url is required");
		let i = `${_r(n)}|${t}`, a = this.slots.get(i);
		if ((!a || a.closed) && (a = new vr(t, n), this.slots.set(i, a)), r?.aborted) throw new DOMException("aborted", "AbortError");
		return new Promise((t, n) => {
			a.request(e, t, n, r);
		});
	}
	dispose(e, t = Y) {
		let n = `${_r(t)}|${e}`, r = this.slots.get(n);
		r && (r.failAll("disposed"), this.slots.delete(n));
	}
}(), Z = null, yr = null;
async function br({ peer: e, signaling: t = Y, allow: n } = {}) {
	if (!navigator.serviceWorker) throw Error("peerdrive-media: Service Worker not supported");
	let r = await navigator.serviceWorker.register("/sw.js");
	await navigator.serviceWorker.ready;
	let i = new MessageChannel();
	return i.port1.start(), Z = i.port1, yr = {
		peer: e,
		signaling: t,
		allow: n
	}, i.port2.postMessage({
		type: "pdm-config",
		config: {
			peer: e,
			signaling: t,
			allow: n
		}
	}), i.port1.onmessage = (e) => {
		let t = e.data || {};
		t.type === "pdm-load-request" && xr(t.url, t.reqId);
	}, { unregister: async () => {
		await r.unregister(), Z = null;
	} };
}
async function xr(e, t) {
	try {
		let n = await X.load(e, {
			peer: yr.peer,
			signaling: yr.signaling
		});
		Z && Z.postMessage({
			type: "pdm-load-response",
			reqId: t,
			blob: n.blob,
			mime: n.mime,
			size: n.size
		});
	} catch (e) {
		Z && Z.postMessage({
			type: "pdm-load-response",
			reqId: t,
			error: e.message
		});
	}
}
var Q = null, $ = {};
function Sr({ peer: e, signaling: t = Y, allow: n } = {}) {
	if (!e) throw Error("peerdrive-media: peer is required for setupMP");
	if (Q = {
		peer: e,
		signaling: t,
		allow: n
	}, !$.img) {
		let e = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, "src");
		$.img = e?.set, Object.defineProperty(HTMLImageElement.prototype, "src", {
			get: e?.get,
			set: function(e) {
				Q && Cr(e) ? wr(this, e, Q) : $.img && $.img.call(this, e);
			},
			configurable: !0
		});
	}
	if (!$.video) {
		let e = Object.getOwnPropertyDescriptor(HTMLVideoElement.prototype, "src");
		$.video = e?.set, Object.defineProperty(HTMLVideoElement.prototype, "src", {
			get: e?.get,
			set: function(e) {
				Q && Cr(e) ? wr(this, e, Q) : $.video && $.video.call(this, e);
			},
			configurable: !0
		});
	}
	if (typeof HTMLAudioElement < "u" && !$.audio) {
		let e = Object.getOwnPropertyDescriptor(HTMLAudioElement.prototype, "src");
		$.audio = e?.set, Object.defineProperty(HTMLAudioElement.prototype, "src", {
			get: e?.get,
			set: function(e) {
				Q && Cr(e) ? wr(this, e, Q) : $.audio && $.audio.call(this, e);
			},
			configurable: !0
		});
	}
	return { teardown: () => {
		Q = null;
		for (let [e, t] of Object.entries($)) if (t) {
			let n = e === "img" ? HTMLImageElement.prototype : e === "video" ? HTMLVideoElement.prototype : HTMLAudioElement.prototype, r = Object.getOwnPropertyDescriptor(n, "src");
			Object.defineProperty(n, "src", {
				get: r?.get,
				set: t,
				configurable: !0
			});
		}
		$ = {};
	} };
}
function Cr(e) {
	return Q?.allow ? typeof Q.allow == "function" ? Q.allow(e) : Array.isArray(Q.allow) ? Q.allow.some((t) => e.startsWith(t)) : !1 : !1;
}
async function wr(e, t, n) {
	try {
		let r = await X.load(t, {
			peer: n.peer,
			signaling: n.signaling
		});
		$[e.tagName?.toLowerCase()] ? $[e.tagName?.toLowerCase()].call(e, r.blobUrl) : e.src = r.blobUrl;
	} catch (n) {
		console.error("peerdrive-media: load failed", n), $[e.tagName?.toLowerCase()] ? $[e.tagName?.toLowerCase()].call(e, t) : e.src = t;
	}
}
//#endregion
//#region src/vanilla/main.js
function Tr({ url: e, peer: t, signaling: n = Y, signal: r } = {}) {
	return X.load(e, {
		peer: t,
		signaling: n,
		signal: r
	});
}
async function Er({ peer: e, signaling: t = Y, allow: n } = {}) {
	return br({
		peer: e,
		signaling: t,
		allow: n
	});
}
function Dr({ peer: e, signaling: t = Y, allow: n } = {}) {
	return Sr({
		peer: e,
		signaling: t,
		allow: n
	});
}
function Or({ url: e, peer: t, signaling: n, signal: r, container: i, props: a = {} } = {}) {
	let o = i || document.body;
	return Tr({
		url: e,
		peer: t,
		signaling: n,
		signal: r
	}).then(({ blobUrl: e, mime: t }) => {
		let n;
		if (t.startsWith("image/")) n = document.createElement("img");
		else if (t.startsWith("video/")) n = document.createElement("video"), n.controls = !0;
		else throw Error(`peerdrive-media: unsupported mime ${t} for mount()`);
		return n.src = e, Object.assign(n, a), o.appendChild(n), {
			node: n,
			blobUrl: e,
			mime: t
		};
	});
}
var kr = {
	load: Tr,
	mount: Or,
	setupSW: Er,
	setupMonkeyPatch: Dr,
	client: X,
	DEFAULT_SIGNALING: Y
};
//#endregion
export { Y as DEFAULT_SIGNALING, X as client, kr as default, Tr as load, Or as mount, br as registerSW, Sr as setupMP, Dr as setupMonkeyPatch, Er as setupSW };
