// packages/core/dist/opcodes.js
var Op = {
  SAVE: 1,
  RESTORE: 2,
  TRANSFORM: 3,
  TRANSLATE: 4,
  SCALE: 5,
  CLIP_PATH: 6,
  CLIP_RECT: 7,
  FILL_COLOR: 16,
  FILL_PAINT: 17,
  STROKE_COLOR: 18,
  STROKE_PAINT: 19,
  LINE: 20,
  DASH: 21,
  ALPHA: 22,
  BLEND: 23,
  SHADOW: 24,
  FILTER: 25,
  FONT: 26,
  TEXT_STYLE: 27,
  FILL_RECT: 32,
  STROKE_RECT: 33,
  FILL_PATH: 34,
  STROKE_PATH: 35,
  FILL_PATH_AT: 36,
  FILL_PATH_RUN: 37,
  CLEAR_RECT: 38,
  FILL_TEXT: 48,
  STROKE_TEXT: 49,
  IMAGE: 64,
  IMAGE_SUB: 65,
  SMOOTHING: 66,
  USE: 80,
  USE_AT: 81,
  GROUP_BEGIN: 82,
  GROUP_END: 83,
  MASK_BEGIN: 84,
  MASK_END: 85,
  LINK: 112,
  MARK: 113,
  EXT: 255
};
var OPSET_VERSION = 1;
var FORMAT_VERSION = 1;
var OP_NAMES = Object.fromEntries(Object.entries(Op).map(([k, v]) => [v, k]));
var MaskKind = { ALPHA: 0, LUMINOSITY: 1 };
var BLEND_NAMES = [
  "source-over",
  "multiply",
  "screen",
  "overlay",
  "darken",
  "lighten",
  "color-dodge",
  "color-burn",
  "hard-light",
  "soft-light",
  "difference",
  "exclusion",
  "hue",
  "saturation",
  "color",
  "luminosity",
  "destination-over",
  "destination-in",
  "destination-out",
  "source-in",
  "source-out",
  "source-atop",
  "destination-atop",
  "xor",
  "copy",
  "lighter"
];
var LINE_CAPS = ["butt", "round", "square"];
var LINE_JOINS = ["miter", "round", "bevel"];
var TEXT_ALIGNS = ["left", "right", "center", "start", "end"];
var TEXT_BASELINES = ["alphabetic", "top", "middle", "bottom", "hanging", "ideographic"];
var TEXT_DIRECTIONS = ["inherit", "ltr", "rtl"];
var FILL_RULES = ["nonzero", "evenodd"];
var REPEATS = ["repeat", "repeat-x", "repeat-y", "no-repeat"];
var SMOOTHING_QUALITIES = ["low", "medium", "high"];
var FONT_STYLES = ["normal", "italic", "oblique"];
var Verb = { MOVE: 0, LINE: 1, QUAD: 2, CUBIC: 3, CLOSE: 4, RECT: 5, ELLIPSE: 6, ARC_TO: 7, ROUND_RECT: 8 };
var VERB_ARGS = [2, 2, 4, 6, 0, 4, 8, 5, 5];
var PaintKind = { LINEAR: 0, RADIAL: 1, CONIC: 2, PATTERN: 3 };
var FontKind = { EMBEDDED: 0, SYSTEM: 1 };

// packages/core/dist/bytes.js
var ByteReader = class {
  bytes;
  view;
  pos = 0;
  utf8 = new TextDecoder();
  constructor(bytes) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }
  get eof() {
    return this.pos >= this.bytes.length;
  }
  /** Check a byte count before reading or allocating space for decoded data. */
  need(n) {
    if (!Number.isSafeInteger(n) || n < 0)
      throw new BdfFormatError("byte count out of range");
    if (n > this.bytes.length - this.pos)
      throw new BdfFormatError(`unexpected end of data at ${this.pos}`);
  }
  u8() {
    this.need(1);
    return this.bytes[this.pos++];
  }
  u16() {
    this.need(2);
    const v = this.view.getUint16(this.pos, true);
    this.pos += 2;
    return v;
  }
  u32() {
    this.need(4);
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }
  u64() {
    this.need(8);
    const v = this.view.getBigUint64(this.pos, true);
    this.pos += 8;
    if (v > BigInt(Number.MAX_SAFE_INTEGER))
      throw new BdfFormatError("u64 out of range");
    return Number(v);
  }
  f32() {
    this.need(4);
    const v = this.view.getFloat32(this.pos, true);
    this.pos += 4;
    return v;
  }
  varuint() {
    let result = 0;
    let shift = 0;
    for (; ; ) {
      const b = this.u8();
      if (shift < 28) {
        result |= (b & 127) << shift;
      } else {
        result += (b & 127) * 2 ** shift;
        if (!Number.isSafeInteger(result))
          throw new BdfFormatError("varuint out of range");
      }
      if ((b & 128) === 0)
        break;
      shift += 7;
      if (shift > 63)
        throw new BdfFormatError("bad varuint");
    }
    return result >>> 0 === result ? result >>> 0 : result;
  }
  bytesN(n) {
    this.need(n);
    const v = this.bytes.subarray(this.pos, this.pos + n);
    this.pos += n;
    return v;
  }
  str() {
    const n = this.varuint();
    return this.utf8.decode(this.bytesN(n));
  }
  hash() {
    const b = this.bytesN(16);
    let s = "";
    for (let i = 0; i < 16; i++)
      s += b[i].toString(16).padStart(2, "0");
    return s;
  }
  f32array(n) {
    if (!Number.isSafeInteger(n))
      throw new BdfFormatError("float count out of range");
    this.need(n * 4);
    const out = new Float32Array(n);
    for (let i = 0; i < n; i++)
      out[i] = this.f32();
    return out;
  }
};
var BdfFormatError = class extends Error {
  constructor(message) {
    super(`bdf: ${message}`);
    this.name = "BdfFormatError";
  }
};

// packages/core/dist/object.js
var PAINT_COORDS = [4, 6, 3];
function decodePath(r) {
  const n = r.varuint();
  const verbs = new Uint8Array(r.bytesN(n));
  let total = 0;
  for (const v of verbs) {
    if (v >= VERB_ARGS.length)
      throw new BdfFormatError(`unknown path verb ${v}`);
    total += VERB_ARGS[v];
  }
  return { verbs, args: r.f32array(total) };
}
function decodePathCollection(bytes) {
  const r = new ByteReader(bytes);
  const n = r.varuint();
  const out = [];
  for (let i = 0; i < n; i++)
    out.push(decodePath(r));
  return out;
}
function decodePaint(r) {
  const kind = r.u8();
  if (kind === PaintKind.PATTERN) {
    const image = r.varuint();
    const repeat = r.u8();
    return { kind, coords: new Float32Array(0), stops: [], image, repeat, matrix: r.f32array(6) };
  }
  if (kind >= PAINT_COORDS.length)
    throw new BdfFormatError(`unknown paint kind ${kind}`);
  const coords = r.f32array(PAINT_COORDS[kind]);
  const n = r.varuint();
  const stops = [];
  for (let i = 0; i < n; i++)
    stops.push({ offset: r.f32(), color: r.u32() });
  return { kind, coords, stops, image: 0, repeat: 0, matrix: new Float32Array(0) };
}
function decodeFont(r) {
  const kind = r.u8();
  const hash = kind === FontKind.EMBEDDED ? r.hash() : void 0;
  const family = r.str();
  const weight = r.u16();
  const style = r.u8();
  return { kind, hash, family, weight, style };
}
function decodeObject(bytes) {
  const r = new ByteReader(bytes);
  const magic = r.bytesN(4);
  if (magic[0] !== 66 || magic[1] !== 79 || magic[2] !== 66 || magic[3] !== 74) {
    throw new BdfFormatError("not an object part");
  }
  const opset = r.u16();
  if (opset !== OPSET_VERSION)
    throw new BdfFormatError(`unsupported opset ${opset}`);
  const flags = r.u16();
  if (flags !== 0)
    throw new BdfFormatError(`unsupported object flags ${flags}`);
  const bbox = { x: r.f32(), y: r.f32(), w: r.f32(), h: r.f32() };
  const strings = [];
  for (let i = 0, n = r.varuint(); i < n; i++)
    strings.push(r.str());
  const paths = [];
  for (let i = 0, n = r.varuint(); i < n; i++) {
    if (r.u8() === 0)
      paths.push({ inline: decodePath(r) });
    else {
      const hash = r.hash();
      paths.push({ hash, index: r.varuint() });
    }
  }
  const paints = [];
  for (let i = 0, n = r.varuint(); i < n; i++)
    paints.push(decodePaint(r));
  const fonts = [];
  for (let i = 0, n = r.varuint(); i < n; i++)
    fonts.push(decodeFont(r));
  const images = [];
  for (let i = 0, n = r.varuint(); i < n; i++)
    images.push(r.hash());
  const objects = [];
  for (let i = 0, n = r.varuint(); i < n; i++)
    objects.push(r.hash());
  const ops = r.bytesN(r.varuint());
  return { opset, bbox, strings, paths, paints, fonts, images, objects, ops };
}
function objectDeps(o) {
  const out = [];
  for (const f of o.fonts)
    if (f.hash)
      out.push(f.hash);
  for (const h of o.images)
    out.push(h);
  for (const p of o.paths)
    if ("hash" in p)
      out.push(p.hash);
  for (const h of o.objects)
    out.push(h);
  return out;
}
var MAX_USE_DEPTH = 64;
var MAX_REUSED_INSTRUCTIONS = 1 << 27;
var MAX_TEXT_RUNS = 1 << 22;
var UseLimits = class {
  maxReused;
  maxRuns;
  seen = /* @__PURE__ */ new Set();
  reused = 0;
  /** The limits are the constants above, unless a test asks for smaller ones. */
  constructor(maxReused = MAX_REUSED_INSTRUCTIONS, maxRuns = MAX_TEXT_RUNS) {
    this.maxReused = maxReused;
    this.maxRuns = maxRuns;
  }
  /** Note that a child object is walked; true when it was walked before, and its instructions count as read again. */
  enter(h) {
    if (this.seen.has(h))
      return true;
    this.seen.add(h);
    return false;
  }
  /** Whether an object at depth (0: a top-level one) may draw another; throws when not. */
  descend(depth) {
    if (depth >= MAX_USE_DEPTH)
      throw new BdfFormatError("objects draw objects too deep");
  }
  /** Count an instruction read from an object walked before (the third argument of walk). */
  count = () => {
    if (++this.reused > this.maxReused)
      throw new BdfFormatError("objects are drawn too many times");
  };
};
function walk(o, sink, each) {
  const r = new ByteReader(o.ops);
  const S = o.strings;
  const str = () => {
    const i = r.varuint();
    if (i >= S.length)
      throw new BdfFormatError("bad string ref");
    return S[i];
  };
  while (!r.eof) {
    each?.();
    const code = r.u8();
    switch (code) {
      case Op.SAVE:
        sink.save();
        break;
      case Op.RESTORE:
        sink.restore();
        break;
      case Op.TRANSFORM:
        sink.transform(r.f32(), r.f32(), r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.TRANSLATE:
        sink.translate(r.f32(), r.f32());
        break;
      case Op.SCALE:
        sink.scale(r.f32(), r.f32());
        break;
      case Op.CLIP_PATH:
        sink.clipPath(r.varuint(), r.u8());
        break;
      case Op.CLIP_RECT:
        sink.clipRect(r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.FILL_COLOR:
        sink.fillColor(r.u32());
        break;
      case Op.FILL_PAINT:
        sink.fillPaint(r.varuint());
        break;
      case Op.STROKE_COLOR:
        sink.strokeColor(r.u32());
        break;
      case Op.STROKE_PAINT:
        sink.strokePaint(r.varuint());
        break;
      case Op.LINE:
        sink.line(r.f32(), r.u8(), r.u8(), r.f32());
        break;
      case Op.DASH: {
        const n = r.varuint();
        const segs = r.f32array(n);
        sink.dash(segs, r.f32());
        break;
      }
      case Op.ALPHA:
        sink.alpha(r.f32());
        break;
      case Op.BLEND:
        sink.blend(r.u8());
        break;
      case Op.SHADOW:
        sink.shadow(r.u32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.FILTER:
        sink.filter(str());
        break;
      case Op.FONT:
        sink.font(r.varuint(), r.f32());
        break;
      case Op.TEXT_STYLE:
        sink.textStyle(r.u8(), r.u8(), r.u8(), r.f32());
        break;
      case Op.FILL_RECT:
        sink.fillRect(r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.STROKE_RECT:
        sink.strokeRect(r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.FILL_PATH:
        sink.fillPath(r.varuint(), r.u8());
        break;
      case Op.STROKE_PATH:
        sink.strokePath(r.varuint());
        break;
      case Op.FILL_PATH_AT:
        sink.fillPathAt(r.varuint(), r.u8(), r.f32(), r.f32());
        break;
      case Op.FILL_PATH_RUN: {
        const rule = r.u8();
        const n = r.varuint();
        r.need(n * 9);
        const glyphs = new Array(n);
        for (let i = 0; i < n; i++)
          glyphs[i] = { path: r.varuint(), x: r.f32(), y: r.f32() };
        sink.fillPathRun(rule, glyphs);
        break;
      }
      case Op.CLEAR_RECT:
        sink.clearRect(r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.FILL_TEXT:
        sink.fillText(str(), r.f32(), r.f32(), r.f32());
        break;
      case Op.STROKE_TEXT:
        sink.strokeText(str(), r.f32(), r.f32(), r.f32());
        break;
      case Op.IMAGE:
        sink.image(r.varuint(), r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.IMAGE_SUB:
        sink.imageSub(r.varuint(), r.f32(), r.f32(), r.f32(), r.f32(), r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.SMOOTHING:
        sink.smoothing(r.u8() !== 0, r.u8());
        break;
      case Op.USE:
        sink.use(r.varuint());
        break;
      case Op.USE_AT:
        sink.useAt(r.varuint(), r.f32(), r.f32());
        break;
      case Op.GROUP_BEGIN:
        sink.groupBegin(r.f32(), r.u8(), r.f32(), r.f32(), r.f32(), r.f32());
        break;
      case Op.GROUP_END:
        sink.groupEnd();
        break;
      case Op.MASK_BEGIN:
        sink.maskBegin(r.u8(), r.u32(), r.bytesN(r.varuint()));
        break;
      case Op.MASK_END:
        sink.maskEnd();
        break;
      case Op.LINK:
        sink.link(r.f32(), r.f32(), r.f32(), r.f32(), str());
        break;
      case Op.MARK:
        sink.mark(r.u8(), str());
        break;
      case Op.EXT:
        sink.ext(r.bytesN(r.u32()));
        break;
      default:
        throw new BdfFormatError(`unknown opcode 0x${code.toString(16)} at ${r.pos - 1}`);
    }
  }
}
var NoopSink = class {
  save() {
  }
  restore() {
  }
  transform(_a, _b, _c, _d, _e, _f) {
  }
  translate(_x, _y) {
  }
  scale(_x, _y) {
  }
  clipPath(_path, _rule) {
  }
  clipRect(_x, _y, _w, _h) {
  }
  fillColor(_rgba) {
  }
  fillPaint(_paint) {
  }
  strokeColor(_rgba) {
  }
  strokePaint(_paint) {
  }
  line(_width, _cap, _join, _miter) {
  }
  dash(_segments, _offset) {
  }
  alpha(_a) {
  }
  blend(_mode) {
  }
  shadow(_rgba, _blur, _dx, _dy) {
  }
  filter(_css) {
  }
  font(_font, _size) {
  }
  textStyle(_align, _baseline, _dir, _letterSpacing) {
  }
  fillRect(_x, _y, _w, _h) {
  }
  strokeRect(_x, _y, _w, _h) {
  }
  fillPath(_path, _rule) {
  }
  strokePath(_path) {
  }
  fillPathAt(_path, _rule, _x, _y) {
  }
  fillPathRun(_rule, _glyphs) {
  }
  clearRect(_x, _y, _w, _h) {
  }
  fillText(_text, _x, _y, _advance) {
  }
  strokeText(_text, _x, _y, _advance) {
  }
  image(_img, _x, _y, _w, _h) {
  }
  imageSub(_img, _sx, _sy, _sw, _sh, _dx, _dy, _dw, _dh) {
  }
  smoothing(_enabled, _quality) {
  }
  use(_obj) {
  }
  useAt(_obj, _x, _y) {
  }
  groupBegin(_alpha, _blend, _x, _y, _w, _h) {
  }
  groupEnd() {
  }
  maskBegin(_kind, _backdrop, _transfer) {
  }
  maskEnd() {
  }
  link(_x, _y, _w, _h, _url) {
  }
  mark(_kind, _payload) {
  }
  ext(_payload) {
  }
};

// packages/core/dist/container.js
var HEADER_SIZE = 32;
var MAGIC = new Uint8Array([98, 100, 102, 0]);
var MAX_MANIFEST_SIZE = 256 << 20;
var MAX_SINGLE_FILE_SIZE = 1 << 30;
function parseHeader(bytes) {
  const r = new ByteReader(bytes);
  const m = r.bytesN(4);
  if (!MAGIC.every((b, i) => m[i] === b))
    throw new BdfFormatError("bad magic");
  const version = r.u16();
  if (version !== FORMAT_VERSION)
    throw new BdfFormatError(`unsupported format version ${version}`);
  const flags = r.u16();
  if ((flags & ~1) !== 0)
    throw new BdfFormatError(`unsupported header flags ${flags}`);
  const manifestOff = r.u64();
  const manifestLen = r.u64();
  const encoding = r.u8();
  if (encoding > 1)
    throw new BdfFormatError(`unknown manifest encoding ${encoding}`);
  const reserved = r.bytesN(7);
  if (reserved.some((b) => b !== 0))
    throw new BdfFormatError("non-zero reserved header bytes");
  const manifestEnc = encoding === 1 ? "deflate-raw" : "identity";
  return { version, flags, manifestOff, manifestLen, manifestEnc };
}
async function decode(bytes, enc, limit) {
  if (!enc || enc === "identity")
    return bytes;
  if (enc !== "deflate-raw")
    throw new BdfFormatError(`unknown encoding ${enc}`);
  if (!Number.isInteger(limit) || limit < 0)
    throw new BdfFormatError(`bad part size ${limit}`);
  const ds = new DecompressionStream("deflate-raw");
  const reader = new Blob([bytes]).stream().pipeThrough(ds).getReader();
  const chunks = [];
  let n = 0;
  for (; ; ) {
    const { done, value } = await reader.read();
    if (done)
      break;
    n += value.length;
    if (n > limit) {
      reader.cancel().catch(() => {
      });
      throw new BdfFormatError("data inflates to more than its stated size");
    }
    chunks.push(value);
  }
  const out = new Uint8Array(n);
  let at = 0;
  for (const c of chunks) {
    out.set(c, at);
    at += c.length;
  }
  return out;
}
function inRange(off, n, size) {
  return Number.isInteger(off) && Number.isInteger(n) && off >= 0 && n >= 0 && off <= size && n <= size - off;
}
async function readBounded(res, limit, what) {
  if (!inRange(0, limit, Number.MAX_SAFE_INTEGER))
    throw new BdfFormatError(`${what} size out of range`);
  const declared = res.headers.get("Content-Length");
  const encoding = res.headers.get("Content-Encoding");
  if ((!encoding || encoding === "identity") && declared !== null && /^\d+$/.test(declared) && Number(declared) > limit) {
    await res.body?.cancel();
    throw new BdfFormatError(`${what} exceeds its stated size`);
  }
  const reader = res.body?.getReader();
  if (!reader)
    return new Uint8Array(0);
  const chunks = [];
  let size = 0;
  for (; ; ) {
    const { done, value } = await reader.read();
    if (done)
      break;
    if (value.length > limit - size) {
      await reader.cancel();
      throw new BdfFormatError(`${what} exceeds its stated size`);
    }
    size += value.length;
    chunks.push(value);
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.length;
  }
  return bytes;
}
async function readExact(res, size, what) {
  const bytes = await readBounded(res, size, what);
  if (bytes.length !== size)
    throw new BdfFormatError(`${what} is shorter than its stated size`);
  return bytes;
}
function checkHash(h, what = "part name") {
  if (typeof h !== "string" || !/^[0-9a-f]{32}$/.test(h))
    throw new BdfFormatError(`bad ${what} ${JSON.stringify(h)}`);
}
var utf8 = new TextDecoder();
var BufferSource = class {
  bytes;
  header;
  manifestPromise;
  constructor(bytes) {
    this.bytes = bytes;
    this.header = parseHeader(bytes);
  }
  manifest() {
    this.manifestPromise ??= (async () => {
      const h = this.header;
      if (h.manifestLen > MAX_MANIFEST_SIZE)
        throw new BdfFormatError("manifest too large");
      if (!inRange(h.manifestOff, h.manifestLen, this.bytes.length))
        throw new BdfFormatError("manifest out of range");
      const raw = this.bytes.subarray(h.manifestOff, h.manifestOff + h.manifestLen);
      const manifest = JSON.parse(utf8.decode(await decode(raw, h.manifestEnc, MAX_MANIFEST_SIZE)));
      if ((h.flags & 1) !== 0 !== (manifest.encryption !== void 0)) {
        throw new BdfFormatError("encryption flag does not match the manifest");
      }
      return manifest;
    })();
    return this.manifestPromise;
  }
  async stored(e) {
    const base = this.header.manifestOff + this.header.manifestLen;
    if (!inRange(e.off ?? 0, e.len, this.bytes.length - base))
      throw new BdfFormatError("part out of range");
    const off = base + (e.off ?? 0);
    return this.bytes.subarray(off, off + e.len);
  }
};
var RangeSource = class {
  url;
  init;
  maxFileSize;
  header;
  manifestPromise;
  /** The whole file, once a server has answered a range request with it. */
  whole;
  constructor(url, init = {}, maxFileSize = MAX_SINGLE_FILE_SIZE) {
    this.url = url;
    this.init = init;
    this.maxFileSize = maxFileSize;
  }
  async range(off, len) {
    if (!inRange(off, len, Number.MAX_SAFE_INTEGER))
      throw new BdfFormatError("part out of range");
    if (len === 0)
      return new Uint8Array(0);
    if (!this.whole) {
      const res = await fetch(this.url, { ...this.init, headers: { ...this.init.headers, Range: `bytes=${off}-${off + len - 1}` } });
      if (res.status === 206) {
        const contentRange = res.headers.get("Content-Range");
        if (contentRange !== null) {
          const match = /^bytes (\d+)-(\d+)\/(?:\d+|\*)$/.exec(contentRange);
          if (!match || Number(match[1]) !== off || Number(match[2]) !== off + len - 1) {
            await res.body?.cancel();
            throw new BdfFormatError("range response does not match the request");
          }
        }
        return readExact(res, len, "range response");
      }
      if (res.status !== 200)
        throw new Error(`bdf: range request failed with ${res.status}`);
      if (this.whole) {
        res.body?.cancel().catch(() => {
        });
      } else {
        this.whole = readBounded(res, this.maxFileSize, "single-file response");
        this.whole.catch(() => {
          this.whole = void 0;
        });
      }
    }
    const all = await this.whole;
    if (!inRange(off, len, all.length))
      throw new BdfFormatError("part out of range");
    return all.subarray(off, off + len);
  }
  manifest() {
    this.manifestPromise ??= (async () => {
      this.header = parseHeader(await this.range(0, HEADER_SIZE));
      const h = this.header;
      if (h.manifestLen > MAX_MANIFEST_SIZE)
        throw new BdfFormatError("manifest too large");
      const raw = await this.range(h.manifestOff, h.manifestLen);
      const manifest = JSON.parse(utf8.decode(await decode(raw, h.manifestEnc, MAX_MANIFEST_SIZE)));
      if ((h.flags & 1) !== 0 !== (manifest.encryption !== void 0)) {
        throw new BdfFormatError("encryption flag does not match the manifest");
      }
      return manifest;
    })();
    return this.manifestPromise;
  }
  async stored(e) {
    await this.manifest();
    const h = this.header;
    if (!inRange(e.off ?? 0, e.len, Number.MAX_SAFE_INTEGER))
      throw new BdfFormatError("part out of range");
    return this.range(h.manifestOff + h.manifestLen + (e.off ?? 0), e.len);
  }
};
var SplitSource = class {
  base;
  init;
  manifestPromise;
  constructor(base, init = {}) {
    this.base = base;
    this.init = init;
    if (!this.base.endsWith("/"))
      this.base += "/";
  }
  manifest() {
    this.manifestPromise ??= fetch(this.base + "manifest.json", this.init).then(async (res) => {
      if (!res.ok)
        throw new Error(`bdf: manifest fetch failed with ${res.status}`);
      return JSON.parse(utf8.decode(await readBounded(res, MAX_MANIFEST_SIZE, "manifest")));
    });
    return this.manifestPromise;
  }
  async stored(e) {
    checkHash(e.h);
    const res = await fetch(this.base + "parts/" + e.h, this.init);
    if (!res.ok)
      throw new Error(`bdf: part ${e.h} fetch failed with ${res.status}`);
    return readExact(res, e.len, `part ${e.h}`);
  }
};
async function fetchSingle(url, init, maxFileSize = MAX_SINGLE_FILE_SIZE) {
  const res = await fetch(url, init);
  if (!res.ok)
    throw new Error(`bdf: fetch failed with ${res.status}`);
  return new BufferSource(await readBounded(res, maxFileSize, "single-file response"));
}

// packages/core/dist/manifest.js
var MAX_PAGE_SIZE = 1 << 24;
var MAX_SHEET_ENTRIES = 1 << 24;
var MIN_ENTRY_SIZE = 1 / 64;
var DEFAULT_TILE = 2048;
function tileSize(view) {
  return view.tile !== void 0 && view.tile > 0 ? view.tile : DEFAULT_TILE;
}
var isNumber = (v) => typeof v === "number" && Number.isFinite(v);
var isCount = (v) => Number.isInteger(v) && v >= 0;
function checkSize(v, what) {
  if (!isNumber(v) || v < 0 || v > MAX_PAGE_SIZE)
    throw new BdfFormatError(`${what} ${JSON.stringify(v)} out of range`);
}
function checkRect(r, what) {
  for (const v of [r.x, r.y]) {
    if (!isNumber(v) || Math.abs(v) > MAX_PAGE_SIZE)
      throw new BdfFormatError(`${what} at ${JSON.stringify(v)} out of range`);
  }
  checkSize(r.w, `${what} width`);
  checkSize(r.h, `${what} height`);
}
function checkRuns(runs, what) {
  if (runs === void 0)
    return;
  if (!Array.isArray(runs))
    throw new BdfFormatError(`${what} are not a list`);
  let total = 0;
  for (const run of runs) {
    if (!Array.isArray(run))
      throw new BdfFormatError(`${what}: a run is not a pair`);
    const [n, size] = run;
    if (!isCount(n) || (total += n) > MAX_SHEET_ENTRIES)
      throw new BdfFormatError(`more than ${MAX_SHEET_ENTRIES} ${what}`);
    if (!isNumber(size) || size < 0 || size > MAX_PAGE_SIZE || size > 0 && size < MIN_ENTRY_SIZE)
      throw new BdfFormatError(`${what} of size ${JSON.stringify(size)}`);
  }
}
function checkView(v) {
  if (v.pages !== void 0) {
    if (!Array.isArray(v.pages))
      throw new BdfFormatError(`view ${v.id}: pages are not a list`);
    for (const p of v.pages) {
      checkSize(p?.w, "page width");
      checkSize(p.h, "page height");
      if (p.body !== void 0)
        checkRect(p.body, "page body");
    }
  }
  if (v.continuous !== void 0 && (!isNumber(v.continuous?.gap) || Math.abs(v.continuous.gap) > MAX_PAGE_SIZE)) {
    throw new BdfFormatError(`view ${v.id}: bad gap`);
  }
  if (v.tile !== void 0 && (!isNumber(v.tile) || v.tile > 0 && v.tile < 1))
    throw new BdfFormatError(`tile size ${JSON.stringify(v.tile)} out of range`);
  checkRuns(v.cols, "columns");
  checkRuns(v.rows, "rows");
  if (v.freeze !== void 0) {
    for (const n of [v.freeze?.cols ?? 0, v.freeze?.rows ?? 0])
      if (!isCount(n))
        throw new BdfFormatError(`view ${v.id}: bad frozen panes`);
  }
}
function checkManifest(m) {
  if (m.bdf !== FORMAT_VERSION)
    throw new BdfFormatError(`unsupported manifest format version ${m.bdf}`);
  if (m.opset !== void 0 && (!Number.isInteger(m.opset) || m.opset < 0 || m.opset > OPSET_VERSION)) {
    throw new BdfFormatError(`unsupported manifest opset ${m.opset}`);
  }
  if (!Array.isArray(m.parts))
    throw new BdfFormatError("manifest without parts");
  for (const e of m.parts) {
    checkHash(e?.h);
    if (e.sealed !== void 0)
      checkHash(e.sealed, "sealed part name");
  }
  if (!Array.isArray(m.views))
    throw new BdfFormatError("manifest without views");
  for (const v of m.views) {
    if (typeof v?.id !== "string")
      throw new BdfFormatError("view without an id");
    checkView(v);
  }
}

// packages/core/dist/crypto.js
var MAX_ITERATIONS = 1e7;
var MAX_ECDH_SLOTS = 16;
var NONCE_SIZE = 12;
var TAG_SIZE = 16;
var MANIFEST_AAD = new TextEncoder().encode("manifest");
var ECDH_INFO = new TextEncoder().encode("bdf ecdh v1");
var utf82 = new TextDecoder();
var BdfPasswordError = class extends Error {
  reason;
  constructor(reason) {
    super(reason === "required" ? "bdf: the document is encrypted and needs a password" : "bdf: wrong password");
    this.reason = reason;
    this.name = "BdfPasswordError";
  }
};
function base64(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++)
    out[i] = bin.charCodeAt(i);
  return out;
}
function hexBytes(h) {
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++)
    out[i] = parseInt(h.slice(2 * i, 2 * i + 2), 16);
  return out;
}
function contentKey(enc, secret) {
  if (enc.cipher !== "A256GCM")
    throw new BdfFormatError(`unknown cipher ${enc.cipher}`);
  return typeof secret === "string" ? passwordKey(enc, secret) : ecdhKey(enc, secret);
}
async function passwordKey(enc, password) {
  const pw = new TextEncoder().encode(password.normalize("NFC"));
  let spent = 0;
  for (const slot of enc.keys) {
    if (slot.type !== "password")
      continue;
    if (slot.kdf !== "PBKDF2-SHA256")
      throw new BdfFormatError(`unknown key derivation ${slot.kdf}`);
    if (!Number.isInteger(slot.iter) || slot.iter < 1 || slot.iter > MAX_ITERATIONS)
      throw new BdfFormatError(`iteration count ${slot.iter} out of range`);
    if ((spent += slot.iter) > MAX_ITERATIONS)
      throw new BdfFormatError(`the key slots take more than ${MAX_ITERATIONS} iterations`);
    if (pw.length === 0)
      break;
    const base = await crypto.subtle.importKey("raw", pw, "PBKDF2", false, ["deriveKey"]);
    const kek = await crypto.subtle.deriveKey({ name: "PBKDF2", hash: "SHA-256", salt: base64(slot.salt), iterations: slot.iter }, base, { name: "AES-KW", length: 256 }, false, ["unwrapKey"]);
    try {
      return await crypto.subtle.unwrapKey("raw", base64(slot.key), kek, "AES-KW", { name: "AES-GCM", length: 256 }, false, ["decrypt"]);
    } catch {
    }
  }
  throw new BdfPasswordError("wrong");
}
async function ecdhKey(enc, keyPair) {
  const rpk = new Uint8Array(await crypto.subtle.exportKey("raw", keyPair.publicKey));
  let tried = 0;
  for (const slot of enc.keys) {
    if (slot.type !== "ecdh")
      continue;
    if (slot.crv !== "P-256")
      throw new BdfFormatError(`unknown curve ${slot.crv}`);
    if (slot.kdf !== "HKDF-SHA256")
      throw new BdfFormatError(`unknown key derivation ${slot.kdf}`);
    if (++tried > MAX_ECDH_SLOTS)
      throw new BdfFormatError(`more than ${MAX_ECDH_SLOTS} ecdh key slots`);
    const epk = base64(slot.epk);
    let writer;
    try {
      writer = await crypto.subtle.importKey("raw", epk, { name: "ECDH", namedCurve: "P-256" }, false, []);
    } catch {
      throw new BdfFormatError("bad public key in an ecdh key slot");
    }
    const z = await crypto.subtle.deriveKey({ name: "ECDH", public: writer }, keyPair.privateKey, { name: "HKDF" }, false, ["deriveKey"]);
    const both = new Uint8Array(epk.length + rpk.length);
    both.set(epk);
    both.set(rpk, epk.length);
    const salt = await crypto.subtle.digest("SHA-256", both);
    const kek = await crypto.subtle.deriveKey({ name: "HKDF", hash: "SHA-256", salt, info: ECDH_INFO }, z, { name: "AES-KW", length: 256 }, false, ["unwrapKey"]);
    try {
      return await crypto.subtle.unwrapKey("raw", base64(slot.key), kek, "AES-KW", { name: "AES-GCM", length: 256 }, false, ["decrypt"]);
    } catch {
    }
  }
  throw new BdfKeyError();
}
var BdfKeyError = class extends Error {
  constructor() {
    super("bdf: the document is not sealed for this key");
    this.name = "BdfKeyError";
  }
};
async function open(key, sealed, aad) {
  if (sealed.length < NONCE_SIZE + TAG_SIZE)
    throw new BdfFormatError("sealed part too short");
  const bytes = sealed.buffer instanceof ArrayBuffer ? sealed : sealed.slice();
  try {
    return new Uint8Array(await crypto.subtle.decrypt({ name: "AES-GCM", iv: bytes.subarray(0, NONCE_SIZE), additionalData: aad }, key, bytes.subarray(NONCE_SIZE)));
  } catch {
    throw new BdfFormatError("sealed part fails authentication");
  }
}
var SealedSource = class _SealedSource {
  source;
  outer;
  key;
  inner;
  constructor(source, outer, key, inner) {
    this.source = source;
    this.outer = outer;
    this.key = key;
    this.inner = inner;
  }
  /**
   * Unlock an encrypted document with its password, or with the key pair it
   * was sealed for (an ecdh key slot); throws BdfPasswordError("wrong") when
   * the password opens no key slot, BdfKeyError when the key pair opens none.
   */
  static async unlock(source, secret) {
    const stored = await source.manifest();
    if (stored.bdf !== FORMAT_VERSION)
      throw new BdfFormatError(`unsupported manifest format version ${stored.bdf}`);
    const enc = stored.encryption;
    if (!enc)
      throw new Error("bdf: the document is not encrypted");
    const key = await contentKey(enc, secret);
    for (const e of stored.parts)
      checkHash(e.h);
    const outer = new Map(stored.parts.map((e) => [e.h, e]));
    const me = outer.get(enc.manifest.part);
    if (!me)
      throw new BdfFormatError("sealed manifest part missing");
    if (!Number.isSafeInteger(me.len) || me.len < 0 || me.len > MAX_MANIFEST_SIZE + NONCE_SIZE + TAG_SIZE) {
      throw new BdfFormatError("sealed manifest size out of range");
    }
    const json = await decode(await open(key, await source.stored(me), MANIFEST_AAD), enc.manifest.enc, MAX_MANIFEST_SIZE);
    if (json.length > MAX_MANIFEST_SIZE)
      throw new BdfFormatError("manifest too large");
    const inner = JSON.parse(utf82.decode(json));
    if (inner.encryption)
      throw new BdfFormatError("sealed manifest is encrypted again");
    for (const e of inner.parts) {
      checkHash(e.h);
      if (!e.sealed || !outer.has(e.sealed))
        throw new BdfFormatError(`part ${e.h}: sealed part missing`);
    }
    return new _SealedSource(source, outer, key, inner);
  }
  manifest() {
    return Promise.resolve(this.inner);
  }
  async stored(e) {
    const o = e.sealed && this.outer.get(e.sealed);
    if (!o)
      throw new BdfFormatError(`part ${e.h}: sealed part missing`);
    return open(this.key, await this.source.stored(o), hexBytes(e.h));
  }
};

// packages/core/dist/cues.js
var u32 = (v) => v % 4294967296;
function decodeCues(bytes) {
  if (bytes.length < 4 || bytes[0] !== 66 || bytes[1] !== 67 || bytes[2] !== 85 || bytes[3] !== 69)
    throw new BdfFormatError("not a cue index part");
  const r = new ByteReader(bytes);
  r.pos = 4;
  const version = r.u16();
  if (version > 1)
    throw new BdfFormatError(`unsupported cue index version ${version}`);
  let n = r.varuint();
  if (n > bytes.length)
    throw new BdfFormatError("bad system count");
  const systems = [];
  for (let i = 0; i < n; i++)
    systems.push({ page: u32(r.varuint()), x: r.f32(), y: r.f32(), w: r.f32(), h: r.f32() });
  n = r.varuint();
  if (n > bytes.length)
    throw new BdfFormatError("bad cue count");
  const cues = [];
  let t = 0;
  for (let i = 0; i < n; i++) {
    t = u32(t + u32(r.varuint()));
    const q = { tick: t, system: u32(r.varuint()), x: r.f32() };
    if (q.system >= systems.length)
      throw new BdfFormatError("cue refers to a missing system");
    cues.push(q);
  }
  return { systems, cues };
}

// packages/core/dist/text.js
var Mark = {
  PARAGRAPH: 0,
  LINE: 1,
  CELL: 2,
  BOX: 3,
  ALT_TEXT: 4,
  WRAP: 5,
  HEADING: 6,
  LIST: 7,
  LIST_ITEM: 8,
  TABLE: 9,
  FIGURE: 10,
  END: 11,
  LANG: 12
};
var Sep = { NONE: 0, SPACE: 1, BREAK: 2 };
function parseCellRef(payload) {
  const m = /^\s*([A-Za-z]+)(\d+)(?::([A-Za-z]+)(\d+))?(?:\s+(col|row))?\s*$/.exec(payload);
  if (!m)
    return void 0;
  const colOf = (s) => [...s.toUpperCase()].reduce((a, ch) => a * 26 + ch.charCodeAt(0) - 64, 0) - 1;
  const row = Number(m[2]) - 1, col = colOf(m[1]);
  if (row < 0)
    return void 0;
  const row2 = m[4] ? Number(m[4]) - 1 : row, col2 = m[3] ? colOf(m[3]) : col;
  const ref = { row, col, rows: Math.max(1, row2 - row + 1), cols: Math.max(1, col2 - col + 1) };
  if (m[5])
    ref.scope = m[5];
  return ref;
}
function mul(m, n) {
  return [
    m[0] * n[0] + m[2] * n[1],
    m[1] * n[0] + m[3] * n[1],
    m[0] * n[2] + m[2] * n[3],
    m[1] * n[2] + m[3] * n[3],
    m[0] * n[4] + m[2] * n[5] + m[4],
    m[1] * n[4] + m[3] * n[5] + m[5]
  ];
}
function boxOf(m, x, y, w, h) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const [px, py] of [[x, y], [x + w, y], [x, y + h], [x + w, y + h]]) {
    const tx = m[0] * px + m[2] * py + m[4], ty = m[1] * px + m[3] * py + m[5];
    x0 = Math.min(x0, tx);
    y0 = Math.min(y0, ty);
    x1 = Math.max(x1, tx);
    y1 = Math.max(y1, ty);
  }
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}
function intersect(a, b) {
  if (!b)
    return a;
  const x0 = Math.max(a.x, b.x), y0 = Math.max(a.y, b.y);
  const x1 = Math.min(a.x + a.w, b.x + b.w), y1 = Math.min(a.y + a.h, b.y + b.h);
  return x1 >= x0 && y1 >= y0 ? { x: x0, y: y0, w: x1 - x0, h: y1 - y0 } : void 0;
}
function union(a, b) {
  if (!a)
    return b;
  const x0 = Math.min(a.x, b.x), y0 = Math.min(a.y, b.y);
  return { x: x0, y: y0, w: Math.max(a.x + a.w, b.x + b.w) - x0, h: Math.max(a.y + a.h, b.y + b.h) - y0 };
}
function pathBox(p) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  const add = (x, y) => {
    x0 = Math.min(x0, x);
    y0 = Math.min(y0, y);
    x1 = Math.max(x1, x);
    y1 = Math.max(y1, y);
  };
  const a = p.args;
  let i = 0;
  for (const v of p.verbs) {
    switch (v) {
      case 0:
      case 1:
        add(a[i], a[i + 1]);
        i += 2;
        break;
      // MOVE, LINE
      case 2:
        add(a[i], a[i + 1]);
        add(a[i + 2], a[i + 3]);
        i += 4;
        break;
      // QUAD
      case 3:
        add(a[i], a[i + 1]);
        add(a[i + 2], a[i + 3]);
        add(a[i + 4], a[i + 5]);
        i += 6;
        break;
      // CUBIC
      case 4:
        break;
      // CLOSE
      case 5:
        add(a[i], a[i + 1]);
        add(a[i] + a[i + 2], a[i + 1] + a[i + 3]);
        i += 4;
        break;
      // RECT
      case 6: {
        const r = Math.max(Math.abs(a[i + 2]), Math.abs(a[i + 3]));
        add(a[i] - r, a[i + 1] - r);
        add(a[i] + r, a[i + 1] + r);
        i += 8;
        break;
      }
      // ELLIPSE
      case 7:
        add(a[i], a[i + 1]);
        add(a[i + 2], a[i + 3]);
        i += 5;
        break;
      // ARC_TO
      case 8:
        add(a[i], a[i + 1]);
        add(a[i] + a[i + 2], a[i + 1] + a[i + 3]);
        i += 5;
        break;
    }
  }
  return x1 >= x0 ? { x: x0, y: y0, w: x1 - x0, h: y1 - y0 } : void 0;
}
var Extraction = class {
  resolve;
  limits;
  runs = [];
  pending = Sep.NONE;
  hasMark = false;
  /** ALT_TEXT waiting for the drawing op it describes. */
  alt;
  nodes = [];
  links = [];
  /** Open containers, outermost first. */
  open = [];
  /** Leaf the next run joins (-1: start a new one). */
  leaf = -1;
  /** Leaf kind reserved by a leaf MARK for the next run. */
  reserved;
  lang = "";
  /** Open figures (their bounds grow with what is drawn). */
  figures = [];
  /** Where each open figure started, for figures that draw nothing. */
  origins = /* @__PURE__ */ new Map();
  constructor(resolve, limits) {
    this.resolve = resolve;
    this.limits = limits;
  }
  mark(sep) {
    if (!this.hasMark || sep > this.pending)
      this.pending = sep;
    this.hasMark = true;
  }
  /** Emit an ALT_TEXT that no drawing op consumed, without a position. */
  flushAlt(st) {
    if (this.alt !== void 0) {
      const t = this.alt;
      this.alt = void 0;
      this.emit(t, 0, 0, 0, st, true);
    }
  }
  emit(text, x, y, advance, st, altText) {
    if (this.runs.length >= this.limits.maxRuns)
      throw new BdfFormatError("too many text runs");
    const m = st.m;
    const run = {
      text,
      advance,
      size: st.size,
      font: st.font,
      align: st.align,
      matrix: m,
      altText,
      x: m[0] * x + m[2] * y + m[4],
      y: m[1] * x + m[3] * y + m[5],
      sep: Sep.NONE,
      ordinal: this.runs.length
    };
    if (this.hasMark)
      run.sep = this.pending;
    else if (this.runs.length)
      run.sep = guessSep(this.runs[this.runs.length - 1], run);
    this.pending = Sep.NONE;
    this.hasMark = false;
    run.node = this.leafNode();
    if (this.lang)
      run.lang = this.lang;
    if (this.figures.length) {
      const w = advance > 0 ? advance : st.size * text.length * 0.5;
      const anchor = st.align === 1 ? -w : st.align === 2 ? -w / 2 : 0;
      this.grow(boxOf(m, x + anchor, y - st.size * 0.8, w, st.size), st.clip);
    }
    this.runs.push(run);
  }
  top() {
    return this.open.length ? this.open[this.open.length - 1] : -1;
  }
  topKind() {
    const t = this.top();
    return t >= 0 ? this.nodes[t].kind : void 0;
  }
  add(n) {
    this.nodes.push(n);
    return this.nodes.length - 1;
  }
  /** The leaf of the next run, created from the reserved kind or implicitly. */
  leafNode() {
    if (this.leaf >= 0 && !this.reserved)
      return this.leaf;
    const r = this.reserved;
    this.reserved = void 0;
    if (this.topKind() === "list")
      this.openNode({ kind: "item" });
    const parent = this.top();
    if (this.topKind() === "table")
      this.leaf = this.add({ kind: "caption", parent });
    else
      this.leaf = this.add({ ...r ?? { kind: "paragraph" }, parent });
    return this.leaf;
  }
  openNode(t, st) {
    const i = this.add({ ...t, parent: this.top() });
    this.open.push(i);
    this.leaf = -1;
    this.reserved = void 0;
    if (t.kind === "figure") {
      this.figures.push(i);
      this.origins.set(i, { x: st?.m[4] ?? 0, y: st?.m[5] ?? 0, w: 0, h: 0 });
    }
    return i;
  }
  closeTop() {
    const i = this.open.pop();
    this.leaf = -1;
    this.reserved = void 0;
    if (i === void 0 || this.nodes[i].kind !== "figure")
      return;
    this.figures = this.figures.filter((f) => f !== i);
    this.nodes[i].bounds ??= this.origins.get(i);
    this.origins.delete(i);
  }
  closeAll() {
    while (this.open.length)
      this.closeTop();
  }
  /** Apply a structure MARK (spec §7.8). */
  structure(kind, payload, st) {
    switch (kind) {
      case Mark.PARAGRAPH:
      case Mark.BOX:
        this.reserved = { kind: "paragraph" };
        break;
      case Mark.HEADING: {
        const level = Number(payload);
        this.reserved = Number.isInteger(level) && level >= 1 && level <= 6 ? { kind: "heading", level } : { kind: "heading" };
        break;
      }
      case Mark.LIST:
        this.openNode({ kind: "list" });
        break;
      case Mark.LIST_ITEM:
        if (this.topKind() === "item")
          this.closeTop();
        if (this.topKind() === "list")
          this.openNode({ kind: "item" });
        else
          this.reserved = { kind: "paragraph" };
        break;
      case Mark.TABLE:
        this.openNode({ kind: "table" });
        break;
      case Mark.CELL: {
        const ref = parseCellRef(payload);
        if (this.topKind() === "cell")
          this.closeTop();
        if (this.topKind() === "table")
          this.openNode({ kind: "cell", ...ref });
        else
          this.reserved = { kind: "cell", ...ref };
        break;
      }
      case Mark.FIGURE:
        this.openNode({ kind: "figure", alt: payload }, st);
        break;
      case Mark.END: {
        const k = this.topKind();
        if (k === "item" || k === "cell")
          this.closeTop();
        if (this.open.length)
          this.closeTop();
        break;
      }
    }
  }
  /** Add a drawn area to the open figures. */
  grow(box, clip) {
    const b = box && intersect(box, clip);
    if (!b)
      return;
    for (const f of this.figures)
      this.nodes[f].bounds = union(this.nodes[f].bounds, b);
  }
  link(x, y, w, h, url, m) {
    this.links.push({ ...boxOf(m, x, y, w, h), url, after: this.runs.length - 1, node: this.leaf >= 0 ? this.leaf : this.top() });
  }
};
function guessSep(prev, cur) {
  const size = prev.size > 0 ? prev.size : 10;
  if (Math.abs(cur.y - prev.y) > size * 0.5)
    return Sep.SPACE;
  const end = prev.x + prev.advance;
  if (prev.advance === 0 || cur.x - end > Math.fround(0.2) * size)
    return Sep.SPACE;
  return Sep.NONE;
}
var TextSink = class _TextSink extends NoopSink {
  ex;
  obj;
  depth;
  reused;
  stack = [];
  st;
  masking = 0;
  // inside MASK_BEGIN … MASK_END: a soft mask, not content
  /** depth: of the object in the objects that draw it (0: a top-level one); reused: its instructions are read again. */
  constructor(ex, obj, m, clip, depth = 0, reused = false) {
    super();
    this.ex = ex;
    this.obj = obj;
    this.depth = depth;
    this.reused = reused;
    this.st = { m, font: void 0, size: 10, align: 0, clip };
  }
  save() {
    this.stack.push({ ...this.st });
  }
  restore() {
    this.st = this.stack.pop() ?? this.st;
  }
  transform(a, b, c, d, e, f) {
    this.st.m = mul(this.st.m, [a, b, c, d, e, f]);
  }
  translate(x, y) {
    this.transform(1, 0, 0, 1, x, y);
  }
  scale(x, y) {
    this.transform(x, 0, 0, y, 0, 0);
  }
  font(font, size) {
    this.st.font = this.obj.fonts[font];
    this.st.size = size;
  }
  textStyle(align) {
    this.st.align = align;
  }
  maskBegin() {
    this.save();
    this.masking++;
  }
  maskEnd() {
    if (this.masking) {
      this.masking--;
      this.restore();
    }
  }
  mark(kind, payload) {
    if (this.masking)
      return;
    switch (kind) {
      case Mark.LINE:
        this.ex.flushAlt(this.st);
        this.ex.mark(Sep.SPACE);
        break;
      case Mark.PARAGRAPH:
      case Mark.CELL:
      case Mark.BOX:
      case Mark.HEADING:
      case Mark.LIST:
      case Mark.LIST_ITEM:
      case Mark.TABLE:
      case Mark.FIGURE:
      case Mark.END:
        this.ex.flushAlt(this.st);
        this.ex.mark(Sep.BREAK);
        this.ex.structure(kind, payload, this.st);
        break;
      case Mark.ALT_TEXT:
        this.ex.flushAlt(this.st);
        this.ex.alt = payload;
        break;
      case Mark.WRAP:
        this.ex.flushAlt(this.st);
        this.ex.mark(Sep.NONE);
        break;
      // no separator, and a pending ALT_TEXT still belongs to the next drawing op
      case Mark.LANG:
        this.ex.lang = payload;
        break;
    }
  }
  link(x, y, w, h, url) {
    if (!this.masking)
      this.ex.link(x, y, w, h, url, this.st.m);
  }
  // --- figure bounds: only computed while a figure is open ---
  path(i) {
    const p = this.obj.paths[i];
    return p && "inline" in p ? p.inline : void 0;
  }
  grow(x, y, w, h) {
    if (this.ex.figures.length && !this.masking)
      this.ex.grow(boxOf(this.st.m, x, y, w, h), this.st.clip);
  }
  growPath(i, dx = 0, dy = 0) {
    if (!this.ex.figures.length)
      return;
    const b = this.path(i) && pathBox(this.path(i));
    if (b)
      this.grow(b.x + dx, b.y + dy, b.w, b.h);
  }
  clipRect(x, y, w, h) {
    this.st.clip = intersect(boxOf(this.st.m, x, y, w, h), this.st.clip) ?? { x: 0, y: 0, w: 0, h: 0 };
  }
  clipPath(path) {
    const b = this.path(path) && pathBox(this.path(path));
    if (b)
      this.st.clip = intersect(boxOf(this.st.m, b.x, b.y, b.w, b.h), this.st.clip) ?? { x: 0, y: 0, w: 0, h: 0 };
  }
  fillRect(x, y, w, h) {
    this.grow(x, y, w, h);
  }
  strokeRect(x, y, w, h) {
    this.grow(x, y, w, h);
  }
  fillPath(path) {
    this.growPath(path);
  }
  strokePath(path) {
    this.growPath(path);
  }
  image(_img, x, y, w, h) {
    this.grow(x, y, w, h);
  }
  imageSub(_img, _sx, _sy, _sw, _sh, dx, dy, dw, dh) {
    this.grow(dx, dy, dw, dh);
  }
  /** The drawing op right after ALT_TEXT renders that text; returns true when consumed. */
  takeAlt(x, y, advance) {
    if (this.ex.alt === void 0 || this.masking)
      return false;
    const t = this.ex.alt;
    this.ex.alt = void 0;
    this.ex.emit(t, x, y, advance, this.st, true);
    return true;
  }
  fillText(text, x, y, advance) {
    if (!this.masking && !this.takeAlt(x, y, advance))
      this.ex.emit(text, x, y, advance, this.st, false);
  }
  strokeText(text, x, y, advance) {
    if (!this.masking && !this.takeAlt(x, y, advance))
      this.ex.emit(text, x, y, advance, this.st, false);
  }
  fillPathAt(path, _rule, x, y) {
    this.growPath(path, x, y);
    this.takeAlt(x, y, 0);
  }
  fillPathRun(_rule, glyphs) {
    for (const g of glyphs)
      this.growPath(g.path, g.x, g.y);
    this.takeAlt(glyphs[0]?.x ?? 0, glyphs[0]?.y ?? 0, 0);
  }
  use(obj) {
    this.useAt(obj, 0, 0);
  }
  useAt(obj, x, y) {
    if (this.masking)
      return;
    const hash = this.obj.objects[obj];
    const child = this.ex.resolve(hash);
    const again = child !== void 0 && this.ex.limits.enter(hash);
    if (this.ex.alt !== void 0) {
      if (child && this.ex.figures.length)
        this.grow(child.bbox.x + x, child.bbox.y + y, child.bbox.w, child.bbox.h);
      this.takeAlt(x + (child?.bbox.x ?? 0), y, child?.bbox.w ?? 0);
      return;
    }
    if (!child)
      return;
    this.ex.limits.descend(this.depth);
    const reused = this.reused || again;
    const sink = new _TextSink(this.ex, child, mul(this.st.m, [1, 0, 0, 1, x, y]), this.st.clip, this.depth + 1, reused);
    walk(child, sink, reused ? this.ex.limits.count : void 0);
  }
};
function extract(obj, resolve, matrix, limits = new UseLimits()) {
  const ex = new Extraction(resolve, limits);
  walk(obj, new TextSink(ex, obj, matrix));
  ex.flushAlt({ m: matrix, font: void 0, size: 10, align: 0 });
  ex.closeAll();
  return ex;
}
function extractText(obj, resolve, matrix = [1, 0, 0, 1, 0, 0], limits) {
  return extract(obj, resolve, matrix, limits).runs;
}
function extractContent(obj, resolve, matrix = [1, 0, 0, 1, 0, 0], limits) {
  const ex = extract(obj, resolve, matrix, limits);
  return { runs: ex.runs, nodes: ex.nodes, links: ex.links };
}

// packages/core/dist/search.js
function decodeTextIndex(bytes) {
  const r = new ByteReader(bytes);
  const m = r.bytesN(4);
  if (m[0] !== 66 || m[1] !== 84 || m[2] !== 88 || m[3] !== 84)
    throw new BdfFormatError("not a text index part");
  const version = r.u16();
  if (version > 1)
    throw new BdfFormatError(`unsupported text index version ${version}`);
  const n = r.varuint();
  r.need(n * 5);
  const out = new Array(n);
  for (let i = 0; i < n; i++) {
    out[i] = { a: r.varuint(), b: r.varuint(), ordinal: r.varuint(), sep: r.u8(), text: r.str() };
  }
  return out;
}
function normalizeChar(ch, caseSensitive) {
  if (ch === "\u2212")
    return "-";
  let s = ch.normalize("NFKC");
  if (!caseSensitive)
    s = s.toLowerCase();
  let out = "";
  for (const c of s) {
    const cp = c.codePointAt(0);
    out += cp >= 12449 && cp <= 12534 ? String.fromCodePoint(cp - 96) : c;
  }
  return out;
}
function normalizeQuery(q, caseSensitive = false) {
  let out = "";
  for (const c of q)
    out += normalizeChar(c, caseSensitive);
  return out;
}
var TextSearch = class {
  runs;
  caseSensitive;
  norm = "";
  /** For each code unit of norm: run index and code-unit offset in that run's text (or -1 for separators). */
  runOf;
  offOf;
  /** The plain text, once it was asked for (every hit takes its context from it). */
  plain;
  constructor(runs, caseSensitive = false) {
    this.runs = runs;
    this.caseSensitive = caseSensitive;
    const runIdx = [];
    const offIdx = [];
    let norm = "";
    runs.forEach((run, ri) => {
      if (ri > 0 && run.sep !== Sep.NONE) {
        norm += run.sep === Sep.BREAK ? "\0" : " ";
        runIdx.push(-1);
        offIdx.push(-1);
      }
      let off = 0;
      for (const ch of run.text) {
        const n = normalizeChar(ch, caseSensitive);
        for (let k = 0; k < n.length; k++) {
          runIdx.push(ri);
          offIdx.push(off);
        }
        norm += n;
        off += ch.length;
      }
    });
    this.norm = norm;
    this.runOf = Int32Array.from(runIdx);
    this.offOf = Int32Array.from(offIdx);
  }
  /** The searchable plain text (paragraph breaks as newlines). */
  get text() {
    return this.plain ??= this.norm.replaceAll("\0", "\n");
  }
  search(query, opts = {}) {
    const q = normalizeQuery(query, this.caseSensitive).replace(/\s+/g, " ");
    if (!q)
      return [];
    const limit = opts.limit ?? 1e3;
    const ctxLen = opts.context ?? 40;
    const hits = [];
    let from = 0;
    while (hits.length < limit) {
      const at = this.norm.indexOf(q, from);
      if (at < 0)
        break;
      from = at + 1;
      const hit = this.locate(at, at + q.length, ctxLen);
      if (hit)
        hits.push(hit);
    }
    return hits;
  }
  locate(start, end, ctxLen) {
    const segments = [];
    for (let i = start; i < end; i++) {
      const ri = this.runOf[i];
      if (ri < 0)
        continue;
      const off = this.offOf[i];
      const run = this.runs[ri];
      const charEnd = off + ((run.text.codePointAt(off) ?? 0) > 65535 ? 2 : 1);
      const last = segments[segments.length - 1];
      if (last && last.run === ri)
        last.end = Math.max(last.end, charEnd);
      else
        segments.push({ run: ri, a: run.a, b: run.b, ordinal: run.ordinal, start: off, end: charEnd });
    }
    if (!segments.length)
      return void 0;
    let text = "";
    for (const s of segments) {
      const run = this.runs[s.run];
      text += (text && run.sep === Sep.SPACE ? " " : "") + run.text.slice(s.start, s.end);
    }
    const plain = this.text;
    const context = plain.slice(Math.max(0, start - ctxLen), Math.min(plain.length, end + ctxLen)).replace(/\n/g, " \u23CE ");
    return { segments, text, context };
  }
};

// packages/core/dist/document.js
var BdfDocument = class _BdfDocument {
  source;
  manifest;
  /** Part entries, with the source that holds each (addPage brings parts of other sources). */
  entries = /* @__PURE__ */ new Map();
  parts = /* @__PURE__ */ new Map();
  objects = /* @__PURE__ */ new Map();
  pathSets = /* @__PURE__ */ new Map();
  constructor(source, manifest) {
    this.source = source;
    this.manifest = manifest;
    checkManifest(manifest);
    for (const e of manifest.parts)
      this.entries.set(e.h, { entry: e, source });
  }
  /**
   * Open a document. An encrypted one needs options.password: without it
   * BdfPasswordError("required") is thrown, and BdfPasswordError("wrong")
   * when it does not open the document. The source's manifest is cached, so
   * the same source can be opened again with another password. One sealed
   * for a key pair needs options.keyPair, and throws BdfKeyError when it is
   * another. A manifest with part names or numbers out of range (see
   * checkManifest) is refused with BdfFormatError.
   */
  static async open(source, options = {}) {
    const manifest = await source.manifest();
    const enc = manifest.encryption;
    if (!enc)
      return new _BdfDocument(source, manifest);
    const secret = options.keyPair ?? options.password;
    if (secret === void 0) {
      if (Array.isArray(enc.keys) && !enc.keys.some((k) => k.type === "password"))
        throw new BdfKeyError();
      throw new BdfPasswordError("required");
    }
    const sealed = await SealedSource.unlock(source, secret);
    return new _BdfDocument(sealed, await sealed.manifest());
  }
  entry(hash) {
    const e = this.entries.get(hash);
    if (!e)
      throw new Error(`bdf: unknown part ${hash}`);
    return e.entry;
  }
  /**
   * Put the page of a page document (the only page of its only view: a page
   * a streamed conversion returned) in place of page index of a view, and
   * add the parts it brings. The page's objects may use parts added before.
   */
  addPage(viewId, index, from) {
    const pages = this.view(viewId).pages;
    if (!pages || index < 0 || index >= pages.length)
      throw new Error(`bdf: no page ${index} in view ${viewId}`);
    const views = from.manifest.views;
    if (views.length !== 1 || views[0].pages?.length !== 1)
      throw new Error("bdf: not a page document");
    this.adopt(from);
    pages[index] = views[0].pages[0];
  }
  /**
   * Put in the pages a segment document carries (spec §3.6): a document with
   * the same views and pages, whose manifest names the pages it carries.
   * Its parts are added to those this document has; its pages may use parts
   * that earlier segments brought.
   */
  addSegment(from) {
    const s = from.manifest.segment;
    if (!s)
      throw new Error("bdf: not a segment document");
    const mine = this.manifest.views, theirs = from.manifest.views;
    if (mine.length !== theirs.length || mine.some((v, i) => v.id !== theirs[i].id || (v.pages?.length ?? 0) !== (theirs[i].pages?.length ?? 0))) {
      throw new Error("bdf: the segment is of another document");
    }
    const src = from.view(s.view).pages ?? [], dst = this.view(s.view).pages ?? [];
    if (!Number.isInteger(s.from) || !Number.isInteger(s.to) || s.from < 0 || s.from >= s.to || s.to > dst.length) {
      throw new BdfFormatError(`segment of pages ${s.from} to ${s.to} out of range`);
    }
    this.adopt(from);
    for (let i = s.from; i < s.to; i++)
      dst[i] = src[i];
  }
  /** Add the parts of another document that this one does not have, read from its source. */
  adopt(from) {
    for (const e of from.manifest.parts) {
      if (this.entries.has(e.h))
        continue;
      this.entries.set(e.h, { entry: e, source: from.source });
      this.manifest.parts.push(e);
    }
  }
  view(id) {
    const v = this.manifest.views.find((v2) => v2.id === id);
    if (!v)
      throw new Error(`bdf: unknown view ${id}`);
    return v;
  }
  /** Decoded bytes of a part. */
  part(hash) {
    let p = this.parts.get(hash);
    if (!p) {
      const e = this.entries.get(hash);
      if (!e)
        throw new Error(`bdf: unknown part ${hash}`);
      p = e.source.stored(e.entry).then((b) => decode(b, e.entry.enc, e.entry.size ?? 0));
      this.parts.set(hash, p);
    }
    return p;
  }
  object(hash) {
    let p = this.objects.get(hash);
    if (!p) {
      p = this.part(hash).then(decodeObject);
      this.objects.set(hash, p);
    }
    return p;
  }
  /** Synchronous access to an object that has already been loaded. */
  objectSync(hash) {
    const o = this.loadedObjects.get(hash);
    if (!o)
      throw new Error(`bdf: object ${hash} not loaded`);
    return o;
  }
  loadedObjects = /* @__PURE__ */ new Map();
  /**
   * Text index runs of a view: the text index part when present, otherwise
   * built by extracting text from every object of the view (which loads them all).
   */
  async textIndex(view) {
    if (view.textIndex)
      return decodeTextIndex(await this.part(view.textIndex));
    const runs = [];
    const tile = tileSize(view);
    const keep = (r) => view.kind !== "sheet" || r.x >= 0 && r.x < tile && r.y >= 0 && r.y < tile;
    const limits = new UseLimits();
    const add = async (a, b, hash) => {
      const obj = await this.ensure(hash);
      let n = 0;
      for (const r of extractText(obj, (h) => this.objectSync(h), void 0, limits)) {
        if (!keep(r))
          continue;
        runs.push({ a, b, ordinal: r.ordinal, sep: n++ === 0 ? 2 : r.sep, text: r.text });
      }
    };
    if (view.kind === "sheet") {
      const keys = Object.keys(view.tiles ?? {}).map((k) => k.split(",").map(Number));
      keys.sort((p, q) => p[1] - q[1] || p[0] - q[0]);
      for (const [x, y] of keys)
        await add(x, y, view.tiles[`${x},${y}`]);
    } else {
      const pages = view.pages ?? [];
      for (let pi = 0; pi < pages.length; pi++) {
        for (let li = 0; li < pages[pi].layers.length; li++)
          await add(pi, li, pages[pi].layers[li].obj);
      }
    }
    return runs;
  }
  /**
   * The music of a view (docs/spec.md §4.4): its Standard MIDI File as it is
   * stored, and its cues when it has them; null for a view without play.
   */
  async play(view) {
    if (!view.play)
      return null;
    const { seq, cues } = view.play;
    const [bytes, decoded] = await Promise.all([this.part(seq), cues ? this.part(cues).then(decodeCues) : null]);
    return { seq: bytes, cues: decoded };
  }
  pathCollection(hash) {
    let p = this.pathSets.get(hash);
    if (!p) {
      p = this.part(hash).then(decodePathCollection);
      this.pathSets.set(hash, p);
    }
    return p;
  }
  /**
   * Load an object and everything it references. Each part is visited once;
   * at most eight dependencies are prepared at a time per call.
   * Calls onResource for each non-object dependency (font, image, path collection)
   * so a renderer can prepare it.
   */
  async ensure(hash, onResource, seen = /* @__PURE__ */ new Set()) {
    const o = await this.object(hash);
    this.loadedObjects.set(hash, o);
    if (seen.has(hash))
      return o;
    seen.add(hash);
    const queue = [];
    const enqueue = (obj) => {
      for (const dep of objectDeps(obj)) {
        if (seen.has(dep))
          continue;
        const e = this.entry(dep);
        if (e.t !== "obj" && !onResource)
          continue;
        seen.add(dep);
        queue.push(e);
      }
    };
    enqueue(o);
    for (let at = 0; at < queue.length; ) {
      const batch = queue.slice(at, at + 8);
      at += batch.length;
      const done = await Promise.allSettled(batch.map(async (e) => {
        if (e.t === "obj") {
          const child = await this.object(e.h);
          this.loadedObjects.set(e.h, child);
          enqueue(child);
        } else {
          await onResource(e, await this.part(e.h));
        }
      }));
      for (const result of done)
        if (result.status === "rejected")
          throw result.reason;
    }
    return o;
  }
};

// packages/core/dist/segments.js
var BdfSegmentError = class extends Error {
  status;
  constructor(status) {
    super(`bdf: the segment request was answered with ${status}`);
    this.status = status;
    this.name = "BdfSegmentError";
  }
};
var toBase64 = (b) => btoa(String.fromCharCode(...b));
var SegmentLoader = class _SegmentLoader {
  url;
  options;
  /** The segments that came, in the order they came. */
  held = [];
  /** Called as each segment comes, once its pages are in the document. */
  onSegment;
  /** The document: the first segment, which the others are put in (BdfDocument.addSegment). */
  doc;
  /** Requests on their way, with the pages they are expected to bring. */
  inflight = [];
  /** The pages of a segment, once a whole one has come (the last segment of a view is shorter). */
  size = 0;
  abort = new AbortController();
  constructor(url, options) {
    this.url = url;
    this.options = options;
  }
  /** Fetch the first segment; its document is doc. */
  static async open(url, options = {}) {
    const l = new _SegmentLoader(url, options);
    l.doc = await l.fetch(options.view, options.page ?? 0);
    l.took(l.doc.manifest.segment);
    return l;
  }
  /** Whether page of view has come. */
  has(view, page) {
    return this.held.some((s) => s.view === view && s.from <= page && page < s.to);
  }
  /**
   * Fetch the segment that holds page of view unless it came, and start
   * fetching the one a few pages on.
   */
  async ensure(view, page) {
    await this.load(view, page);
    const ahead = this.options.ahead ?? 3;
    if (ahead > 0)
      this.prefetch(view, page + ahead);
  }
  /** Start fetching the segment that holds page of view, unless it came; a failure is left for ensure to meet. */
  prefetch(view, page) {
    if (page >= (this.doc.view(view).pages?.length ?? 0) || this.has(view, page))
      return;
    this.load(view, page).catch(() => {
    });
  }
  /** Stop the requests on their way. */
  close() {
    this.abort.abort();
  }
  async load(view, page) {
    const n = this.doc.view(view).pages?.length ?? 0;
    if (!Number.isInteger(page) || page < 0 || page >= n)
      throw new Error(`bdf: no page ${page} in view ${view}`);
    for (; ; ) {
      if (this.has(view, page))
        return;
      const on = this.inflight.find((f2) => f2.view === view && f2.from <= page && page < f2.to);
      if (on) {
        await on.done;
        continue;
      }
      const from = this.size ? page - page % this.size : page;
      const to = this.size ? Math.min(from + this.size, n) : page + 1;
      const f = { view, from, to, done: this.fetch(view, page).then((d) => this.merge(d)) };
      this.inflight.push(f);
      try {
        await f.done;
      } finally {
        this.inflight.splice(this.inflight.indexOf(f), 1);
      }
      if (!this.has(view, page))
        throw new BdfFormatError(`the segment of page ${page} does not hold it`);
    }
  }
  async fetch(view, page) {
    const keyPair = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, false, ["deriveKey"]);
    const key = new Uint8Array(await crypto.subtle.exportKey("raw", keyPair.publicKey));
    const init = this.options.init ?? {};
    const res = await fetch(this.url, {
      ...init,
      method: "POST",
      headers: { ...init.headers, "Content-Type": "application/json" },
      body: JSON.stringify({ key: toBase64(key), view, page, have: this.held }),
      cache: "no-store",
      signal: this.abort.signal
    });
    if (!res.ok)
      throw new BdfSegmentError(res.status);
    const doc = await BdfDocument.open(new BufferSource(new Uint8Array(await res.arrayBuffer())), { keyPair });
    const s = doc.manifest.segment;
    if (!s || view !== void 0 && s.view !== view || !(s.from <= page && page < s.to))
      throw new BdfFormatError("the server sent another segment");
    return doc;
  }
  merge(d) {
    this.doc.addSegment(d);
    this.took(d.manifest.segment);
  }
  took(s) {
    if (!this.has(s.view, s.from) || !this.has(s.view, s.to - 1))
      this.held.push({ view: s.view, from: s.from, to: s.to });
    if (s.to < (this.doc.view(s.view).pages?.length ?? 0))
      this.size = Math.max(this.size, s.to - s.from);
    this.onSegment?.(s);
  }
};

// packages/core/dist/smf.js
var MAX_MIDI_EVENTS = 1 << 20;

// packages/render/src/svg.ts
function isSvg(b) {
  if (b[0] === 255 && b[1] === 254 || b[0] === 254 && b[1] === 255) return true;
  let i = b[0] === 239 && b[1] === 187 && b[2] === 191 ? 3 : 0;
  while (b[i] === 32 || b[i] === 9 || b[i] === 10 || b[i] === 13) i++;
  return b[i] === 60;
}
var UNITS = { "": 1, px: 1, pt: 96 / 72, pc: 16, in: 96, cm: 96 / 2.54, mm: 96 / 25.4, q: 96 / 101.6, em: 16, ex: 8 };
function svgLength(s) {
  const m = /^\s*([+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)\s*([A-Za-z%]*)\s*$/.exec(s ?? "");
  if (!m) return void 0;
  const v = Number(m[1]) * (UNITS[m[2].toLowerCase()] ?? NaN);
  return v > 0 ? v : void 0;
}
function svgSize(data) {
  const label = data[0] === 255 && data[1] === 254 ? "utf-16le" : data[0] === 254 && data[1] === 255 ? "utf-16be" : "utf-8";
  const head = new TextDecoder(label).decode(data.subarray(0, 65536));
  const tag = /<(?:[\w.-]+:)?svg\b[^<>]*>/.exec(head)?.[0] ?? "";
  const attr = (name) => {
    const m = new RegExp(`\\s${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)')`).exec(tag);
    return m ? m[1] ?? m[2] : void 0;
  };
  let w = svgLength(attr("width")), h = svgLength(attr("height"));
  const vb = (attr("viewBox") ?? "").split(/[\s,]+/).filter(Boolean).map(Number);
  const [vw, vh] = vb.length === 4 && vb[2] > 0 && vb[3] > 0 ? [vb[2], vb[3]] : [0, 0];
  if (w === void 0 || h === void 0) {
    if (w !== void 0 && vw) h = w * vh / vw;
    else if (h !== void 0 && vw) w = h * vw / vh;
    else if (vw) [w, h] = [vw, vh];
    w ??= 300;
    h ??= 150;
  }
  return { width: w, height: h };
}
var MAX_PIXELS = 4096 * 4096;
var SLACK = 1.25;
var KEEP = 4;
var VectorImage = class {
  constructor(hash, data) {
    this.hash = hash;
    this.data = data.byteLength === data.buffer.byteLength ? data : data.slice();
    ({ width: this.width, height: this.height } = svgSize(data));
  }
  width;
  height;
  rasters = [];
  pending = /* @__PURE__ */ new Map();
  failed = /* @__PURE__ */ new Set();
  disposed = false;
  data;
  /** The scale of the raster for drawing at scale (device pixels per image pixel): bounded in size. */
  fit(scale) {
    const max = Math.sqrt(MAX_PIXELS / (this.width * this.height));
    return Math.min(Math.max(scale, 1 / Math.min(this.width, this.height)), max);
  }
  /**
   * A raster for drawing at scale, and the scale of the raster that is
   * missing when none fits (the closest one is returned meanwhile).
   */
  raster(scale) {
    const k = this.fit(scale);
    const i = this.rasters.findIndex((r) => r.k >= k && r.k <= k * SLACK);
    if (i >= 0) {
      const [r] = this.rasters.splice(i, 1);
      this.rasters.push(r);
      return { raster: r };
    }
    let best;
    for (const r of this.rasters) if (!best || Math.abs(Math.log(r.k / k)) < Math.abs(Math.log(best.k / k))) best = r;
    return { raster: best, missing: this.failed.has(k) ? void 0 : k };
  }
  /** The raster used last. */
  latest() {
    return this.rasters.at(-1)?.bitmap;
  }
  /** Close the rasters; those still being drawn are closed when they come. */
  dispose() {
    this.disposed = true;
    for (const r of this.rasters.splice(0)) r.bitmap.close();
  }
  /** Draw the raster of scale k (once, however many draws missed it); false when it cannot be drawn. */
  draw(k, rasterize) {
    if (this.rasters.some((r) => r.k === k)) return Promise.resolve(true);
    let p = this.pending.get(k);
    if (p) return p;
    p = (async () => {
      const w = Math.max(1, Math.round(this.width * k)), h = Math.max(1, Math.round(this.height * k));
      try {
        if (!rasterize) throw new Error("no SVG rasterizer (workers need the page to draw SVG images)");
        const bitmap = await rasterize(this.hash, this.data, w, h);
        if (this.disposed) {
          bitmap.close();
          return false;
        }
        this.rasters.push({ bitmap, k });
        while (this.rasters.length > KEEP) this.rasters.shift().bitmap.close();
        return true;
      } catch (e) {
        this.failed.add(k);
        console.warn(`bdf: SVG image ${this.hash}: ${e.message ?? e}`);
        return false;
      } finally {
        this.pending.delete(k);
      }
    })();
    this.pending.set(k, p);
    return p;
  }
};
function domSvgRasterizer() {
  if (typeof document === "undefined" || typeof Image === "undefined") return void 0;
  const decoded = /* @__PURE__ */ new Map();
  const load = (hash, data) => {
    let p = decoded.get(hash);
    if (p) {
      decoded.delete(hash);
    } else {
      p = (async () => {
        const url = URL.createObjectURL(new Blob([data], { type: "image/svg+xml" }));
        const img = new Image();
        img.src = url;
        try {
          await img.decode();
        } finally {
          URL.revokeObjectURL(url);
        }
        return img;
      })();
      p.catch(() => decoded.delete(hash));
    }
    decoded.set(hash, p);
    if (decoded.size > 32) decoded.delete(decoded.keys().next().value);
    return p;
  };
  return async (hash, data, width, height) => {
    const img = await load(hash, data);
    if (typeof OffscreenCanvas !== "undefined") {
      const canvas2 = new OffscreenCanvas(width, height);
      canvas2.getContext("2d").drawImage(img, 0, 0, width, height);
      return canvas2.transferToImageBitmap();
    }
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    canvas.getContext("2d").drawImage(img, 0, 0, width, height);
    return createImageBitmap(canvas);
  };
}

// packages/render/src/resources.ts
function buildPath2D(p) {
  const path = new Path2D();
  const a = p.args;
  let i = 0;
  for (const v of p.verbs) {
    switch (v) {
      case Verb.MOVE:
        path.moveTo(a[i], a[i + 1]);
        i += 2;
        break;
      case Verb.LINE:
        path.lineTo(a[i], a[i + 1]);
        i += 2;
        break;
      case Verb.QUAD:
        path.quadraticCurveTo(a[i], a[i + 1], a[i + 2], a[i + 3]);
        i += 4;
        break;
      case Verb.CUBIC:
        path.bezierCurveTo(a[i], a[i + 1], a[i + 2], a[i + 3], a[i + 4], a[i + 5]);
        i += 6;
        break;
      case Verb.CLOSE:
        path.closePath();
        break;
      case Verb.RECT:
        path.rect(a[i], a[i + 1], a[i + 2], a[i + 3]);
        i += 4;
        break;
      case Verb.ELLIPSE:
        path.ellipse(a[i], a[i + 1], a[i + 2], a[i + 3], a[i + 4], a[i + 5], a[i + 6], a[i + 7] !== 0);
        i += 8;
        break;
      case Verb.ARC_TO:
        path.arcTo(a[i], a[i + 1], a[i + 2], a[i + 3], a[i + 4]);
        i += 5;
        break;
      case Verb.ROUND_RECT:
        path.roundRect(a[i], a[i + 1], a[i + 2], a[i + 3], a[i + 4]);
        i += 5;
        break;
    }
  }
  return path;
}
function embeddedFamily(hash) {
  return `bdf-${hash}`;
}
function fontString(f, size) {
  let family;
  if (f.kind === FontKind.EMBEDDED && f.hash) {
    family = `"${embeddedFamily(f.hash)}"`;
    if (f.family) family += `, ${f.family}`;
  } else {
    family = f.family || "sans-serif";
  }
  return `${FONT_STYLES[f.style] ?? "normal"} ${f.weight || 400} ${size}px ${family}`;
}
var DEFAULT_IMAGE_BUDGET = 256 * 1024 * 1024;
var DEFAULT_MAX_IMAGE_PIXELS = 1 << 28;
var DEFAULT_HOLD_LIMIT = 4 * 1024 * 1024 * 1024;
function imageSize(b) {
  const dv = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const tag = (at, s) => at + s.length <= b.length && [...s].every((c, i) => b[at + i] === c.charCodeAt(0));
  if (tag(0, "\x89PNG\r\n\n")) {
    return b.length >= 24 && tag(12, "IHDR") ? { width: dv.getUint32(16), height: dv.getUint32(20) } : void 0;
  }
  if (tag(0, "GIF87a") || tag(0, "GIF89a")) {
    return b.length >= 10 ? { width: dv.getUint16(6, true), height: dv.getUint16(8, true) } : void 0;
  }
  if (tag(0, "BM")) {
    if (b.length < 26) return void 0;
    if (dv.getUint32(14, true) === 12) return { width: dv.getUint16(18, true), height: dv.getUint16(20, true) };
    return { width: Math.abs(dv.getInt32(18, true)), height: Math.abs(dv.getInt32(22, true)) };
  }
  if (tag(0, "RIFF") && tag(8, "WEBP")) {
    if (b.length < 30) return void 0;
    if (tag(12, "VP8X")) return { width: 1 + (b[24] | b[25] << 8 | b[26] << 16), height: 1 + (b[27] | b[28] << 8 | b[29] << 16) };
    if (tag(12, "VP8 ")) return { width: dv.getUint16(26, true) & 16383, height: dv.getUint16(28, true) & 16383 };
    if (tag(12, "VP8L")) {
      const v = dv.getUint32(21, true);
      return { width: 1 + (v & 16383), height: 1 + (v >>> 14 & 16383) };
    }
    return void 0;
  }
  if (b[0] === 255 && b[1] === 216) {
    for (let at = 2; at + 4 <= b.length; ) {
      if (b[at] !== 255) return void 0;
      const m = b[at + 1];
      if (m === 255) {
        at++;
        continue;
      }
      if (m === 1 || m >= 208 && m <= 216) {
        at += 2;
        continue;
      }
      if (m === 217 || m === 218) return void 0;
      if (m >= 192 && m <= 207 && m !== 196 && m !== 200 && m !== 204) {
        return at + 9 <= b.length ? { width: dv.getUint16(at + 7), height: dv.getUint16(at + 5) } : void 0;
      }
      at += 2 + dv.getUint16(at + 2);
    }
  }
  return void 0;
}
var ImageHold = class {
  images = /* @__PURE__ */ new Set();
  /** What the images take decoded, as far as it is known. */
  bytes = 0;
};
var bitmapBytes = (b) => b.width * b.height * 4;
var ResourceCache = class {
  constructor(doc, fontSet, opts = {}) {
    this.doc = doc;
    this.fontSet = fontSet ?? globalThis.fonts ?? globalThis.document?.fonts;
    this.imageBudget = opts.imageBudget ?? DEFAULT_IMAGE_BUDGET;
    this.maxImagePixels = opts.maxImagePixels ?? DEFAULT_MAX_IMAGE_PIXELS;
    this.holdLimit = opts.holdLimit ?? DEFAULT_HOLD_LIMIT;
    this.decodeImage = opts.decodeImage ?? ((bytes) => createImageBitmap(new Blob([bytes])));
    this.rasterize = opts.rasterizeSvg ?? domSvgRasterizer();
  }
  inlinePaths = /* @__PURE__ */ new WeakMap();
  extPaths = /* @__PURE__ */ new Map();
  /** Decoded images, least recently prepared first. */
  images = /* @__PURE__ */ new Map();
  decoding = /* @__PURE__ */ new Map();
  holds = /* @__PURE__ */ new Set();
  bytes = 0;
  disposed = false;
  /** Images that are not decoded: too large. */
  refused = /* @__PURE__ */ new Set();
  vectors = /* @__PURE__ */ new Map();
  fonts = /* @__PURE__ */ new Map();
  fontSet;
  imageBudget;
  maxImagePixels;
  holdLimit;
  decodeImage;
  rasterize;
  /** SVG rasters the draws since takeMisses() did not find, by image and scale. */
  misses = /* @__PURE__ */ new Map();
  /**
   * Load an object, its children, and every font/image/path part they use.
   * The images go into hold, when given, and stay decoded until it is released.
   */
  async prepare(hash, hold) {
    return this.doc.ensure(hash, (e, bytes) => this.load(e, bytes, hold));
  }
  /**
   * Load an object with its children, fonts and path collections but not
   * its images: what extracting its text needs, without decoding pictures
   * that no render asked for.
   */
  async prepareText(hash) {
    return this.doc.ensure(hash, (e, bytes) => e.t === "img" ? void 0 : this.load(e, bytes));
  }
  /** Start holding the images of a render (see prepare). */
  hold() {
    const h = new ImageHold();
    this.holds.add(h);
    return h;
  }
  /** End a hold, and close the images over the budget that nothing holds. */
  release(hold) {
    this.holds.delete(hold);
    this.trim();
  }
  /** The decoded images and their size in bytes (width × height × 4). */
  get imageStats() {
    return { count: this.images.size, bytes: this.bytes };
  }
  /**
   * Close the decoded images and take the fonts out of the font set; the
   * cache is not used for new renders afterwards. Images a render in
   * progress holds are closed when it releases them, and those still
   * decoding when they are done; the fonts go at once, so dispose of a
   * cache whose renders may still draw text only once they are done.
   */
  dispose() {
    this.disposed = true;
    this.trim();
    for (const v of this.vectors.values()) v.dispose();
    for (const face of this.fonts.values()) this.fontSet?.delete(face);
    this.fonts.clear();
    this.extPaths.clear();
  }
  async load(e, bytes, hold) {
    switch (e.t) {
      case "img": {
        if (isSvg(bytes)) {
          if (!this.vectors.has(e.h) && !this.disposed) this.vectors.set(e.h, new VectorImage(e.h, bytes));
          return;
        }
        if (this.refused.has(e.h)) return;
        const bmp = this.images.get(e.h);
        const stated = bmp ?? imageSize(bytes);
        if (stated && stated.width * stated.height > this.maxImagePixels) return this.refuse(e.h, stated);
        const held = hold?.images.has(e.h);
        hold?.images.add(e.h);
        if (hold && !held && stated) this.pin(hold, stated);
        if (bmp) {
          this.images.delete(e.h);
          this.images.set(e.h, bmp);
          return;
        }
        let p = this.decoding.get(e.h);
        if (!p) {
          p = this.decodeImage(bytes).then((bmp2) => {
            if (this.disposed) {
              bmp2.close();
              return;
            }
            if (bmp2.width * bmp2.height > this.maxImagePixels) {
              bmp2.close();
              return this.refuse(e.h, bmp2);
            }
            this.images.set(e.h, bmp2);
            this.bytes += bitmapBytes(bmp2);
            this.trim();
          }).finally(() => this.decoding.delete(e.h));
          this.decoding.set(e.h, p);
        }
        if (!hold || held || stated) return p;
        return p.then(() => {
          const decoded = this.images.get(e.h);
          if (decoded) this.pin(hold, decoded);
        });
      }
      case "font": {
        if (this.fonts.has(e.h)) return;
        const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
        const face = new FontFace(embeddedFamily(e.h), buffer);
        await face.load();
        if (this.disposed) return;
        this.fontSet?.add(face);
        this.fonts.set(e.h, face);
        return;
      }
      case "path": {
        if (this.extPaths.has(e.h)) return;
        const paths = await this.doc.pathCollection(e.h);
        this.extPaths.set(e.h, paths.map(buildPath2D));
        return;
      }
    }
  }
  /** An image that is not decoded: draws find nothing to draw. */
  refuse(hash, size) {
    this.refused.add(hash);
    console.warn(`bdf: image ${hash} of ${size.width} \xD7 ${size.height} pixels is not drawn`);
  }
  /** Count an image of a hold; a hold of more than the limit fails the render it is for. */
  pin(hold, size) {
    hold.bytes += size.width * size.height * 4;
    if (hold.bytes > this.holdLimit) throw new BdfFormatError(`the images of a render take more than ${this.holdLimit} bytes`);
  }
  /** Close the least recently prepared images that nothing holds until the rest fit the budget. */
  trim() {
    const budget = this.disposed ? 0 : this.imageBudget;
    if (this.bytes <= budget) return;
    const held = /* @__PURE__ */ new Set();
    for (const h of this.holds) for (const i of h.images) held.add(i);
    for (const [hash, bmp] of this.images) {
      if (this.bytes <= budget) break;
      if (held.has(hash)) continue;
      this.images.delete(hash);
      this.bytes -= bitmapBytes(bmp);
      bmp.close();
    }
  }
  path(o, index) {
    const entry = o.paths[index];
    if (!entry) throw new Error(`bdf: bad path ref ${index}`);
    if ("inline" in entry) {
      let list = this.inlinePaths.get(o);
      if (!list) {
        list = new Array(o.paths.length);
        this.inlinePaths.set(o, list);
      }
      let p2 = list[index];
      if (!p2) {
        p2 = buildPath2D(entry.inline);
        list[index] = p2;
      }
      return p2;
    }
    const set = this.extPaths.get(entry.hash);
    const p = set?.[entry.index];
    if (!p) throw new Error(`bdf: path collection ${entry.hash} not loaded`);
    return p;
  }
  /**
   * Whether text in a CSS font is drawn in the font's own faces: none of
   * them in the font set is still to load (the embedded fonts are loaded
   * by prepare; a page's own web fonts may load later).
   */
  fontLoaded(font) {
    try {
      return this.fontSet?.check(font) ?? true;
    } catch {
      return false;
    }
  }
  /**
   * A bitmap image; for an SVG image, the raster drawn last. Undefined for
   * an image that was not decoded for its size: it is drawn as nothing.
   */
  image(hash) {
    const img = this.images.get(hash) ?? this.vectors.get(hash)?.latest();
    if (!img && !this.refused.has(hash)) throw new Error(`bdf: image ${hash} not loaded`);
    return img;
  }
  /** An SVG image, or undefined for a bitmap. */
  vector(hash) {
    return this.vectors.get(hash);
  }
  /**
   * A raster of an SVG image for drawing at scale (device pixels per image
   * pixel). When none fits, the closest one (or none) is returned and the
   * miss is noted for settle().
   */
  svgRaster(vec, scale) {
    const { raster, missing } = vec.raster(scale);
    if (missing !== void 0) this.misses.set(`${vec.hash} ${missing}`, { vec, k: missing });
    return raster;
  }
  /**
   * The SVG rasters the draws since the last call did not find. Draws are
   * synchronous, so taking them before and after a draw gives its own.
   */
  takeMisses() {
    const m = [...this.misses.values()];
    this.misses.clear();
    return m;
  }
  /** Draw the rasters a draw missed; true when any was drawn, and the draw should be run again. */
  async settle(misses) {
    const drawn = await Promise.all(misses.map(({ vec, k }) => vec.draw(k, this.rasterize)));
    return drawn.includes(true);
  }
  object(hash) {
    return this.doc.objectSync(hash);
  }
};

// packages/render/src/canvas.ts
var colorCache = /* @__PURE__ */ new Map();
function cssColor(rgba) {
  let s = colorCache.get(rgba);
  if (s === void 0) {
    const a = rgba & 255;
    const r = rgba >>> 24 & 255, g = rgba >>> 16 & 255, b = rgba >>> 8 & 255;
    s = a === 255 ? `#${(rgba >>> 8).toString(16).padStart(6, "0")}` : `rgba(${r},${g},${b},${(a / 255).toFixed(4)})`;
    colorCache.set(rgba, s);
  }
  return s;
}
function fontSize(font) {
  const m = /(\d*\.?\d+(?:e[+-]?\d+)?)px/i.exec(font);
  return m ? Number(m[1]) : 10;
}
var MAX_WIDTHS = 2e5;
var INK_REACH = 4;
var MAX_GROUP_DEPTH = 64;
var MAX_INK_REACH = 4096;
function inkReach(ctx) {
  let reach = Math.max(Math.abs(ctx.shadowOffsetX), Math.abs(ctx.shadowOffsetY)) + 1.5 * ctx.shadowBlur;
  const filter = "filter" in ctx ? ctx.filter : "";
  if (filter && filter !== "none") {
    for (const m of filter.matchAll(/-?\d*\.?\d+/g)) reach += 3 * Math.abs(Number(m[0]));
  }
  return Number.isFinite(reach) ? Math.min(Math.ceil(reach), MAX_INK_REACH) : MAX_INK_REACH;
}
function release(canvas) {
  canvas.width = canvas.height = 0;
}
var FILTER_FUNCTIONS = /* @__PURE__ */ new Set([
  "blur",
  "brightness",
  "contrast",
  "drop-shadow",
  "grayscale",
  "hue-rotate",
  "invert",
  "opacity",
  "saturate",
  "sepia",
  "rgb",
  "rgba",
  "hsl",
  "hsla",
  "hwb",
  "lab",
  "lch",
  "oklab",
  "oklch",
  "color"
]);
function filterAllowed(css) {
  if (!/^[a-zA-Z0-9\s.,%#()+\/-]*$/.test(css)) return false;
  for (const m of css.matchAll(/([a-zA-Z-]*)\(/g)) if (!FILTER_FUNCTIONS.has(m[1].toLowerCase())) return false;
  return true;
}
function defaultCreateCanvas(w, h) {
  if (typeof OffscreenCanvas !== "undefined") return new OffscreenCanvas(w, h);
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  return c;
}
function resetState(ctx) {
  ctx.fillStyle = "#000000";
  ctx.strokeStyle = "#000000";
  ctx.lineWidth = 1;
  ctx.lineCap = "butt";
  ctx.lineJoin = "miter";
  ctx.miterLimit = 10;
  ctx.setLineDash([]);
  ctx.lineDashOffset = 0;
  ctx.globalAlpha = 1;
  ctx.globalCompositeOperation = "source-over";
  ctx.shadowColor = "rgba(0,0,0,0)";
  ctx.shadowBlur = 0;
  ctx.shadowOffsetX = 0;
  ctx.shadowOffsetY = 0;
  ctx.font = "10px sans-serif";
  ctx.textAlign = "left";
  ctx.textBaseline = "alphabetic";
  ctx.direction = "inherit";
  if ("letterSpacing" in ctx) ctx.letterSpacing = "0px";
  if ("filter" in ctx) ctx.filter = "none";
  ctx.imageSmoothingEnabled = true;
}
var CanvasRenderer = class {
  constructor(res, opts = {}) {
    this.res = res;
    this.tol = opts.advanceTolerance ?? 5e-3;
    this.createCanvas = opts.createCanvas ?? defaultCreateCanvas;
  }
  ctx;
  obj;
  groups = [];
  masks = [];
  tol;
  createCanvas;
  /** What text can be skipped now, and as of each SAVE not restored yet. */
  cull = { box: void 0, size: 10 };
  culls = [];
  /**
   * Widths that measureText gave, by font (with the letter spacing and
   * direction) and text: the advance correction measures every run each
   * time it is drawn, and measuring takes longer than drawing.
   */
  widths = /* @__PURE__ */ new Map();
  widthCount = 0;
  /** The limits of the objects drawn with USE, how deep the object drawn now is, and whether it is drawn again. */
  limits = new UseLimits();
  depth = 0;
  reused = false;
  /**
   * Draw an object with the context's current transform and clip. Top-level
   * objects (pages, tiles) start from the initial drawing state; USE'd children
   * inherit the state of their parent. visible is the part of the object
   * that can be seen, in its coordinates: text wholly outside it is skipped
   * (a tile of a sheet has thousands of runs, and a region shows a few).
   * limits counts the objects drawn with USE across the objects of a render
   * (default: of this object alone); past them BdfFormatError is thrown.
   */
  draw(ctx, obj, reset = true, visible, limits = new UseLimits()) {
    const prevCtx = this.ctx, prevObj = this.obj, prevCull = this.cull, prevCulls = this.culls;
    const prevLimits = this.limits, prevDepth = this.depth, prevReused = this.reused;
    this.ctx = ctx;
    this.obj = obj;
    this.limits = limits;
    this.depth = 0;
    this.reused = false;
    ctx.save();
    if (reset) resetState(ctx);
    this.cull = {
      box: visible && { x0: visible.x, y0: visible.y, x1: visible.x + visible.w, y1: visible.y + visible.h },
      size: fontSize(ctx.font)
    };
    this.culls = [];
    try {
      walk(obj, this);
    } finally {
      for (const m of this.masks.splice(0)) release(m.canvas);
      while (this.groups.length) this.groupEnd();
      ctx.restore();
      this.ctx = prevCtx;
      this.obj = prevObj;
      this.cull = prevCull;
      this.culls = prevCulls;
      this.limits = prevLimits;
      this.depth = prevDepth;
      this.reused = prevReused;
    }
  }
  /** Map the visible box into the coordinates a transform sets up (their bounding box when it rotates or skews). */
  transformCull(a, b, c, d, e, f) {
    const v = this.cull.box;
    if (!v) return;
    const det = a * d - b * c;
    if (!det || !Number.isFinite(det)) {
      this.cull = { ...this.cull, box: void 0 };
      return;
    }
    const ia = d / det, ib = -b / det, ic = -c / det, id = a / det;
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const [px, py] of [[v.x0, v.y0], [v.x1, v.y0], [v.x0, v.y1], [v.x1, v.y1]]) {
      const x = ia * (px - e) + ic * (py - f), y = ib * (px - e) + id * (py - f);
      x0 = Math.min(x0, x);
      y0 = Math.min(y0, y);
      x1 = Math.max(x1, x);
      y1 = Math.max(y1, y);
    }
    this.cull = { ...this.cull, box: { x0, y0, x1, y1 } };
  }
  /** Whether a run with its anchor at x, y and that advance cannot reach the visible box. */
  unseen(x, y, advance) {
    const v = this.cull.box;
    if (!v) return false;
    const m = this.cull.size * INK_REACH;
    if (y - m > v.y1 || y + m < v.y0) return true;
    return advance > 0 && (x - advance - m > v.x1 || x + advance + m < v.x0);
  }
  /** measureText's width of a text in the current font, remembered while the font is loaded. */
  width(text) {
    const ctx = this.ctx;
    let key = ctx.font;
    const ls = ctx.letterSpacing;
    if (ls && ls !== "0px") key += ` ${ls}`;
    if (ctx.direction === "rtl") key += " rtl";
    let widths = this.widths.get(key);
    if (!widths) {
      if (!this.res.fontLoaded(ctx.font)) return ctx.measureText(text).width;
      this.widths.set(key, widths = /* @__PURE__ */ new Map());
    }
    let w = widths.get(text);
    if (w === void 0) {
      w = ctx.measureText(text).width;
      if (++this.widthCount > MAX_WIDTHS) {
        this.widths.clear();
        this.widths.set(key, widths = /* @__PURE__ */ new Map());
        this.widthCount = 1;
      }
      widths.set(text, w);
    }
    return w;
  }
  paint(index) {
    const p = this.obj.paints[index];
    if (!p) throw new Error(`bdf: bad paint ref ${index}`);
    const ctx = this.ctx;
    const c = p.coords;
    let g;
    switch (p.kind) {
      case PaintKind.LINEAR:
        g = ctx.createLinearGradient(c[0], c[1], c[2], c[3]);
        break;
      case PaintKind.RADIAL:
        g = ctx.createRadialGradient(c[0], c[1], c[2], c[3], c[4], c[5]);
        break;
      case PaintKind.CONIC:
        g = ctx.createConicGradient(c[0], c[1], c[2]);
        break;
      case PaintKind.PATTERN: {
        const hash = this.obj.images[p.image];
        const m = p.matrix;
        let img, kx = 1, ky = 1;
        const vec = this.res.vector(hash);
        if (vec) {
          const t = ctx.getTransform();
          const a = t.a * m[0] + t.c * m[1], b = t.b * m[0] + t.d * m[1], c2 = t.a * m[2] + t.c * m[3], d = t.b * m[2] + t.d * m[3];
          img = this.res.svgRaster(vec, Math.max(Math.hypot(a, b), Math.hypot(c2, d)))?.bitmap;
          if (img) [kx, ky] = [img.width / vec.width, img.height / vec.height];
        } else {
          img = this.res.image(hash);
        }
        if (!img) return "rgba(0,0,0,0)";
        const pat = ctx.createPattern(img, REPEATS[p.repeat] ?? "repeat");
        if (!pat) throw new Error("bdf: createPattern failed");
        if (!(m[0] === 1 && m[1] === 0 && m[2] === 0 && m[3] === 1 && m[4] === 0 && m[5] === 0 && kx === 1 && ky === 1)) {
          pat.setTransform(new DOMMatrix([m[0], m[1], m[2], m[3], m[4], m[5]]).scale(1 / kx, 1 / ky));
        }
        return pat;
      }
      default:
        throw new Error(`bdf: unknown paint kind ${p.kind}`);
    }
    for (const s of p.stops) g.addColorStop(Math.min(1, Math.max(0, s.offset)), cssColor(s.color));
    return g;
  }
  // --- state ---
  save() {
    this.culls.push(this.cull);
    this.ctx.save();
  }
  restore() {
    this.cull = this.culls.pop() ?? { ...this.cull, box: void 0 };
    this.ctx.restore();
  }
  transform(a, b, c, d, e, f) {
    this.ctx.transform(a, b, c, d, e, f);
    this.transformCull(a, b, c, d, e, f);
  }
  translate(x, y) {
    this.ctx.translate(x, y);
    this.transformCull(1, 0, 0, 1, x, y);
  }
  scale(x, y) {
    this.ctx.scale(x, y);
    this.transformCull(x, 0, 0, y, 0, 0);
  }
  clipPath(path, rule) {
    this.ctx.clip(this.res.path(this.obj, path), FILL_RULES[rule]);
  }
  clipRect(x, y, w, h) {
    this.ctx.beginPath();
    this.ctx.rect(x, y, w, h);
    this.ctx.clip();
  }
  // --- style ---
  fillColor(rgba) {
    this.ctx.fillStyle = cssColor(rgba);
  }
  fillPaint(paint) {
    this.ctx.fillStyle = this.paint(paint);
  }
  strokeColor(rgba) {
    this.ctx.strokeStyle = cssColor(rgba);
  }
  strokePaint(paint) {
    this.ctx.strokeStyle = this.paint(paint);
  }
  line(width, cap, join, miter) {
    const ctx = this.ctx;
    ctx.lineWidth = width;
    ctx.lineCap = LINE_CAPS[cap] ?? "butt";
    ctx.lineJoin = LINE_JOINS[join] ?? "miter";
    ctx.miterLimit = miter;
  }
  dash(segments, offset) {
    this.ctx.setLineDash(Array.from(segments));
    this.ctx.lineDashOffset = offset;
  }
  alpha(a) {
    this.ctx.globalAlpha = a;
  }
  blend(mode) {
    this.ctx.globalCompositeOperation = BLEND_NAMES[mode] ?? "source-over";
  }
  shadow(rgba, blur, dx, dy) {
    const ctx = this.ctx;
    const m = ctx.getTransform();
    const s = Math.sqrt(Math.abs(m.a * m.d - m.b * m.c)) || 1;
    ctx.shadowColor = cssColor(rgba);
    ctx.shadowBlur = blur * s;
    ctx.shadowOffsetX = dx * s;
    ctx.shadowOffsetY = dy * s;
    if ((rgba & 255) !== 0 && (blur !== 0 || dx !== 0 || dy !== 0)) this.cull = { ...this.cull, box: void 0 };
  }
  filter(css) {
    if (!filterAllowed(css)) css = "none";
    if ("filter" in this.ctx) this.ctx.filter = css;
    if (css && css !== "none") this.cull = { ...this.cull, box: void 0 };
  }
  font(font, size) {
    const f = this.obj.fonts[font];
    if (!f) throw new Error(`bdf: bad font ref ${font}`);
    this.ctx.font = fontString(f, size);
    this.cull = { ...this.cull, size: Math.abs(size) };
  }
  textStyle(align, baseline, dir, letterSpacing) {
    const ctx = this.ctx;
    ctx.textAlign = TEXT_ALIGNS[align] ?? "left";
    ctx.textBaseline = TEXT_BASELINES[baseline] ?? "alphabetic";
    ctx.direction = TEXT_DIRECTIONS[dir] ?? "inherit";
    if ("letterSpacing" in ctx) ctx.letterSpacing = `${letterSpacing}px`;
  }
  // --- shapes ---
  fillRect(x, y, w, h) {
    this.ctx.fillRect(x, y, w, h);
  }
  strokeRect(x, y, w, h) {
    this.ctx.strokeRect(x, y, w, h);
  }
  fillPath(path, rule) {
    this.ctx.fill(this.res.path(this.obj, path), FILL_RULES[rule]);
  }
  strokePath(path) {
    this.ctx.stroke(this.res.path(this.obj, path));
  }
  fillPathAt(path, rule, x, y) {
    const ctx = this.ctx;
    ctx.translate(x, y);
    ctx.fill(this.res.path(this.obj, path), FILL_RULES[rule]);
    ctx.translate(-x, -y);
  }
  fillPathRun(rule, glyphs) {
    const ctx = this.ctx;
    const fr = FILL_RULES[rule];
    for (const g of glyphs) {
      ctx.translate(g.x, g.y);
      ctx.fill(this.res.path(this.obj, g.path), fr);
      ctx.translate(-g.x, -g.y);
    }
  }
  clearRect(x, y, w, h) {
    this.ctx.clearRect(x, y, w, h);
  }
  // --- text ---
  text(text, x, y, advance, stroke) {
    const ctx = this.ctx;
    if (advance > 0) {
      const measured = this.width(text);
      if (measured > 0 && Math.abs(measured - advance) / advance > this.tol) {
        ctx.save();
        ctx.translate(x, y);
        ctx.scale(advance / measured, 1);
        if (stroke) ctx.strokeText(text, 0, 0);
        else ctx.fillText(text, 0, 0);
        ctx.restore();
        return;
      }
    }
    if (stroke) ctx.strokeText(text, x, y);
    else ctx.fillText(text, x, y);
  }
  fillText(text, x, y, advance) {
    if (!this.unseen(x, y, advance)) this.text(text, x, y, advance, false);
  }
  // always drawn: the ink of a stroke reaches as far as its line is wide
  strokeText(text, x, y, advance) {
    this.text(text, x, y, advance, true);
  }
  // --- images ---
  image(img, x, y, w, h) {
    const hash = this.obj.images[img];
    const vec = this.res.vector(hash);
    if (!vec) {
      const bmp = this.res.image(hash);
      if (bmp) this.ctx.drawImage(bmp, x, y, w, h);
      return;
    }
    const r = this.res.svgRaster(vec, this.deviceScale(w / vec.width, h / vec.height));
    if (r) this.ctx.drawImage(r.bitmap, x, y, w, h);
  }
  imageSub(img, sx, sy, sw, sh, dx, dy, dw, dh) {
    const hash = this.obj.images[img];
    const vec = this.res.vector(hash);
    if (!vec) {
      const bmp = this.res.image(hash);
      if (bmp) this.ctx.drawImage(bmp, sx, sy, sw, sh, dx, dy, dw, dh);
      return;
    }
    const r = this.res.svgRaster(vec, this.deviceScale(dw / sw, dh / sh));
    if (!r) return;
    const kx = r.bitmap.width / vec.width, ky = r.bitmap.height / vec.height;
    this.ctx.drawImage(r.bitmap, sx * kx, sy * ky, sw * kx, sh * ky, dx, dy, dw, dh);
  }
  /** Device pixels one image pixel covers, drawn sx by sy units per image pixel. */
  deviceScale(sx, sy) {
    const m = this.ctx.getTransform();
    return Math.max(Math.hypot(m.a, m.b) * Math.abs(sx), Math.hypot(m.c, m.d) * Math.abs(sy)) || 1;
  }
  smoothing(enabled, quality) {
    this.ctx.imageSmoothingEnabled = enabled;
    this.ctx.imageSmoothingQuality = SMOOTHING_QUALITIES[quality] ?? "low";
  }
  // --- composition ---
  use(obj) {
    this.useAt(obj, 0, 0);
  }
  useAt(obj, x, y) {
    const hash = this.obj.objects[obj];
    if (hash === void 0) throw new Error(`bdf: bad object ref ${obj}`);
    const child = this.res.object(hash);
    const again = this.limits.enter(hash);
    this.limits.descend(this.depth);
    const ctx = this.ctx;
    ctx.save();
    const cull = this.cull, depth = this.culls.length;
    if (x !== 0 || y !== 0) this.translate(x, y);
    const parent = this.obj, reused = this.reused;
    this.obj = child;
    this.depth++;
    this.reused = reused || again;
    try {
      walk(child, this, this.reused ? this.limits.count : void 0);
    } finally {
      this.obj = parent;
      this.depth--;
      this.reused = reused;
      ctx.restore();
      this.cull = this.culls.length === depth ? cull : { ...cull, box: void 0 };
      this.culls.length = Math.min(this.culls.length, depth);
    }
  }
  groupBegin(alpha, blend, x, y, w, h) {
    if (this.groups.length + this.masks.length >= MAX_GROUP_DEPTH) throw new BdfFormatError("groups nested too deep");
    const ctx = this.ctx;
    const m = ctx.getTransform();
    const pts = [m.transformPoint({ x, y }), m.transformPoint({ x: x + w, y }), m.transformPoint({ x, y: y + h }), m.transformPoint({ x: x + w, y: y + h })];
    let x0 = Math.floor(Math.min(...pts.map((p) => p.x))), y0 = Math.floor(Math.min(...pts.map((p) => p.y)));
    let x1 = Math.ceil(Math.max(...pts.map((p) => p.x))), y1 = Math.ceil(Math.max(...pts.map((p) => p.y)));
    const on = ctx.canvas;
    if (typeof on?.width === "number" && typeof on.height === "number") {
      const reach = inkReach(ctx);
      x0 = Math.max(x0, -reach);
      y0 = Math.max(y0, -reach);
      x1 = Math.min(x1, on.width + reach);
      y1 = Math.min(y1, on.height + reach);
    }
    const cw = Math.max(1, x1 - x0), ch = Math.max(1, y1 - y0);
    const canvas = this.createCanvas(cw, ch);
    const gctx = canvas.getContext("2d");
    gctx.setTransform(m.a, m.b, m.c, m.d, m.e - x0, m.f - y0);
    gctx.font = ctx.font;
    gctx.fillStyle = ctx.fillStyle;
    gctx.strokeStyle = ctx.strokeStyle;
    gctx.lineWidth = ctx.lineWidth;
    this.groups.push({ ctx, canvas, alpha, blend, dx: x0, dy: y0, cull: this.cull });
    this.ctx = gctx;
  }
  groupEnd() {
    const g = this.groups.pop();
    if (!g) return;
    const ctx = g.ctx;
    this.ctx = ctx;
    this.cull = g.cull;
    ctx.save();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.globalAlpha = g.alpha;
    ctx.globalCompositeOperation = BLEND_NAMES[g.blend] ?? "source-over";
    ctx.drawImage(g.canvas, g.dx, g.dy);
    ctx.restore();
    release(g.canvas);
  }
  maskBegin(kind, backdrop, transfer) {
    if (this.groups.length + this.masks.length >= MAX_GROUP_DEPTH) throw new BdfFormatError("groups nested too deep");
    const group = this.groups[this.groups.length - 1];
    const w = group?.canvas.width ?? 1, h = group?.canvas.height ?? 1;
    const canvas = this.createCanvas(w, h);
    const mctx = canvas.getContext("2d");
    if (kind === MaskKind.LUMINOSITY) {
      mctx.fillStyle = cssColor((backdrop | 255) >>> 0);
      mctx.fillRect(0, 0, w, h);
    }
    resetState(mctx);
    mctx.setTransform(this.ctx.getTransform());
    mctx.save();
    this.masks.push({ ctx: this.ctx, canvas, mctx, kind, transfer: transfer.length === 256 ? transfer : void 0, group, cull: this.cull });
    this.ctx = mctx;
    this.cull = { ...this.cull, size: 10 };
  }
  maskEnd() {
    const m = this.masks.pop();
    if (!m) return;
    this.ctx = m.ctx;
    this.cull = m.cull;
    const g = m.group;
    if (!g || this.groups[this.groups.length - 1] !== g) return release(m.canvas);
    const { mctx, canvas } = m;
    mctx.restore();
    const w = canvas.width, h = canvas.height;
    const lum = m.kind === MaskKind.LUMINOSITY, tr = m.transfer;
    if ((lum || tr) && w > 0 && h > 0) {
      const img = mctx.getImageData(0, 0, w, h);
      const d = img.data;
      for (let i = 0; i < d.length; i += 4) {
        let v = lum ? Math.round(0.3 * d[i] + 0.59 * d[i + 1] + 0.11 * d[i + 2]) : d[i + 3];
        if (tr) v = tr[v];
        d[i + 3] = v;
        d[i] = d[i + 1] = d[i + 2] = 0;
      }
      mctx.putImageData(img, 0, 0);
    }
    mctx.save();
    mctx.setTransform(1, 0, 0, 1, 0, 0);
    mctx.globalAlpha = 1;
    mctx.globalCompositeOperation = "source-in";
    mctx.drawImage(g.canvas, 0, 0);
    mctx.restore();
    release(g.canvas);
    g.canvas = canvas;
  }
  // --- meta ---
  link() {
  }
  mark() {
  }
  ext() {
  }
};

// packages/render/src/page.ts
var tileLists = /* @__PURE__ */ new WeakMap();
function tilesIn(view, ranges) {
  const tiles = view.tiles ?? {};
  let list = tileLists.get(tiles);
  if (!list) {
    list = [];
    for (const [key, h] of Object.entries(tiles)) {
      const m = /^(-?\d+),(-?\d+)$/.exec(key);
      if (m && `${Number(m[1])},${Number(m[2])}` === key && h) list.push([Number(m[1]), Number(m[2]), h]);
    }
    tileLists.set(tiles, list);
  }
  const count = ranges.reduce((n, r) => n + Math.max(0, r.tx1 - r.tx0 + 1) * Math.max(0, r.ty1 - r.ty0 + 1), 0);
  const found = /* @__PURE__ */ new Map();
  if (count <= list.length) {
    for (const r of ranges) {
      for (let ty = r.ty0; ty <= r.ty1; ty++) for (let tx = r.tx0; tx <= r.tx1; tx++) {
        const h = tiles[`${tx},${ty}`];
        if (h) found.set(`${tx},${ty}`, [tx, ty, h]);
      }
    }
  } else {
    for (const t of list) {
      if (ranges.some((r) => t[0] >= r.tx0 && t[0] <= r.tx1 && t[1] >= r.ty0 && t[1] <= r.ty1)) found.set(`${t[0]},${t[1]}`, t);
    }
  }
  return [...found.values()].sort((a, b) => a[1] - b[1] || a[0] - b[0]);
}
var PageRenderer = class {
  constructor(doc, opts = {}, fontSet, resources = {}) {
    this.doc = doc;
    this.res = new ResourceCache(doc, fontSet, resources);
    this.renderer = new CanvasRenderer(this.res, opts);
  }
  res;
  renderer;
  /** Let the fonts and images go: the document is not drawn any more. */
  dispose() {
    this.res.dispose();
  }
  /**
   * Load everything a page needs. To draw it afterwards with drawPageSync,
   * pass a hold (ResourceCache.hold) and release it after drawing, so that
   * its images stay decoded in between.
   */
  async preparePage(page, hold) {
    await Promise.all(page.layers.map((l) => this.res.prepare(l.obj, hold)));
  }
  /** Load what extracting a page's text needs: everything but its images. */
  async preparePageText(page) {
    await Promise.all(page.layers.map((l) => this.res.prepareText(l.obj)));
  }
  /**
   * Run draw, and again once the SVG rasters it asked for are drawn. The
   * region is cleared first when the background is transparent.
   */
  async drawSettled(ctx, opts, region, draw) {
    this.res.takeMisses();
    draw();
    const misses = this.res.takeMisses();
    if (!misses.length || !await this.res.settle(misses)) return;
    if (opts.background === null) {
      ctx.save();
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(region.x, region.y, region.w, region.h);
      ctx.restore();
    }
    draw();
    this.res.takeMisses();
  }
  /** Run a render: prepare with a hold on the images, draw, release. */
  async held(render) {
    const hold = this.res.hold();
    try {
      await render(hold);
    } finally {
      this.res.release(hold);
    }
  }
  /**
   * Draw one page into ctx. The canvas must be at least page.w*scale by page.h*scale;
   * the page origin is placed at (dx, dy) device pixels.
   */
  renderPage(ctx, page, opts, dx = 0, dy = 0) {
    return this.held(async (hold) => {
      await this.preparePage(page, hold);
      const region = { x: dx, y: dy, w: page.w * opts.scale, h: page.h * opts.scale };
      await this.drawSettled(ctx, opts, region, () => this.drawPageSync(ctx, page, opts, dx, dy));
    });
  }
  drawPageSync(ctx, page, opts, dx = 0, dy = 0) {
    const roles = opts.roles ? new Set(opts.roles) : void 0;
    const limits = new UseLimits();
    ctx.save();
    ctx.setTransform(opts.scale, 0, 0, opts.scale, dx, dy);
    ctx.beginPath();
    ctx.rect(0, 0, page.w, page.h);
    ctx.clip();
    if (opts.background !== null) {
      ctx.fillStyle = opts.background ?? "#ffffff";
      ctx.fillRect(0, 0, page.w, page.h);
    }
    for (const layer of page.layers) {
      if (roles && !roles.has(layer.role)) continue;
      this.renderer.draw(ctx, this.res.object(layer.obj), true, void 0, limits);
    }
    ctx.restore();
  }
  /**
   * Continuous layout of a flow view: body rectangles stacked vertically
   * (for a scroll view, its strips, without gaps).
   * Returns the y offset of each page's body and the total height, in units.
   */
  continuousLayout(view) {
    const gap = view.kind === "scroll" ? 0 : view.continuous?.gap ?? 0;
    const offsets = [];
    let y = 0;
    let width = 0;
    for (const p of view.pages ?? []) {
      const b = p.body ?? { x: 0, y: 0, w: p.w, h: p.h };
      offsets.push(y);
      y += b.h + gap;
      width = Math.max(width, b.w);
    }
    return { offsets, width, height: Math.max(0, y - gap) };
  }
  /**
   * Draw the part of the continuous layout that intersects viewport (units) into ctx,
   * with viewport's top-left at device (0,0).
   */
  renderContinuous(ctx, view, viewport, opts) {
    return this.held((hold) => this.continuousHeld(ctx, view, viewport, opts, hold));
  }
  async continuousHeld(ctx, view, viewport, opts, hold) {
    const pages = view.pages ?? [];
    const { offsets } = this.continuousLayout(view);
    const roles = opts.roles ?? ["body", "annotation"];
    const visible = [];
    pages.forEach((p, i) => {
      const b = p.body ?? { x: 0, y: 0, w: p.w, h: p.h };
      if (offsets[i] < viewport.y + viewport.h && offsets[i] + b.h > viewport.y) visible.push(i);
    });
    await Promise.all(visible.map((i) => this.preparePage(pages[i], hold)));
    const region = { x: 0, y: 0, w: viewport.w * opts.scale, h: viewport.h * opts.scale };
    await this.drawSettled(ctx, opts, region, () => this.drawContinuous(ctx, view, viewport, opts, visible, offsets, roles));
  }
  drawContinuous(ctx, view, viewport, opts, visible, offsets, roles) {
    const pages = view.pages ?? [];
    const limits = new UseLimits();
    for (const i of visible) {
      const p = pages[i];
      const b = p.body ?? { x: 0, y: 0, w: p.w, h: p.h };
      ctx.save();
      ctx.setTransform(opts.scale, 0, 0, opts.scale, -viewport.x * opts.scale, (offsets[i] - viewport.y) * opts.scale);
      ctx.beginPath();
      ctx.rect(0, 0, b.w, b.h);
      ctx.clip();
      if (opts.background !== null) {
        ctx.fillStyle = opts.background ?? "#ffffff";
        ctx.fillRect(0, 0, b.w, b.h);
      }
      ctx.translate(-b.x, -b.y);
      const roleSet = new Set(roles);
      const seen = { x: viewport.x + b.x, y: viewport.y - offsets[i] + b.y, w: viewport.w, h: viewport.h };
      for (const layer of p.layers) {
        if (!roleSet.has(layer.role)) continue;
        this.renderer.draw(ctx, this.res.object(layer.obj), true, seen, limits);
      }
      ctx.restore();
    }
  }
  /** Total size of a sheet in units, from its row/column runs. */
  sheetSize(view) {
    const sum = (runs) => (runs ?? []).reduce((acc, [n, s]) => acc + n * s, 0);
    return { width: sum(view.cols), height: sum(view.rows) };
  }
  /**
   * Draw the region of a sheet that intersects viewport (units) into ctx,
   * with viewport's top-left at device (0,0). Draws gridlines when the view asks for them;
   * row/column headers and frozen panes are the viewer's job.
   */
  renderSheet(ctx, view, viewport, opts) {
    return this.held((hold) => this.sheetHeld(ctx, view, viewport, opts, hold));
  }
  async sheetHeld(ctx, view, viewport, opts, hold) {
    const tile = tileSize(view);
    const tx0 = Math.floor(viewport.x / tile), ty0 = Math.floor(viewport.y / tile);
    const tx1 = Math.floor((viewport.x + viewport.w - 1e-6) / tile), ty1 = Math.floor((viewport.y + viewport.h - 1e-6) / tile);
    const keys = tilesIn(view, [{ tx0, ty0, tx1, ty1 }]);
    await Promise.all(keys.map(([, , h]) => this.res.prepare(h, hold)));
    const region = { x: 0, y: 0, w: viewport.w * opts.scale, h: viewport.h * opts.scale };
    await this.drawSettled(ctx, opts, region, () => this.drawSheet(ctx, view, viewport, opts, keys));
  }
  drawSheet(ctx, view, viewport, opts, keys) {
    const tile = tileSize(view);
    const s = opts.scale;
    const limits = new UseLimits();
    ctx.save();
    ctx.setTransform(s, 0, 0, s, -viewport.x * s, -viewport.y * s);
    if (opts.background !== null) {
      ctx.fillStyle = opts.background ?? "#ffffff";
      ctx.fillRect(viewport.x, viewport.y, viewport.w, viewport.h);
    }
    if (view.gridlines) this.drawGridlines(ctx, view, viewport, s);
    for (const [tx, ty, h] of keys) {
      ctx.save();
      ctx.translate(tx * tile, ty * tile);
      ctx.beginPath();
      ctx.rect(0, 0, tile, tile);
      ctx.clip();
      this.renderer.draw(ctx, this.res.object(h), true, { x: viewport.x - tx * tile, y: viewport.y - ty * tile, w: viewport.w, h: viewport.h }, limits);
      ctx.restore();
    }
    ctx.restore();
  }
  drawGridlines(ctx, view, vp, scale) {
    ctx.save();
    ctx.strokeStyle = "#d9d9d9";
    ctx.lineWidth = 1 / scale;
    ctx.beginPath();
    const walkRuns = (runs, from, to, line) => {
      let pos = 0;
      for (const [n, size] of runs ?? []) {
        for (let i = 0; i < n; i++) {
          pos += size;
          if (pos > to) return;
          if (pos >= from) line(pos);
        }
      }
    };
    const { width, height } = this.sheetSize(view);
    const x1 = Math.min(vp.x + vp.w, width), y1 = Math.min(vp.y + vp.h, height);
    const snap = (v) => Math.round(v * scale) / scale + 0.5 / scale;
    walkRuns(view.cols, vp.x, x1, (x) => {
      ctx.moveTo(snap(x), vp.y);
      ctx.lineTo(snap(x), y1);
    });
    walkRuns(view.rows, vp.y, y1, (y) => {
      ctx.moveTo(vp.x, snap(y));
      ctx.lineTo(x1, snap(y));
    });
    ctx.stroke();
    ctx.restore();
  }
};

// packages/render/src/content.ts
function concat(parts) {
  const out = { runs: [], nodes: [], links: [] };
  for (const c of parts) {
    const nodeBase = out.nodes.length, runBase = out.runs.length;
    const shift = (i) => i === void 0 || i < 0 ? i : i + nodeBase;
    for (const n of c.nodes) out.nodes.push({ ...n, parent: shift(n.parent) });
    for (const r of c.runs) out.runs.push({ ...r, node: shift(r.node) });
    for (const l of c.links) out.links.push({ ...l, after: l.after + runBase, node: shift(l.node) });
  }
  return out;
}
function within(c, r, dx = 0, dy = 0) {
  const inside = (x, y) => x - dx >= r.x && x - dx < r.x + r.w && y - dy >= r.y && y - dy <= r.y + r.h;
  const runs = [];
  const kept = [];
  for (const run of c.runs) {
    if (inside(run.x, run.y)) runs.push(run);
    kept.push(runs.length - 1);
  }
  const links = c.links.filter((l) => inside(l.x + l.w / 2, l.y + l.h / 2)).map((l) => ({ ...l, after: l.after >= 0 ? kept[l.after] : -1 }));
  return { runs, nodes: c.nodes, links };
}

// packages/render/src/search.ts
var DocumentSearch = class {
  constructor(doc, measure2, prepare) {
    this.doc = doc;
    this.measure = measure2;
    this.prepare = prepare ?? ((hash) => doc.ensure(hash));
  }
  indexes = /* @__PURE__ */ new Map();
  runs = /* @__PURE__ */ new Map();
  prepare;
  index(view) {
    let p = this.indexes.get(view.id);
    if (!p) {
      p = this.doc.textIndex(view).then((runs) => new TextSearch(runs));
      this.indexes.set(view.id, p);
    }
    return p;
  }
  /** Drop the index of a view whose pages changed; the next search builds it again. */
  forget(view) {
    this.indexes.delete(view.id);
  }
  async search(view, query, opts) {
    return (await this.index(view)).search(query, opts);
  }
  /** The searchable plain text of a view. */
  async text(view) {
    return (await this.index(view)).text;
  }
  objectFor(view, a, b) {
    if (view.kind === "sheet") return view.tiles?.[`${a},${b}`];
    return view.pages?.[a]?.layers[b]?.obj;
  }
  async runsOf(hash) {
    let runs = this.runs.get(hash);
    if (!runs) {
      const obj = await this.prepare(hash);
      runs = extractText(obj, (h) => this.doc.objectSync(h));
      this.runs.set(hash, runs);
    }
    return runs;
  }
  /** Rectangles covering a hit, in page coordinates (or sheet coordinates for sheets). */
  async locate(view, hit) {
    const out = [];
    const tile = tileSize(view);
    for (const seg of hit.segments) {
      const hash = this.objectFor(view, seg.a, seg.b);
      if (!hash) continue;
      const run = (await this.runsOf(hash))[seg.ordinal];
      if (!run || !hasExtent(run)) continue;
      const r = runRect(run, seg.start, seg.end, this.measure);
      if (!r) continue;
      if (view.kind === "sheet") {
        r.x += seg.a * tile;
        r.y += seg.b * tile;
      }
      out.push({ ...r, a: seg.a, b: seg.b });
    }
    return out;
  }
};
function hasExtent(run) {
  return run.font !== void 0 && (!run.altText || run.advance > 0);
}
function runRect(run, start, end, measure2) {
  if (!run.font) return void 0;
  const font = fontString(run.font, run.size);
  const full = measure2(font, run.text);
  const width = run.advance > 0 ? run.advance : full;
  const scale = run.advance > 0 && full > 0 ? run.advance / full : 1;
  const w1 = measure2(font, run.text.slice(0, start)) * scale;
  const w2 = measure2(font, run.text.slice(0, end)) * scale;
  const anchor = run.align === 1 ? -width : run.align === 2 ? -width / 2 : 0;
  const m = run.matrix;
  const xs = [anchor + w1, anchor + w2];
  const ys = [-run.size * 0.8, run.size * 0.2];
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const dx of xs) for (const dy of ys) {
    const px = m[0] * dx + m[2] * dy + run.x;
    const py = m[1] * dx + m[3] * dy + run.y;
    x0 = Math.min(x0, px);
    y0 = Math.min(y0, py);
    x1 = Math.max(x1, px);
    y1 = Math.max(y1, py);
  }
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

// packages/render/src/worker.ts
var open2;
var locked;
var retired = [];
var running = 0;
var settings = {};
var measureCtx = new OffscreenCanvas(1, 1).getContext("2d");
var measure = (font, text) => {
  measureCtx.font = font;
  return measureCtx.measureText(text).width;
};
var rasterizing = /* @__PURE__ */ new Map();
var nextRid = 1;
var rasterizeOnPage = (hash, data, width, height) => new Promise((resolve, reject) => {
  const rid = nextRid++;
  const timer = setTimeout(() => {
    rasterizing.delete(rid);
    reject(new Error("the page did not draw the SVG image"));
  }, 3e4);
  const done = () => {
    clearTimeout(timer);
    rasterizing.delete(rid);
  };
  rasterizing.set(rid, { resolve: (b) => {
    done();
    resolve(b);
  }, reject: (e) => {
    done();
    reject(e);
  } });
  const req = { type: "rasterize", rid, hash, data, width, height };
  self.postMessage(req);
});
function sourceOf(source) {
  switch (source.kind) {
    case "buffer":
      return new BufferSource(new Uint8Array(source.buffer));
    case "single":
      return source.range ? new RangeSource(source.url) : fetchSingle(source.url);
    case "split":
      return new SplitSource(source.base);
    case "segments":
      throw new Error("bdf: a document of segments is opened, not replaced");
  }
}
function opened(doc) {
  const pages = new PageRenderer(doc, {}, self.fonts, { imageBudget: settings.imageBudget, maxImagePixels: settings.maxImagePixels, holdLimit: settings.holdLimit, rasterizeSvg: rasterizeOnPage });
  return { doc, pages, search: new DocumentSearch(doc, measure, (h) => pages.res.prepareText(h)) };
}
function retire() {
  if (open2) {
    open2.segments?.close();
    retired.push(open2);
  }
  open2 = locked = void 0;
}
async function openSource(source, password, options = {}) {
  retire();
  settings = options;
  if (source.kind === "segments") {
    const segments = await SegmentLoader.open(source.url, { view: source.view, page: source.page });
    const o = { ...opened(segments.doc), segments };
    segments.onSegment = (s) => o.search.forget(o.doc.view(s.view));
    open2 = o;
    return o.doc.manifest;
  }
  locked = await sourceOf(source);
  return unlock(password);
}
async function ensurePages(o, view, pages) {
  if (o.segments) await Promise.all(pages.map((i) => o.segments.ensure(view.id, i)));
}
function pagesIn(o, view, viewport) {
  const { offsets } = o.pages.continuousLayout(view);
  const out = [];
  (view.pages ?? []).forEach((p, i) => {
    const h = (p.body ?? p).h;
    if (offsets[i] < viewport.y + viewport.h && offsets[i] + h > viewport.y) out.push(i);
  });
  return out;
}
async function unlock(password) {
  if (!locked) throw new Error("bdf: no document to unlock");
  const doc = await BdfDocument.open(locked, { password });
  locked = void 0;
  open2 = opened(doc);
  return doc.manifest;
}
async function replace(source) {
  const doc = await BdfDocument.open(await sourceOf(source));
  if (open2) retired.push(open2);
  open2 = opened(doc);
  return doc.manifest;
}
async function pageContent({ doc, pages }, page, matrix, roles, limits = new UseLimits()) {
  await pages.preparePageText(page);
  const layers = roles ? page.layers.filter((l) => roles.includes(l.role)) : page.layers;
  const c = concat(layers.map((layer) => extractContent(doc.objectSync(layer.obj), (h) => doc.objectSync(h), matrix, limits)));
  for (const r of c.runs) {
    if (r.advance === 0 && r.font && r.text && !r.altText) r.advance = measure(fontString(r.font, r.size), r.text);
  }
  return c;
}
async function continuousContent(o, view, viewport) {
  const { offsets } = o.pages.continuousLayout(view);
  const parts = [];
  const list = view.pages ?? [];
  const limits = new UseLimits();
  for (let i = 0; i < list.length; i++) {
    const p = list[i];
    const b = p.body ?? { x: 0, y: 0, w: p.w, h: p.h };
    if (offsets[i] >= viewport.y + viewport.h || offsets[i] + b.h <= viewport.y) continue;
    const dx = -b.x - viewport.x, dy = offsets[i] - b.y - viewport.y;
    parts.push(within(await pageContent(o, p, [1, 0, 0, 1, dx, dy], ["body", "annotation"], limits), b, dx, dy));
  }
  return concat(parts);
}
async function sheetContent({ doc, pages }, view, viewport) {
  const tile = tileSize(view);
  const ranges = [];
  for (const r of Array.isArray(viewport) ? viewport : [viewport]) {
    if (r.w <= 0 || r.h <= 0) continue;
    const tx0 = Math.max(0, Math.floor(r.x / tile)), ty0 = Math.max(0, Math.floor(r.y / tile));
    const tx1 = Math.floor((r.x + r.w - 1e-6) / tile), ty1 = Math.floor((r.y + r.h - 1e-6) / tile);
    ranges.push({ tx0, ty0, tx1, ty1 });
  }
  const parts = [];
  const limits = new UseLimits();
  for (const [tx, ty, h] of tilesIn(view, ranges)) {
    await pages.res.prepareText(h);
    const c = extractContent(doc.objectSync(h), (hh) => doc.objectSync(hh), [1, 0, 0, 1, tx * tile, ty * tile], limits);
    parts.push(within(c, { x: 0, y: 0, w: tile, h: tile - 1e-6 }, tx * tile, ty * tile));
  }
  return concat(parts);
}
var MAX_CANVAS_PIXELS = 1 << 28;
var MAX_CANVAS_SIDE = 32767;
function canvasFor(w, h) {
  const cw = Math.max(1, Math.ceil(w)), ch = Math.max(1, Math.ceil(h));
  if (!(cw <= MAX_CANVAS_SIDE && ch <= MAX_CANVAS_SIDE && cw * ch <= MAX_CANVAS_PIXELS)) throw new Error(`bdf: a canvas of ${cw} by ${ch} pixels is too large`);
  return new OffscreenCanvas(cw, ch);
}
async function handle(req) {
  switch (req.type) {
    case "open":
      return { result: await openSource(req.source, req.password, req.options), transfer: [] };
    case "unlock":
      return { result: await unlock(req.password), transfer: [] };
    case "replace":
      return { result: await replace(req.source), transfer: [] };
    case "close":
      retire();
      return { result: null, transfer: [] };
  }
  const o = open2;
  if (!o) throw new Error("bdf: no document open");
  const { doc, pages, search } = o;
  const view = doc.view(req.view);
  if (req.type === "addPage") {
    doc.addPage(view.id, req.page, await BdfDocument.open(new BufferSource(new Uint8Array(req.buffer))));
    search.forget(view);
    return { result: null, transfer: [] };
  }
  switch (req.type) {
    case "page":
    case "text":
    case "content":
      await ensurePages(o, view, [req.page]);
      break;
    case "continuous":
    case "continuousText":
    case "continuousContent":
      await ensurePages(o, view, pagesIn(o, view, req.viewport));
      break;
    case "locate":
      await ensurePages(o, view, req.hits.flatMap((h) => h.segments.map((s) => s.a)));
      break;
  }
  switch (req.type) {
    case "page": {
      const page = view.pages?.[req.page];
      if (!page) throw new Error(`bdf: no page ${req.page}`);
      const canvas = canvasFor(page.w * req.scale, page.h * req.scale);
      await pages.renderPage(canvas.getContext("2d"), page, { scale: req.scale, roles: req.roles });
      const bmp = canvas.transferToImageBitmap();
      return { result: bmp, transfer: [bmp] };
    }
    case "continuous": {
      const canvas = canvasFor(req.viewport.w * req.scale, req.viewport.h * req.scale);
      await pages.renderContinuous(canvas.getContext("2d"), view, req.viewport, { scale: req.scale });
      const bmp = canvas.transferToImageBitmap();
      return { result: bmp, transfer: [bmp] };
    }
    case "sheet": {
      const canvas = canvasFor(req.viewport.w * req.scale, req.viewport.h * req.scale);
      await pages.renderSheet(canvas.getContext("2d"), view, req.viewport, { scale: req.scale });
      const bmp = canvas.transferToImageBitmap();
      return { result: bmp, transfer: [bmp] };
    }
    case "text":
    case "content": {
      const page = view.pages?.[req.page];
      if (!page) throw new Error(`bdf: no page ${req.page}`);
      const c = await pageContent(o, page);
      return { result: req.type === "text" ? c.runs : c, transfer: [] };
    }
    case "continuousText":
    case "continuousContent": {
      const c = await continuousContent(o, view, req.viewport);
      return { result: req.type === "continuousText" ? c.runs : c, transfer: [] };
    }
    case "sheetContent":
      return { result: await sheetContent(o, view, req.viewport), transfer: [] };
    case "search":
      return { result: await search.search(view, req.query, req.options), transfer: [] };
    case "locate": {
      const rects = [];
      for (const hit of req.hits) rects.push(await search.locate(view, hit));
      return { result: rects, transfer: [] };
    }
    case "play": {
      const play = await doc.play(view);
      if (!play) return { result: null, transfer: [] };
      const seq = play.seq.slice().buffer;
      return { result: { seq, cues: play.cues }, transfer: [seq] };
    }
  }
}
function errorCode(e) {
  if (e instanceof BdfPasswordError) return e.reason === "required" ? "password-required" : "wrong-password";
  if (e instanceof BdfSegmentError) return e.status === 429 ? "rate-limited" : e.status === 401 || e.status === 403 ? "not-allowed" : void 0;
  return void 0;
}
self.onmessage = async (ev) => {
  if ("rid" in ev.data) {
    const res = ev.data;
    const p = rasterizing.get(res.rid);
    if (res.ok) {
      if (p) p.resolve(res.bitmap);
      else res.bitmap.close();
    } else {
      p?.reject(new Error(res.error));
    }
    return;
  }
  const req = ev.data;
  running++;
  try {
    const { result, transfer } = await handle(req);
    const res = { id: req.id, ok: true, result };
    self.postMessage(res, transfer);
  } catch (e) {
    const code = errorCode(e);
    const res = { id: req.id, ok: false, error: e instanceof Error ? e.message : String(e), code };
    self.postMessage(res);
  } finally {
    if (--running === 0) for (const r of retired.splice(0)) r.pages.dispose();
  }
};
