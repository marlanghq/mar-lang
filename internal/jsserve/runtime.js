// mar JS runtime. Interprets a mar AST in the browser and runs an MVU loop.
//
// Loaded into a page along with the program AST and the entry point. The
// runtime evaluates the program until it produces a special "mountApp"
// effect, then takes over and runs the init/update/view loop with real DOM.
//
// Dev tooling (time-travel panel, dev dock, SSE reload, error overlay) is
// gated behind the build-time constant `__MAR_DEV__`. The dev path leaves
// it as `true`; `mar build` runs the source through esbuild with
// `Define: __MAR_DEV__ = false`, and dead-code elimination drops every
// `if (__MAR_DEV__) { ... }` block from the production bundle.

(function (global) {
  'use strict';

  // Build-time dev flag. The dev server (internal/jsserve/server.go)
  // ships this file verbatim, leaving the value `true`. `mar build`
  // rewrites this exact line to `false` before passing the source to
  // esbuild, which then constant-folds + DCEs every `if (__MAR_DEV__)`
  // block out of the production bundle. `const` (not `var` / `let`)
  // so esbuild can prove the binding is immutable.
  const __MAR_DEV__ = true;

  // ---------- Value constructors ----------

  const VInt    = (n)        => ({ k: 'I', n });
  const VFloat  = (n)        => ({ k: 'F', n });
  // VDuration — time interval normalized to seconds. Constructed
  // only via Time.seconds / .minutes / .hours / .days / .weeks so
  // unit confusion is impossible at the call site.
  const VDuration = (seconds) => ({ k: 'D', seconds });
  // VTime — absolute moment, Unix milliseconds. Constructed via
  // Time.now (effect) or Time.fromIso. Wire format is ISO 8601.
  const VTime = (millis) => ({ k: 'TM', millis });
  const VString = (s)        => ({ k: 'S', s });
  const VBool   = (b)        => ({ k: 'B', b });
  const VUnit   = ()         => ({ k: 'U' });
  const VList   = (xs)       => ({ k: 'L', xs });
  const VTuple  = (xs)       => ({ k: 'T', xs });
  const VRecord = (fields, order) => ({ k: 'R', fields, order });
  const VCtor   = (tag, args)=> ({ k: 'C', tag, args: args || [] });
  // VChar — Unicode code point, distinct from a 1-char VString. Same
  // model as Go's rune / Swift's Unicode.Scalar. JSON wire format is
  // {"__char": "x"} (see jsToMar / marToJs below). `c` is the integer
  // code point.
  const VChar = (c) => ({ k: 'Ch', c });

  // VDict / VSet — ordered, polymorphic comparable-keyed containers.
  // Internal representation parallels the Go runtime: VDict.pairs is a
  // sorted array of {key, value}; VSet.items is a sorted array. Sort
  // key is cmpValues; "comparable" means Int / Float / String at
  // runtime. Same wire markers as the Go side (`__dict`, `__set`).
  const VDict   = (pairs)    => ({ k: 'M', pairs: pairs || [] });
  const VSet    = (items)    => ({ k: 'Se', items: items || [] });
  const VFn     = (params, body, env, native, arity, applied) =>
    ({ k: 'Fn', params, body, env, native, arity, applied: applied || [] });
  const VView   = (tag, attrs, children, text, msg) =>
    ({ k: 'V', tag, attrs: attrs || [], children: children || [], text: text || '', msg: msg });
  const VEffect = (run, tag) => ({ k: 'E', run, tag: tag || '' });

  // ---------- JSON ⇄ MarValue ----------
  //
  // Lifted to the IIFE level (rather than inside makeBuiltinEnv)
  // so that mountPages's auth helpers — fetchAuthMe in particular
  // — can reach jsToMar by closure scope. Without this, the user
  // record from /_auth/whoami decodes via a ReferenceError catch and
  // collapses to Nothing, which silently redirects authenticated
  // users back to /sign-in instead of mounting the protected page.

  function jsToMar(v) {
    // null → VUnit. Previously null → VCtor('Nothing'), but that
    // collided with VUnit's wire encoding (Go encodes () as null):
    // a service `Int -> ()` returned null, decoded as Nothing on the
    // client, and pattern `Ok ()` failed at runtime. Both Maybe
    // constructors now tag uniformly via {"__ctor":"Nothing"} /
    // {"__ctor":"Just",...}, matching encodeValue / valueToAny on
    // the Go side and MarJSONCodec on iOS.
    if (v === null || v === undefined) return VUnit();
    if (typeof v === 'number') return VInt(v | 0);
    if (typeof v === 'string') return VString(v);
    if (typeof v === 'boolean') return VBool(v);
    if (Array.isArray(v)) return VList(v.map(jsToMar));
    if (typeof v === 'object') {
      // Tagged constructor — round-trip from {__ctor: "Tag"} or
      // {__ctor: "Tag", __args: [...]}. Convention shared with the
      // Go encoder (encodeValue / valueToAny). Any object missing
      // __ctor falls through to the record path.
      if (typeof v.__ctor === 'string') {
        const args = Array.isArray(v.__args) ? v.__args.map(jsToMar) : [];
        return VCtor(v.__ctor, args);
      }
      // Time round-trip — `{__time: "ISO 8601"}` from the Go
      // encoder rebuilds a VTime so user code typed as
      // `createdAt : Time` actually receives a Time, not a String.
      if (typeof v.__time === 'string') {
        const ms = Date.parse(v.__time);
        if (!isNaN(ms)) return VTime(ms);
      }
      // Char round-trip — `{__char: "x"}` from the Go encoder. Take
      // the FIRST code point (covers BMP + supplementary planes). A
      // malformed empty string degrades to U+FFFD rather than NaN.
      if (typeof v.__char === 'string') {
        const cp = v.__char.codePointAt(0);
        return VChar(cp == null ? 0xFFFD : cp);
      }
      // Dict / Set round-trip. The encoder emits `{__dict:[[k,v],...]}`
      // and `{__set:[i, ...]}`. We rebuild via the runtime's own
      // insert helpers so the sorted invariant survives even when the
      // wire payload arrives out of order (hand-written JSON, network
      // intermediaries, etc).
      if (Array.isArray(v.__dict)) {
        let d = VDict([]);
        for (const pair of v.__dict) {
          if (!Array.isArray(pair) || pair.length !== 2) continue;
          d = dictInsertHelper(d, jsToMar(pair[0]), jsToMar(pair[1]));
        }
        return d;
      }
      if (Array.isArray(v.__set)) {
        let s = VSet([]);
        for (const it of v.__set) {
          s = setInsertHelper(s, jsToMar(it));
        }
        return s;
      }
      const fields = {};
      const order = [];
      for (const k of Object.keys(v)) {
        fields[k] = jsToMar(v[k]);
        order.push(k);
      }
      return VRecord(fields, order);
    }
    return VString(String(v));
  }

  function marToJs(v) {
    if (!v || typeof v !== 'object') return v;
    switch (v.k) {
      case 'I': return v.n;
      case 'S': return v.s;
      case 'B': return v.b;
      case 'U': return null;
      case 'L': return v.xs.map(marToJs);
      case 'T': return v.xs.map(marToJs);
      case 'R': {
        const out = {};
        for (const k of (v.order || Object.keys(v.fields))) out[k] = marToJs(v.fields[k]);
        return out;
      }
      case 'C':
        // Every ctor — Nothing and Just included — uses the
        // `__ctor` marker so generic decoders (jsToMar here, Go's
        // jsonToMar, iOS MarJSONCodec) can rebuild a VCtor without
        // needing type info at runtime. Transparent encodings
        // (Nothing → null, Just x → x) would collide with VUnit's
        // null and break generic decoders for record payloads, so
        // we tag uniformly. The Ok/Err shortcuts below stay
        // because the server-side decode for those tags is
        // type-directed already.
        if (v.tag === 'Ok') return marToJs(v.args[0]);
        if (v.tag === 'Err') return { error: marToJs(v.args[0]) };
        if (!v.args || v.args.length === 0) return { __ctor: v.tag };
        return { __ctor: v.tag, __args: v.args.map(marToJs) };
      case 'D': return v.seconds;
      case 'TM': return { __time: new Date(v.millis).toISOString() };
      case 'Ch': return { __char: String.fromCodePoint(v.c) };
      case 'M':
        return { __dict: v.pairs.map(p => [marToJs(p.key), marToJs(p.value)]) };
      case 'Se':
        return { __set: v.items.map(marToJs) };
      default: return null;
    }
  }

  // pathParamString renders a single value for a `{name:Type}` URL
  // segment. Mirrors the Go encodePathSegment: String raw, Int/Bool as
  // their literal, enum ctor lowercased.
  function pathParamString(v) {
    if (v && typeof v === 'object' && v.__ctor) return String(v.__ctor).toLowerCase();
    if (typeof v === 'boolean') return v ? 'true' : 'false';
    return String(v);
  }

  // buildServiceRequest turns a contract's verb + path pattern and the
  // request value into a concrete URL and (optionally) a JSON body. Typed
  // `{name:Type}` params are substituted into the URL; the remaining
  // fields ride in the query (`q` JSON param, for GET / DELETE) or the
  // JSON body (POST / PUT / PATCH). A `()` request yields no body.
  function buildServiceRequest(verb, pattern, req) {
    const obj = marToJs(req);
    const isObj = obj && typeof obj === 'object' && !Array.isArray(obj);
    const fields = isObj ? Object.assign({}, obj) : {};
    let hadParam = false;
    const segs = pattern.split('/').filter(s => s.length).map(s => {
      const m = /^\{([^:}]+):[^}]*\}$/.exec(s);
      if (!m) return s;
      hadParam = true;
      const name = m[1];
      const enc = encodeURIComponent(pathParamString(fields[name]));
      delete fields[name];
      return enc;
    });
    const url = '/' + segs.join('/');
    const restKeys = Object.keys(fields);
    if (verb === 'GET' || verb === 'DELETE') {
      if (restKeys.length > 0) {
        return { url: url + '?q=' + encodeURIComponent(JSON.stringify(fields)), body: undefined };
      }
      return { url, body: undefined };
    }
    // POST / PUT / PATCH
    if (!isObj) {
      return { url, body: JSON.stringify(obj) };
    }
    if (restKeys.length === 0 && hadParam) {
      return { url, body: undefined };
    }
    return { url, body: JSON.stringify(fields) };
  }

  // --- Dict / Set helpers (hoisted to IIFE level so jsToMar can use
  // them during decode, before makeBuiltinEnv has installed builtins).
  //
  // `dictSearch` / `setSearch` return the insertion index (binary
  // search) plus a `found` flag. Sort key is cmpValues; runtime
  // throws if cmpValues hits a non-comparable Mar value.

  function dictSearch(d, key) {
    let lo = 0, hi = d.pairs.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const c = cmpValues(d.pairs[mid].key, key);
      if (c < 0) lo = mid + 1; else hi = mid;
    }
    const found = lo < d.pairs.length && cmpValues(d.pairs[lo].key, key) === 0;
    return { idx: lo, found };
  }
  function dictInsertHelper(d, key, value) {
    const { idx, found } = dictSearch(d, key);
    const pairs = d.pairs.slice();
    if (found) pairs[idx] = { key, value };
    else pairs.splice(idx, 0, { key, value });
    return VDict(pairs);
  }
  function dictRemoveHelper(d, idx) {
    const pairs = d.pairs.slice();
    pairs.splice(idx, 1);
    return VDict(pairs);
  }

  function setSearch(s, key) {
    let lo = 0, hi = s.items.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const c = cmpValues(s.items[mid], key);
      if (c < 0) lo = mid + 1; else hi = mid;
    }
    const found = lo < s.items.length && cmpValues(s.items[lo], key) === 0;
    return { idx: lo, found };
  }
  function setInsertHelper(s, item) {
    const { idx, found } = setSearch(s, item);
    if (found) return s;
    const items = s.items.slice();
    items.splice(idx, 0, item);
    return VSet(items);
  }

  // ---------- Path patterns ----------
  //
  // Hoisted to IIFE level so both makeBuiltinEnv (linkTo / Nav.pushTo
  // / Nav.replaceTo) and mountPages (URL matcher for dynamic pages)
  // can share the same parser. Same shape, same caching, same error
  // wording on either side.

  // Custom-enum types registered by the module loader. Entries look
  // like { Role: ["Member", "Admin"] }. URL matchers consult this
  // to decode `{role:Role}` segments; linkTo / Nav.pushTo emit
  // lowercased ctor names back into URLs. Mirrors
  // internal/runtime/path.go's EnumTypes.
  const enumTypes = Object.create(null);

  // Path matcher for Page.dynamic / Page.dynamicProtected.
  // Pattern '/notes/{id:Int}' splits to segments [{lit:'notes'},
  // {param:'id', type:'Int'}]. Empty leading/trailing slashes are
  // stripped so '/' parses as []. Bare ':id' (legacy/Express-style)
  // is rejected to keep the error consistent with the Go side.
  function parsePathPattern(p) {
    const parts = p.split('/').filter(s => s !== '');
    const segs = [];
    const seen = {};
    for (const part of parts) {
      if (part.startsWith(':')) {
        throw new Error(`path "${p}": bare ":${part.slice(1)}" not supported. Use "{${part.slice(1)}:Type}".`);
      }
      if (part.startsWith('{') && part.endsWith('}')) {
        const inner = part.slice(1, -1);
        const colon = inner.indexOf(':');
        if (colon < 0) {
          throw new Error(`path "${p}": param "{${inner}}" requires a type, e.g. "{${inner}:String}" or "{${inner}:Int}".`);
        }
        const name = inner.slice(0, colon).trim();
        const type = inner.slice(colon + 1).trim();
        if (!name) {
          throw new Error(`path "${p}": empty param name in "{${inner}}".`);
        }
        // Built-ins resolve immediately; everything else has to be
        // a registered enum type. The registry may not be populated
        // yet at very early bootstrap, so the decoder/encoder also
        // re-check at use time — the typechecker is the authoritative
        // gate, this is a defensive fallback for handwritten patterns.
        if (type !== 'String' && type !== 'Int' && !enumTypes[type]) {
          throw new Error(`path "${p}": unknown type "${type}" for param "${name}". Allowed: String, Int, or a zero-arg enum type.`);
        }
        if (seen[name]) {
          throw new Error(`path "${p}": duplicate param "${name}".`);
        }
        seen[name] = true;
        segs.push({ kind: 'param', name, type });
        continue;
      }
      if (part.includes('{') || part.includes('}')) {
        throw new Error(`path "${p}": malformed segment "${part}" (use "{name:Type}").`);
      }
      segs.push({ kind: 'lit', value: part });
    }
    return { source: p, segments: segs };
  }
  // Decode a single URL segment per its declared type. Returns
  // VString / VInt / VCtor on success, null on type mismatch
  // (e.g. "abc" against `{id:Int}`, or "foo" against `{role:Role}`
  // where Role has no `Foo` ctor). Match failure surfaces as null
  // further up — the matcher tries the next page.
  function decodePathSegment(raw, type) {
    let decoded = raw;
    try { decoded = decodeURIComponent(raw); } catch (_) {}
    if (type === 'String') return VString(decoded);
    if (type === 'Int') {
      if (!/^-?\d+$/.test(decoded)) return null;
      const n = parseInt(decoded, 10);
      if (Number.isNaN(n)) return null;
      return VInt(n);
    }
    // Custom enum: case-insensitive match against the registered
    // ctor list. Lowercased URL ↔ PascalCase ctor is the convention.
    const ctors = enumTypes[type];
    if (ctors) {
      const want = decoded.toLowerCase();
      for (const c of ctors) {
        if (c.toLowerCase() === want) return VCtor(c);
      }
      return null;
    }
    return null;
  }
  // Encode a VInt / VString / VCtor back to a URL segment. Used by
  // linkTo / Nav.pushTo. Type mismatches throw — caller surfaces
  // them at the call-site with the path source for context.
  function encodePathSegment(v, type) {
    if (type === 'String') {
      if (!v || v.k !== 'S') throw new Error(`expected String, got ${v && v.k}`);
      return encodeURIComponent(v.s);
    }
    if (type === 'Int') {
      if (!v || v.k !== 'I') throw new Error(`expected Int, got ${v && v.k}`);
      return String(v.n);
    }
    const ctors = enumTypes[type];
    if (ctors) {
      if (!v || v.k !== 'C') throw new Error(`expected ${type}, got ${v && v.k}`);
      // Defensive: ensure the ctor is actually a member. The
      // typechecker should already guarantee this, but a stray
      // runtime VCtor would otherwise produce a URL with arbitrary
      // tag names.
      for (const c of ctors) {
        if (c === v.tag) return v.tag.toLowerCase();
      }
      throw new Error(`ctor "${v.tag}" is not a member of ${type}`);
    }
    throw new Error(`unknown path-param type "${type}"`);
  }
  // Match a URL against a parsed pattern. Returns a VRecord with
  // typed fields on success, null on miss. Segment count must
  // match exactly — '/notes/{id:Int}' won't match '/notes' or
  // '/notes/abc/edit', and '/notes/abc' will miss '{id:Int}'.
  function matchPathPattern(urlPath, pattern) {
    const urlSegs = urlPath.split('/').filter(s => s !== '');
    if (urlSegs.length !== pattern.segments.length) return null;
    const params = {};
    const order = [];
    for (let i = 0; i < urlSegs.length; i++) {
      const seg = pattern.segments[i];
      const us = urlSegs[i];
      if (seg.kind === 'lit') {
        if (seg.value !== us) return null;
        continue;
      }
      const v = decodePathSegment(us, seg.type);
      if (v === null) return null;
      params[seg.name] = v;
      order.push(seg.name);
    }
    return VRecord(params, order);
  }
  // Build a URL string from a parsed pattern + a VRecord of params.
  // Used by linkTo and Nav.pushTo / Nav.replaceTo. Throws on
  // missing/wrong-type fields so user code gets a clear error
  // rather than a silently malformed URL.
  function buildPathURL(pattern, paramsRec) {
    let out = '';
    for (const seg of pattern.segments) {
      out += '/';
      if (seg.kind === 'lit') {
        out += seg.value;
        continue;
      }
      const v = (paramsRec && paramsRec.fields) ? paramsRec.fields[seg.name] : undefined;
      if (v === undefined) {
        throw new Error(`linkTo ${pattern.source}: missing param "${seg.name}"`);
      }
      try {
        out += encodePathSegment(v, seg.type);
      } catch (e) {
        throw new Error(`linkTo ${pattern.source}: param "${seg.name}": ${e.message}`);
      }
    }
    return out === '' ? '/' : out;
  }

  // ---------- Environment ----------

  function envNew(parent) { return { bindings: Object.create(null), parent }; }
  function envBind(env, name, val) {
    const e = envNew(env);
    e.bindings[name] = val;
    return e;
  }
  function envDefine(env, name, val) { env.bindings[name] = val; }
  function envLookup(env, name) {
    for (let cur = env; cur; cur = cur.parent) {
      if (cur.bindings && cur.bindings[name] !== undefined) return cur.bindings[name];
    }
    return undefined;
  }

  // envExportsOf collects every binding that belongs to module
  // `modName`: keys of the form `modName.suffix` where suffix has no
  // further dot (so `Mar.Admin.x` exports from `Mar.Admin`, not from
  // `Mar`). Powers `import M exposing (..)` at load time, mirroring
  // the Go runtime's Env.ExportsOf. Walks frames outermost-first so
  // inner bindings win, matching envLookup's shadowing order.
  function envExportsOf(env, modName) {
    const prefix = modName + '.';
    const frames = [];
    for (let cur = env; cur; cur = cur.parent) frames.push(cur);
    const out = Object.create(null);
    for (let i = frames.length - 1; i >= 0; i--) {
      const b = frames[i].bindings;
      if (!b) continue;
      for (const name in b) {
        if (!name.startsWith(prefix)) continue;
        const suffix = name.slice(prefix.length);
        if (suffix === '' || suffix.indexOf('.') !== -1) continue;
        out[suffix] = b[name];
      }
    }
    return out;
  }

  // ---------- Native function helpers ----------

  function native(arity, fn) {
    return VFn(null, null, null, fn, arity, []);
  }

  // ---------- Apply ----------

  function apply(fn, arg) {
    if (fn.k !== 'Fn') throw new Error('apply: not a function: ' + JSON.stringify(fn));
    const applied = fn.applied.concat([arg]);
    if (applied.length < fn.arity) {
      return VFn(fn.params, fn.body, fn.env, fn.native, fn.arity, applied);
    }
    if (fn.native) return fn.native(applied);
    let env = fn.env;
    for (let i = 0; i < fn.params.length; i++) {
      env = envBind(env, fn.params[i], applied[i]);
    }
    return evalExpr(fn.body, env);
  }

  // ---------- Pattern matching ----------

  // describeValue produces a short human-readable summary of a
  // runtime value — used in error messages where pasting the full
  // value would be too noisy (records with dozens of fields, deeply
  // nested ctors). Keeps the same format for every value kind so
  // the operator can match the error against the AST quickly.
  function describeValue(v) {
    if (v == null) return 'null';
    switch (v.k) {
      case 'I': return 'Int ' + v.n;
      case 'F': return 'Float ' + v.n;
      case 'S': return 'String ' + JSON.stringify(v.s);
      case 'B': return 'Bool ' + v.b;
      case 'U': return 'Unit';
      case 'L': return 'List[' + v.xs.length + ']';
      case 'T': return 'Tuple(' + v.xs.length + ')';
      case 'R': return 'Record{' + (v.order || Object.keys(v.fields)).join(',') + '}';
      case 'C': return 'Ctor ' + v.tag + (v.args && v.args.length ? '(' + v.args.length + ' arg)' : '');
      case 'Fn': return 'Fn arity=' + v.arity;
      case 'V': return 'View<' + v.tag + '>';
      case 'E': return 'Effect<' + (v.tag || '?') + '>';
      case 'D': return 'Duration ' + v.seconds + 's';
      case 'TM': return 'Time ' + v.millis + 'ms';
    }
    return 'unknown';
  }

  function matchInto(pat, v, bindings) {
    switch (pat.kind) {
      case 'PWildcard': return true;
      case 'PVar': bindings[pat.name] = v; return true;
      case 'PInt': return v.k === 'I' && v.n === pat.value;
      case 'PString': return v.k === 'S' && v.s === pat.value;
      case 'PChar': return v.k === 'Ch' && v.c === pat.value;
      case 'PUnit': return v.k === 'U';
      case 'PCtor':
        if (v.k !== 'C' || v.tag !== pat.name || v.args.length !== pat.args.length) return false;
        for (let i = 0; i < pat.args.length; i++) {
          if (!matchInto(pat.args[i], v.args[i], bindings)) return false;
        }
        return true;
      case 'PTuple':
        if (v.k !== 'T' || v.xs.length !== pat.members.length) return false;
        for (let i = 0; i < pat.members.length; i++) {
          if (!matchInto(pat.members[i], v.xs[i], bindings)) return false;
        }
        return true;
      case 'PList':
        if (v.k !== 'L' || v.xs.length !== pat.elements.length) return false;
        for (let i = 0; i < pat.elements.length; i++) {
          if (!matchInto(pat.elements[i], v.xs[i], bindings)) return false;
        }
        return true;
      case 'PCons':
        if (v.k !== 'L' || v.xs.length === 0) return false;
        if (!matchInto(pat.head, v.xs[0], bindings)) return false;
        return matchInto(pat.tail, VList(v.xs.slice(1)), bindings);
      case 'PRecord':
        // `{ f1, f2, ... }` — bind each listed field's value into
        // the scope. Partial-match: the value record may carry
        // additional fields the pattern doesn't list. The
        // typechecker already verified every listed field exists on
        // the value's static type, so a missing field here would be
        // a typechecker bug — treat it as a non-match rather than a
        // hard crash.
        //
        // hasOwnProperty (not `in`) so prototype keys like
        // `toString` / `hasOwnProperty` themselves can never satisfy
        // the lookup. v.fields is a plain object literal so its
        // prototype IS Object.prototype.
        if (v.k !== 'R') return false;
        for (let i = 0; i < pat.fields.length; i++) {
          const fname = pat.fields[i];
          if (!Object.prototype.hasOwnProperty.call(v.fields, fname)) return false;
          bindings[fname] = v.fields[fname];
        }
        return true;
    }
    return false;
  }

  // ---------- Eval ----------

  function evalExpr(e, env) {
    switch (e.kind) {
      case 'EInt':    return VInt(e.value);
      case 'EFloat':  return VFloat(e.value);
      case 'EString': return VString(e.value);
      case 'EChar':   return VChar(e.value);
      case 'EUnit':   return VUnit();
      case 'EVar': {
        const v = envLookup(env, e.name);
        if (v === undefined) throw new Error('unbound name: ' + e.name);
        return v;
      }
      case 'ECtor': {
        // Qualified or bare constructor. The full key (`Module.Sub.Name`)
        // is the same for every evaluation of this AST node, so memoize
        // it on the node itself the first time we build it.
        const key = e._qn || (e._qn = (e.module && e.module.length > 0
          ? e.module.join('.') + '.' + e.name
          : e.name));
        let v = envLookup(env, key);
        if (v === undefined) v = envLookup(env, e.name);
        if (v === undefined) throw new Error('unbound constructor: ' + key);
        return v;
      }
      case 'EQualified': {
        const key = e._qn || (e._qn = e.module.join('.') + '.' + e.name);
        let v = envLookup(env, key);
        if (v === undefined) v = envLookup(env, e.name);
        if (v === undefined) throw new Error('unbound name: ' + key);
        return v;
      }
      case 'ENegate': {
        const v = evalExpr(e.inner, env);
        if (v.k === 'I') return VInt(-v.n);
        if (v.k === 'F') return VFloat(-v.n);
        throw new Error('negate: unsupported type');
      }
      case 'EApp': {
        const fn = evalExpr(e.fn, env);
        const arg = evalExpr(e.arg, env);
        return apply(fn, arg);
      }
      case 'EBinop': {
        const op = envLookup(env, e.op);
        if (op === undefined) throw new Error('unknown operator: ' + e.op);
        const left = evalExpr(e.left, env);
        const right = evalExpr(e.right, env);
        return apply(apply(op, left), right);
      }
      case 'ELambda': {
        // Param names depend only on the AST node (immutable), so cache
        // the array on the node itself instead of remapping every call.
        const paramNames = e._pn || (e._pn = e.params.map(p => {
          if (p.kind === 'PVar') return p.name;
          if (p.kind === 'PWildcard') return '__wild';
          throw new Error('lambda params must be names or _');
        }));
        return VFn(paramNames, e.body, env, null, paramNames.length, []);
      }
      case 'EIf': {
        const c = evalExpr(e.cond, env);
        return c.b ? evalExpr(e.then, env) : evalExpr(e.else, env);
      }
      case 'ELet': {
        let cur = env;
        for (const b of e.bindings) {
          const val = evalExpr(b.body, cur);
          const bindings = {};
          if (matchInto(b.pattern, val, bindings)) {
            const frame = envNew(cur);
            Object.assign(frame.bindings, bindings);
            cur = frame;
          }
        }
        return evalExpr(e.body, cur);
      }
      case 'ETuple':
        return VTuple(e.members.map(m => evalExpr(m, env)));
      case 'EList':
        return VList(e.elements.map(x => evalExpr(x, env)));
      case 'ERecord': {
        const fields = {};
        const order = [];
        for (const f of e.fields) {
          fields[f.name] = evalExpr(f.value, env);
          order.push(f.name);
        }
        return VRecord(fields, order);
      }
      case 'ERecordUpdate': {
        const base = evalExpr(e.record, env);
        if (base.k !== 'R') throw new Error('record update on non-record');
        const fields = Object.assign({}, base.fields);
        for (const f of e.fields) fields[f.name] = evalExpr(f.value, env);
        return VRecord(fields, base.order);
      }
      case 'EFieldAccess': {
        const r = evalExpr(e.record, env);
        if (r.k !== 'R') throw new Error('field access on non-record');
        return r.fields[e.field];
      }
      case 'EFieldAccessor':
        return native(1, args => {
          const rec = args[0];
          if (rec.k !== 'R') throw new Error('field accessor on non-record');
          return rec.fields[e.field];
        });
      case 'ECase': {
        const subj = evalExpr(e.subject, env);
        for (const br of e.branches) {
          const bindings = {};
          if (matchInto(br.pattern, subj, bindings)) {
            const frame = envNew(env);
            Object.assign(frame.bindings, bindings);
            return evalExpr(br.body, frame);
          }
        }
        // Include the subject's shape in the error so the
        // browser console points at WHICH case match exploded.
        // A bare "no case branch matched" forces the operator to
        // bisect by guesswork; with the tag/kind we usually
        // spot the missing branch in seconds.
        throw new Error('no case branch matched (subject: ' + describeValue(subj) + ')');
      }
    }
    throw new Error('unsupported expr: ' + e.kind);
  }

  // ---------- Builtins ----------

  function eqValues(a, b) {
    if (a.k !== b.k) return false;
    switch (a.k) {
      case 'I': case 'F': return a.n === b.n;
      case 'S': return a.s === b.s;
      case 'Ch': return a.c === b.c;
      case 'B': return a.b === b.b;
      case 'U': return true;
      case 'C':
        if (a.tag !== b.tag || a.args.length !== b.args.length) return false;
        for (let i = 0; i < a.args.length; i++) if (!eqValues(a.args[i], b.args[i])) return false;
        return true;
      case 'L':
        if (a.xs.length !== b.xs.length) return false;
        for (let i = 0; i < a.xs.length; i++) if (!eqValues(a.xs[i], b.xs[i])) return false;
        return true;
      case 'T':
        for (let i = 0; i < a.xs.length; i++) if (!eqValues(a.xs[i], b.xs[i])) return false;
        return true;
      case 'M':
        if (a.pairs.length !== b.pairs.length) return false;
        for (let i = 0; i < a.pairs.length; i++) {
          if (!eqValues(a.pairs[i].key, b.pairs[i].key)) return false;
          if (!eqValues(a.pairs[i].value, b.pairs[i].value)) return false;
        }
        return true;
      case 'Se':
        if (a.items.length !== b.items.length) return false;
        for (let i = 0; i < a.items.length; i++) {
          if (!eqValues(a.items[i], b.items[i])) return false;
        }
        return true;
    }
    return false;
  }

  function cmpValues(a, b) {
    if (a.k === 'I' || a.k === 'F') return a.n - b.n;
    if (a.k === 'S') return a.s < b.s ? -1 : a.s > b.s ? 1 : 0;
    if (a.k === 'Ch') return a.c - b.c;
    return 0;
  }

  // preservedModel / preservedScreenModels survive across marReload (the
  // IIFE wrapping this file runs once per page load, not per marRun call).
  // mountApp / mountScreens read them on entry to restore the user's last
  // state instead of always starting from init. Discarded if the new view
  // function rejects the old shape.
  let preservedModel = null;
  let preservedScreenModels = {};

  // Time-travel state survives marReload too. After hot-reload, frames are
  // validated against the new view functions: any frame whose nextModel
  // can't be rendered by the (possibly updated) view of its page is
  // discarded. Empty list = no usable history → fresh start.
  //
  // currentJumpToFrame is updated by every mountPages call so the
  // module-level keyboard handler (registered once below) always
  // dispatches into the freshest closure. Without this indirection,
  // re-registering the listener on each hot-reload would multiply it
  // and arrow keys would skip N frames at a time.
  let preservedTimeTravel = null;
  let currentJumpToFrame = null;
  if (__MAR_DEV__) {
    preservedTimeTravel = {
      frames: [],   // [{ msg, prevModel, nextModel, hadEffect, pagePath, time }]
      cursor: -1,
      traveling: false,
    };
    if (typeof document !== 'undefined') {
      document.addEventListener('keydown', function (e) {
        const tag = (e.target && e.target.tagName) || '';
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        try {
          if (typeof localStorage === 'undefined') return;
          if (localStorage.getItem('mar-dev-dock-expanded') !== 'time-travel') return;
        } catch (_) { return; }
        if (!currentJumpToFrame) return;
        if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') {
          e.preventDefault();
          currentJumpToFrame(preservedTimeTravel.cursor - 1);
        } else if (e.key === 'ArrowRight' || e.key === 'ArrowUp') {
          e.preventDefault();
          currentJumpToFrame(preservedTimeTravel.cursor + 1);
        }
      });
    }
  }

  // currentDispatch is set by App.serve before render. Click handlers use it
  // to dispatch a Msg into the running update loop.
  let currentDispatch = null;

  // ---------- Subscriptions (Sub) reconciler ----------
  // A Sub is a declarative value `{k:'SUB', items:[{src,key,intervalMs,tagger}]}`
  // produced by a page's `subscriptions : Model -> Sub Msg`. After every render
  // we reconcile the live sources against the page's desired set: start newly
  // returned ones, stop ones no longer returned, refresh taggers on survivors.
  // Identity is the structural key (the data args, never the tagger), so two
  // `Time.every` at the same interval share one timer. `activeSubs` is module
  // level so a hot reload (which re-runs mountPages) can tear down the previous
  // mount's live timers via teardownAllSubs().
  const activeSubs = new Map(); // key -> { src, handle, taggers: [fn] }
  const subSources = {
    timeEvery: {
      start: (g, fire) => setInterval(fire, g.intervalMs),
      stop: (rec) => clearInterval(rec.handle),
      fire: (rec) => {
        const now = VTime(Date.now());
        for (const tg of rec.taggers) {
          if (currentDispatch) currentDispatch(apply(tg, now));
        }
      },
    },
  };
  function teardownAllSubs() {
    for (const rec of activeSubs.values()) subSources[rec.src].stop(rec);
    activeSubs.clear();
  }
  function reconcileSubs(subValue) {
    const desired = new Map(); // key -> { src, intervalMs, taggers: [] }
    const items = (subValue && subValue.k === 'SUB' && subValue.items) ? subValue.items : [];
    for (const it of items) {
      let g = desired.get(it.key);
      if (!g) { g = { src: it.src, intervalMs: it.intervalMs, taggers: [] }; desired.set(it.key, g); }
      g.taggers.push(it.tagger);
    }
    for (const [key, rec] of [...activeSubs]) {
      if (!desired.has(key)) { subSources[rec.src].stop(rec); activeSubs.delete(key); }
    }
    for (const [key, g] of desired) {
      const existing = activeSubs.get(key);
      if (existing) { existing.taggers = g.taggers; }
      else {
        const rec = { src: g.src, intervalMs: g.intervalMs, taggers: g.taggers, handle: null };
        rec.handle = subSources[g.src].start(g, () => subSources[g.src].fire(rec));
        activeSubs.set(key, rec);
      }
    }
  }

  function makeBuiltinEnv() {
    const env = envNew(null);
    const def = (n, v) => envDefine(env, n, v);

    // Booleans / Maybe / Result / Order constructors. Order (LT/EQ/GT)
    // is used by List.sortWith — same convention as Elm.
    def('True',  VBool(true));
    def('False', VBool(false));
    def('Nothing', VCtor('Nothing'));
    def('Just', native(1, args => VCtor('Just', [args[0]])));
    def('Ok',  native(1, args => VCtor('Ok',  [args[0]])));
    def('Err', native(1, args => VCtor('Err', [args[0]])));
    def('LT', VCtor('LT'));
    def('EQ', VCtor('EQ'));
    def('GT', VCtor('GT'));

    // Method constructors — the HTTP verbs passed to Service.declare.
    def('GET', VCtor('GET'));
    def('POST', VCtor('POST'));
    def('PUT', VCtor('PUT'));
    def('PATCH', VCtor('PATCH'));
    def('DELETE', VCtor('DELETE'));

    // Service.Error constructors — the transport failure a Service.call
    // delivers in its Err. serviceCall builds these directly (see
    // serviceErrorFromResponse / serviceErrorOffline). Registered under
    // their qualified names only, the Elm Http.Error model: user code
    // writes `Service.Offline` to construct and to pattern-match, and the
    // bare names stay free for user constructors. Tags stay bare.
    def('Service.Offline', VCtor('Offline'));
    def('Service.Unauthorized', VCtor('Unauthorized'));
    def('Service.ServerError', native(1, args => VCtor('ServerError', [args[0]])));
    def('serviceErrorToString', native(1, ([e]) => VString(serviceErrorToStringJS(e))));
    def('Service.errorToString', envLookup(env, 'serviceErrorToString'));

    // Arithmetic
    def('+', native(2, ([a, b]) => VInt(a.n + b.n)));
    def('-', native(2, ([a, b]) => VInt(a.n - b.n)));
    def('*', native(2, ([a, b]) => VInt(a.n * b.n)));
    def('/', native(2, ([a, b]) => VInt(b.n === 0 ? 0 : Math.trunc(a.n / b.n))));

    // Comparisons
    def('==', native(2, ([a, b]) => VBool(eqValues(a, b))));
    def('/=', native(2, ([a, b]) => VBool(!eqValues(a, b))));
    def('<',  native(2, ([a, b]) => VBool(cmpValues(a, b) <  0)));
    def('>',  native(2, ([a, b]) => VBool(cmpValues(a, b) >  0)));
    def('<=', native(2, ([a, b]) => VBool(cmpValues(a, b) <= 0)));
    def('>=', native(2, ([a, b]) => VBool(cmpValues(a, b) >= 0)));

    // Logic
    def('&&', native(2, ([a, b]) => VBool(a.b && b.b)));
    def('||', native(2, ([a, b]) => VBool(a.b || b.b)));

    // Append (strings + lists)
    def('++', native(2, ([a, b]) => {
      if (a.k === 'S') return VString(a.s + b.s);
      if (a.k === 'L') return VList(a.xs.concat(b.xs));
      throw new Error('++: unsupported');
    }));

    // Cons
    def('::', native(2, ([h, t]) => VList([h].concat(t.xs))));

    // Pipes
    def('|>', native(2, ([x, f]) => apply(f, x)));
    def('<|', native(2, ([f, x]) => apply(f, x)));

    // String stdlib — semantics match the Go runtime (and iOS Swift)
    // exactly. Arg order in particular: needle/prefix/sep first, so
    // pipe-friendly:  `s |> String.contains "foo"`.
    def('stringFromInt', native(1, ([n]) => VString(String(n.n))));
    def('String.fromInt', native(1, ([n]) => VString(String(n.n))));
    def('stringLength', native(1, ([s]) => VInt(s.s.length)));
    def('String.length', native(1, ([s]) => VInt(s.s.length)));

    const stringContainsImpl = native(2, ([needle, hay]) => VBool(hay.s.includes(needle.s)));
    def('stringContains', stringContainsImpl);
    def('String.contains', stringContainsImpl);

    const stringStartsWithImpl = native(2, ([prefix, s]) => VBool(s.s.startsWith(prefix.s)));
    def('stringStartsWith', stringStartsWithImpl);
    def('String.startsWith', stringStartsWithImpl);

    const stringToUpperImpl = native(1, ([s]) => VString(s.s.toUpperCase()));
    def('stringToUpper', stringToUpperImpl);
    def('String.toUpper', stringToUpperImpl);

    const stringToLowerImpl = native(1, ([s]) => VString(s.s.toLowerCase()));
    def('stringToLower', stringToLowerImpl);
    def('String.toLower', stringToLowerImpl);

    // String.split mirrors Go's strings.Split: empty separator yields
    // one element per code unit (not per UTF-16 surrogate pair), since
    // we already use s.length elsewhere — keep behavior consistent.
    const stringSplitImpl = native(2, ([sep, s]) => {
      const parts = sep.s === '' ? Array.from(s.s) : s.s.split(sep.s);
      return VList(parts.map(p => VString(p)));
    });
    def('stringSplit', stringSplitImpl);
    def('String.split', stringSplitImpl);

    const stringJoinImpl = native(2, ([sep, list]) =>
      VString(list.xs.map(e => e.s).join(sep.s)));
    def('stringJoin', stringJoinImpl);
    def('String.join', stringJoinImpl);

    // String.trim — strip leading + trailing whitespace including
    // newlines, matching strings.TrimSpace in Go.
    const stringTrimImpl = native(1, ([s]) => VString(s.s.trim()));
    def('stringTrim', stringTrimImpl);
    def('String.trim', stringTrimImpl);

    // String.endsWith : String suffix -> String s -> Bool
    const stringEndsWithImpl = native(2, ([suf, s]) =>
      VBool(s.s.endsWith(suf.s)));
    def('stringEndsWith', stringEndsWithImpl);
    def('String.endsWith', stringEndsWithImpl);

    // String.toInt : String -> Maybe Int — Number.isInteger after
    // Number(s) rejects floats, NaN, whitespace-only strings (Number
    // would happily return 0 on " " — we don't want that). parseInt
    // would accept "12abc" as 12; Number(...) doesn't, which matches
    // Elm's stricter behavior.
    const stringToIntImpl = native(1, ([s]) => {
      const trimmed = s.s.trim();
      if (trimmed === '') return VCtor('Nothing');
      const n = Number(trimmed);
      if (!Number.isFinite(n) || !Number.isInteger(n)) return VCtor('Nothing');
      return VCtor('Just', [VInt(n)]);
    });
    def('stringToInt', stringToIntImpl);
    def('String.toInt', stringToIntImpl);

    // String.toFloat : String -> Maybe Float
    const stringToFloatImpl = native(1, ([s]) => {
      const trimmed = s.s.trim();
      if (trimmed === '') return VCtor('Nothing');
      const n = Number(trimmed);
      if (!Number.isFinite(n)) return VCtor('Nothing');
      return VCtor('Just', [VFloat(n)]);
    });
    def('stringToFloat', stringToFloatImpl);
    def('String.toFloat', stringToFloatImpl);

    // String.fromFloat — JS's default Number.toString is already
    // shortest-round-trip in modern engines.
    const stringFromFloatImpl = native(1, ([f]) => VString(String(f.n)));
    def('stringFromFloat', stringFromFloatImpl);
    def('String.fromFloat', stringFromFloatImpl);

    // String.replace : String needle -> String replacement -> String s -> String
    // Uses String.prototype.replaceAll (ES2021+). All modern browsers
    // we target support it.
    const stringReplaceImpl = native(3, ([needle, rep, s]) =>
      VString(s.s.split(needle.s).join(rep.s)));
    def('stringReplace', stringReplaceImpl);
    def('String.replace', stringReplaceImpl);

    // String.repeat : Int -> String -> String
    const stringRepeatImpl = native(2, ([n, s]) => {
      if (n.n <= 0) return VString('');
      return VString(s.s.repeat(n.n));
    });
    def('stringRepeat', stringRepeatImpl);
    def('String.repeat', stringRepeatImpl);

    // String.padLeft / padRight — pad with a SINGLE Char (Elm-style).
    // The seed is one code point that we repeat to fill.
    function padString(s, width, padCh, left) {
      const padStr = String.fromCodePoint(padCh);
      if (s.length >= width) return s;
      const need = width - s.length;
      let filler = '';
      while (filler.length < need) filler += padStr;
      filler = filler.slice(0, need);
      return left ? filler + s : s + filler;
    }
    const stringPadLeftImpl = native(3, ([w, pad, s]) =>
      VString(padString(s.s, w.n, pad.c, true)));
    def('stringPadLeft', stringPadLeftImpl);
    def('String.padLeft', stringPadLeftImpl);

    const stringPadRightImpl = native(3, ([w, pad, s]) =>
      VString(padString(s.s, w.n, pad.c, false)));
    def('stringPadRight', stringPadRightImpl);
    def('String.padRight', stringPadRightImpl);

    // String.indexes : String needle -> String s -> List Int — every
    // (non-overlapping) byte offset. Matches Elm's behavior.
    const stringIndexesImpl = native(2, ([needle, s]) => {
      if (needle.s === '') return VList([]);
      const out = [];
      let i = 0;
      while (true) {
        const j = s.s.indexOf(needle.s, i);
        if (j < 0) break;
        out.push(VInt(j));
        i = j + needle.s.length;
      }
      return VList(out);
    });
    def('stringIndexes', stringIndexesImpl);
    def('String.indexes', stringIndexesImpl);

    // List stdlib
    def('listLength', native(1, ([l]) => VInt(l.xs.length)));
    def('List.length', native(1, ([l]) => VInt(l.xs.length)));
    def('listMap', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    def('List.map', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    def('listSum', native(1, ([l]) => VInt(l.xs.reduce((a, x) => a + x.n, 0))));
    def('List.sum', native(1, ([l]) => VInt(l.xs.reduce((a, x) => a + x.n, 0))));
    def('listFilter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));
    def('List.filter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));
    // List.reverse — non-mutating: build a new list rather than
    // calling Array.prototype.reverse (which mutates the underlying
    // array, surprising callers that share the same VList instance).
    const listReverseImpl = native(1, ([l]) => VList(l.xs.slice().reverse()));
    def('listReverse', listReverseImpl);
    def('List.reverse', listReverseImpl);

    // List.foldl : (b -> a -> b) -> b -> List a -> b
    const listFoldlImpl = native(3, ([fn, init, l]) => {
      let acc = init;
      for (const x of l.xs) acc = apply(apply(fn, acc), x);
      return acc;
    });
    def('listFoldl', listFoldlImpl);
    def('List.foldl', listFoldlImpl);

    // List.range : Int -> Int -> List Int (inclusive both ends; empty when from > to)
    const listRangeImpl = native(2, ([from, to]) => {
      const a = from.n, b = to.n;
      if (a > b) return VList([]);
      const xs = new Array(b - a + 1);
      for (let i = 0; i <= b - a; i++) xs[i] = VInt(a + i);
      return VList(xs);
    });
    def('listRange', listRangeImpl);
    def('List.range', listRangeImpl);

    const listHeadImpl = native(1, ([l]) =>
      l.xs.length === 0 ? VCtor('Nothing', []) : VCtor('Just', [l.xs[0]]));
    def('listHead', listHeadImpl);
    def('List.head', listHeadImpl);

    const listTailImpl = native(1, ([l]) =>
      l.xs.length === 0 ? VCtor('Nothing', []) : VCtor('Just', [VList(l.xs.slice(1))]));
    def('listTail', listTailImpl);
    def('List.tail', listTailImpl);

    const listIsEmptyImpl = native(1, ([l]) => VBool(l.xs.length === 0));
    def('listIsEmpty', listIsEmptyImpl);
    def('List.isEmpty', listIsEmptyImpl);

    // listTake / listDrop : Int -> List a -> List a
    const listTakeImpl = native(2, ([n, l]) => {
      const k = n.n;
      if (k <= 0) return VList([]);
      if (k >= l.xs.length) return l;
      return VList(l.xs.slice(0, k));
    });
    def('listTake', listTakeImpl);
    def('List.take', listTakeImpl);

    const listDropImpl = native(2, ([n, l]) => {
      const k = n.n;
      if (k <= 0) return l;
      if (k >= l.xs.length) return VList([]);
      return VList(l.xs.slice(k));
    });
    def('listDrop', listDropImpl);
    def('List.drop', listDropImpl);

    // listMove : Int -> Int -> List a -> List a
    // Pure splice — mirrors the Go impl: defensive no-op on
    // from == to or out-of-bounds indices. Returns a NEW list;
    // never mutates the input. Used by the `list` UI primitive's
    // reorder gesture: the renderer fires onMove(from, to) and the
    // app applies this to its model.
    const listMoveImpl = native(3, ([fromV, toV, l]) => {
      const from = fromV.n;
      const to = toV.n;
      const n = l.xs.length;
      if (from === to || from < 0 || from >= n || to < 0 || to >= n) {
        return l;
      }
      const out = l.xs.slice();
      const [elt] = out.splice(from, 1);
      out.splice(to, 0, elt);
      return VList(out);
    });
    def('listMove', listMoveImpl);
    def('List.move', listMoveImpl);

    // listMember : a -> List a -> Bool — structural equality.
    const listMemberImpl = native(2, ([needle, l]) => {
      for (const e of l.xs) if (eqValues(needle, e)) return VBool(true);
      return VBool(false);
    });
    def('listMember', listMemberImpl);
    def('List.member', listMemberImpl);

    // listAny / listAll — short-circuit.
    const listAnyImpl = native(2, ([fn, l]) => {
      for (const e of l.xs) if (apply(fn, e).b) return VBool(true);
      return VBool(false);
    });
    def('listAny', listAnyImpl);
    def('List.any', listAnyImpl);

    const listAllImpl = native(2, ([fn, l]) => {
      for (const e of l.xs) if (!apply(fn, e).b) return VBool(false);
      return VBool(true);
    });
    def('listAll', listAllImpl);
    def('List.all', listAllImpl);

    // listFoldr : (a -> b -> b) -> b -> List a -> b
    const listFoldrImpl = native(3, ([fn, acc, l]) => {
      for (let i = l.xs.length - 1; i >= 0; i--) {
        acc = apply(apply(fn, l.xs[i]), acc);
      }
      return acc;
    });
    def('listFoldr', listFoldrImpl);
    def('List.foldr', listFoldrImpl);

    // listIndexedMap : (Int -> a -> b) -> List a -> List b
    const listIndexedMapImpl = native(2, ([fn, l]) =>
      VList(l.xs.map((e, i) => apply(apply(fn, VInt(i)), e))));
    def('listIndexedMap', listIndexedMapImpl);
    def('List.indexedMap', listIndexedMapImpl);

    // listRepeat : Int -> a -> List a
    const listRepeatImpl = native(2, ([n, v]) => {
      const k = n.n;
      if (k <= 0) return VList([]);
      const out = new Array(k);
      for (let i = 0; i < k; i++) out[i] = v;
      return VList(out);
    });
    def('listRepeat', listRepeatImpl);
    def('List.repeat', listRepeatImpl);

    // listIntersperse : a -> List a -> List a
    const listIntersperseImpl = native(2, ([sep, l]) => {
      const n = l.xs.length;
      if (n <= 1) return l;
      const out = [];
      for (let i = 0; i < n; i++) {
        if (i > 0) out.push(sep);
        out.push(l.xs[i]);
      }
      return VList(out);
    });
    def('listIntersperse', listIntersperseImpl);
    def('List.intersperse', listIntersperseImpl);

    // listPartition : (a -> Bool) -> List a -> (List a, List a)
    const listPartitionImpl = native(2, ([fn, l]) => {
      const yes = [];
      const no = [];
      for (const e of l.xs) (apply(fn, e).b ? yes : no).push(e);
      return VTuple([VList(yes), VList(no)]);
    });
    def('listPartition', listPartitionImpl);
    def('List.partition', listPartitionImpl);

    // listConcatMap : (a -> List b) -> List a -> List b
    const listConcatMapImpl = native(2, ([fn, l]) => {
      const out = [];
      for (const e of l.xs) {
        const inner = apply(fn, e);
        for (const x of inner.xs) out.push(x);
      }
      return VList(out);
    });
    def('listConcatMap', listConcatMapImpl);
    def('List.concatMap', listConcatMapImpl);

    // listFilterMap : (a -> Maybe b) -> List a -> List b
    const listFilterMapImpl = native(2, ([fn, l]) => {
      const out = [];
      for (const e of l.xs) {
        const v = apply(fn, e);
        if (v && v.k === 'C' && v.tag === 'Just' && v.args.length === 1) {
          out.push(v.args[0]);
        }
      }
      return VList(out);
    });
    def('listFilterMap', listFilterMapImpl);
    def('List.filterMap', listFilterMapImpl);

    // listMaximum / listMinimum : List a -> Maybe a — uses cmpValues
    // which only handles Int/Float/String. Non-comparable element
    // types silently return Nothing rather than throwing.
    function listExtremum(l, want) {
      if (l.xs.length === 0) return VCtor('Nothing');
      let best = l.xs[0];
      for (let i = 1; i < l.xs.length; i++) {
        const c = cmpValues(best, l.xs[i]);
        if ((want === 'max' && c < 0) || (want === 'min' && c > 0)) best = l.xs[i];
      }
      return VCtor('Just', [best]);
    }
    const listMaximumImpl = native(1, ([l]) => listExtremum(l, 'max'));
    def('listMaximum', listMaximumImpl);
    def('List.maximum', listMaximumImpl);
    const listMinimumImpl = native(1, ([l]) => listExtremum(l, 'min'));
    def('listMinimum', listMinimumImpl);
    def('List.minimum', listMinimumImpl);

    // listProduct : List Int -> Int
    const listProductImpl = native(1, ([l]) => {
      let p = 1;
      for (const e of l.xs) p *= e.n;
      return VInt(p);
    });
    def('listProduct', listProductImpl);
    def('List.product', listProductImpl);

    // listSort / listSortBy / listSortWith — Array.prototype.sort is
    // stable in all modern JS engines (ES2019+), so insertion order
    // survives equal keys. Same semantics as Go's sort.SliceStable.
    const listSortImpl = native(1, ([l]) =>
      VList([...l.xs].sort(cmpValues)));
    def('listSort', listSortImpl);
    def('List.sort', listSortImpl);

    const listSortByImpl = native(2, ([fn, l]) => {
      // Cache keys so we don't run `fn` O(n log n) times.
      const keys = l.xs.map(e => apply(fn, e));
      const idx = l.xs.map((_, i) => i);
      idx.sort((a, b) => cmpValues(keys[a], keys[b]));
      return VList(idx.map(i => l.xs[i]));
    });
    def('listSortBy', listSortByImpl);
    def('List.sortBy', listSortByImpl);

    // listSortWith : (a -> a -> Order) -> List a -> List a
    // Comparator returns LT / EQ / GT (a 3-way ADT) — translate to
    // -1/0/1 inside the JS sort callback. `default` covers a comparator
    // that returned something that isn't an Order ctor (typecheck
    // should catch this, but the runtime guard keeps the failure mode
    // localized rather than producing nonsense ordering).
    const listSortWithImpl = native(2, ([fn, l]) =>
      VList([...l.xs].sort((a, b) => {
        const r = apply(apply(fn, a), b);
        if (r.k === 'C') {
          switch (r.tag) {
            case 'LT': return -1;
            case 'EQ': return 0;
            case 'GT': return 1;
          }
        }
        throw new Error('List.sortWith: comparator did not return Order (LT/EQ/GT)');
      })));
    def('listSortWith', listSortWithImpl);
    def('List.sortWith', listSortWithImpl);

    const listConcatImpl = native(1, ([l]) => {
      const out = [];
      for (const inner of l.xs) {
        for (const x of inner.xs) out.push(x);
      }
      return VList(out);
    });
    def('listConcat', listConcatImpl);
    def('List.concat', listConcatImpl);

    // Maybe
    def('maybeWithDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));
    def('Maybe.withDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));

    const maybeMapImpl = native(2, ([fn, m]) =>
      m.tag === 'Just' ? VCtor('Just', [apply(fn, m.args[0])]) : m);
    def('maybeMap', maybeMapImpl);
    def('Maybe.map', maybeMapImpl);

    const maybeAndThenImpl = native(2, ([fn, m]) =>
      m.tag === 'Just' ? apply(fn, m.args[0]) : m);
    def('maybeAndThen', maybeAndThenImpl);
    def('Maybe.andThen', maybeAndThenImpl);

    // Result
    const resultMapImpl = native(2, ([fn, r]) =>
      r.tag === 'Ok' ? VCtor('Ok', [apply(fn, r.args[0])]) : r);
    def('resultMap', resultMapImpl);
    def('Result.map', resultMapImpl);

    const resultAndThenImpl = native(2, ([fn, r]) =>
      r.tag === 'Ok' ? apply(fn, r.args[0]) : r);
    def('resultAndThen', resultAndThenImpl);
    def('Result.andThen', resultAndThenImpl);

    const resultMapErrorImpl = native(2, ([fn, r]) =>
      r.tag === 'Err' ? VCtor('Err', [apply(fn, r.args[0])]) : r);
    def('resultMapError', resultMapErrorImpl);
    def('Result.mapError', resultMapErrorImpl);

    // Result.withDefault : a -> Result e a -> a
    const resultWithDefaultImpl = native(2, ([fallback, r]) =>
      r.tag === 'Ok' && r.args.length === 1 ? r.args[0] : fallback);
    def('resultWithDefault', resultWithDefaultImpl);
    def('Result.withDefault', resultWithDefaultImpl);

    // Result.fromMaybe : err -> Maybe a -> Result err a
    const resultFromMaybeImpl = native(2, ([err, m]) =>
      m.tag === 'Just' && m.args.length === 1
        ? VCtor('Ok', [m.args[0]])
        : VCtor('Err', [err]));
    def('resultFromMaybe', resultFromMaybeImpl);
    def('Result.fromMaybe', resultFromMaybeImpl);

    // Result.toMaybe — discards the error info (matches Elm).
    const resultToMaybeImpl = native(1, ([r]) =>
      r.tag === 'Ok' && r.args.length === 1
        ? VCtor('Just', [r.args[0]])
        : VCtor('Nothing'));
    def('resultToMaybe', resultToMaybeImpl);
    def('Result.toMaybe', resultToMaybeImpl);

    // Maybe.map2 / map3
    const maybeMap2Impl = native(3, ([fn, a, b]) => {
      if (a.tag !== 'Just' || b.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [apply(apply(fn, a.args[0]), b.args[0])]);
    });
    def('maybeMap2', maybeMap2Impl);
    def('Maybe.map2', maybeMap2Impl);

    const maybeMap3Impl = native(4, ([fn, a, b, c]) => {
      if (a.tag !== 'Just' || b.tag !== 'Just' || c.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [apply(apply(apply(fn, a.args[0]), b.args[0]), c.args[0])]);
    });
    def('maybeMap3', maybeMap3Impl);
    def('Maybe.map3', maybeMap3Impl);

    // Maybe.andMap : Maybe a -> Maybe (a -> b) -> Maybe b
    const maybeAndMapImpl = native(2, ([val, fn]) => {
      if (val.tag !== 'Just' || fn.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [apply(fn.args[0], val.args[0])]);
    });
    def('maybeAndMap', maybeAndMapImpl);
    def('Maybe.andMap', maybeAndMapImpl);

    // Maybe.filter : (a -> Bool) -> Maybe a -> Maybe a
    const maybeFilterImpl = native(2, ([fn, m]) => {
      if (m.tag !== 'Just' || m.args.length !== 1) return VCtor('Nothing');
      return apply(fn, m.args[0]).b ? m : VCtor('Nothing');
    });
    def('maybeFilter', maybeFilterImpl);
    def('Maybe.filter', maybeFilterImpl);

    // Tuple — 2-tuple helpers. Tuples are VTuple values with .xs.
    const tupleFirstImpl = native(1, ([t]) => t.xs[0]);
    def('tupleFirst', tupleFirstImpl);
    def('Tuple.first', tupleFirstImpl);

    const tupleSecondImpl = native(1, ([t]) => t.xs[1]);
    def('tupleSecond', tupleSecondImpl);
    def('Tuple.second', tupleSecondImpl);

    const tuplePairImpl = native(2, ([a, b]) => VTuple([a, b]));
    def('tuplePair', tuplePairImpl);
    def('Tuple.pair', tuplePairImpl);

    const tupleMapFirstImpl = native(2, ([fn, t]) =>
      VTuple([apply(fn, t.xs[0]), t.xs[1]]));
    def('tupleMapFirst', tupleMapFirstImpl);
    def('Tuple.mapFirst', tupleMapFirstImpl);

    const tupleMapSecondImpl = native(2, ([fn, t]) =>
      VTuple([t.xs[0], apply(fn, t.xs[1])]));
    def('tupleMapSecond', tupleMapSecondImpl);
    def('Tuple.mapSecond', tupleMapSecondImpl);

    const tupleMapBothImpl = native(3, ([fnA, fnB, t]) =>
      VTuple([apply(fnA, t.xs[0]), apply(fnB, t.xs[1])]));
    def('tupleMapBoth', tupleMapBothImpl);
    def('Tuple.mapBoth', tupleMapBothImpl);

    // ---------- Dict ----------
    //
    // Elm-style polymorphic ordered map (sorted by key). Same wire
    // format and same comparable-key constraint as the Go and Swift
    // runtimes. Pairs slice IS the canonical representation — every
    // mutation rebuilds it sorted via dictInsertHelper / etc.
    def('dictEmpty', VDict([]));
    def('Dict.empty', VDict([]));

    const dictSingletonImpl = native(2, ([k, v]) => VDict([{ key: k, value: v }]));
    def('dictSingleton', dictSingletonImpl);
    def('Dict.singleton', dictSingletonImpl);

    const dictInsertImpl = native(3, ([k, v, d]) => dictInsertHelper(d, k, v));
    def('dictInsert', dictInsertImpl);
    def('Dict.insert', dictInsertImpl);

    const dictUpdateImpl = native(3, ([k, fn, d]) => {
      const { idx, found } = dictSearch(d, k);
      const current = found
        ? VCtor('Just', [d.pairs[idx].value])
        : VCtor('Nothing');
      const next = apply(fn, current);
      if (next.tag === 'Nothing') {
        return found ? dictRemoveHelper(d, idx) : d;
      }
      if (next.tag === 'Just') {
        return dictInsertHelper(d, k, next.args[0]);
      }
      throw new Error('Dict.update: function did not return a Maybe');
    });
    def('dictUpdate', dictUpdateImpl);
    def('Dict.update', dictUpdateImpl);

    const dictRemoveImpl = native(2, ([k, d]) => {
      const { idx, found } = dictSearch(d, k);
      return found ? dictRemoveHelper(d, idx) : d;
    });
    def('dictRemove', dictRemoveImpl);
    def('Dict.remove', dictRemoveImpl);

    const dictIsEmptyImpl = native(1, ([d]) => VBool(d.pairs.length === 0));
    def('dictIsEmpty', dictIsEmptyImpl);
    def('Dict.isEmpty', dictIsEmptyImpl);

    const dictMemberImpl = native(2, ([k, d]) => VBool(dictSearch(d, k).found));
    def('dictMember', dictMemberImpl);
    def('Dict.member', dictMemberImpl);

    const dictGetImpl = native(2, ([k, d]) => {
      const { idx, found } = dictSearch(d, k);
      return found ? VCtor('Just', [d.pairs[idx].value]) : VCtor('Nothing');
    });
    def('dictGet', dictGetImpl);
    def('Dict.get', dictGetImpl);

    const dictSizeImpl = native(1, ([d]) => VInt(d.pairs.length));
    def('dictSize', dictSizeImpl);
    def('Dict.size', dictSizeImpl);

    const dictKeysImpl = native(1, ([d]) => VList(d.pairs.map(p => p.key)));
    def('dictKeys', dictKeysImpl);
    def('Dict.keys', dictKeysImpl);

    const dictValuesImpl = native(1, ([d]) => VList(d.pairs.map(p => p.value)));
    def('dictValues', dictValuesImpl);
    def('Dict.values', dictValuesImpl);

    const dictToListImpl = native(1, ([d]) =>
      VList(d.pairs.map(p => VTuple([p.key, p.value]))));
    def('dictToList', dictToListImpl);
    def('Dict.toList', dictToListImpl);

    const dictFromListImpl = native(1, ([l]) => {
      let d = VDict([]);
      for (const e of l.xs) {
        d = dictInsertHelper(d, e.xs[0], e.xs[1]);
      }
      return d;
    });
    def('dictFromList', dictFromListImpl);
    def('Dict.fromList', dictFromListImpl);

    // Dict.map : (k -> v -> w) -> Dict k v -> Dict k w  — keys
    // untouched so we keep the pair order without re-sorting.
    const dictMapImpl = native(2, ([fn, d]) =>
      VDict(d.pairs.map(p => ({ key: p.key, value: apply(apply(fn, p.key), p.value) }))));
    def('dictMap', dictMapImpl);
    def('Dict.map', dictMapImpl);

    const dictFoldlImpl = native(3, ([fn, init, d]) => {
      let acc = init;
      for (const p of d.pairs) acc = apply(apply(apply(fn, p.key), p.value), acc);
      return acc;
    });
    def('dictFoldl', dictFoldlImpl);
    def('Dict.foldl', dictFoldlImpl);

    const dictFoldrImpl = native(3, ([fn, init, d]) => {
      let acc = init;
      for (let i = d.pairs.length - 1; i >= 0; i--) {
        const p = d.pairs[i];
        acc = apply(apply(apply(fn, p.key), p.value), acc);
      }
      return acc;
    });
    def('dictFoldr', dictFoldrImpl);
    def('Dict.foldr', dictFoldrImpl);

    const dictFilterImpl = native(2, ([fn, d]) =>
      VDict(d.pairs.filter(p => apply(apply(fn, p.key), p.value).b)));
    def('dictFilter', dictFilterImpl);
    def('Dict.filter', dictFilterImpl);

    const dictPartitionImpl = native(2, ([fn, d]) => {
      const yes = [], no = [];
      for (const p of d.pairs) {
        if (apply(apply(fn, p.key), p.value).b) yes.push(p);
        else no.push(p);
      }
      return VTuple([VDict(yes), VDict(no)]);
    });
    def('dictPartition', dictPartitionImpl);
    def('Dict.partition', dictPartitionImpl);

    // Dict.union — left-biased: collision keeps `a`'s value.
    const dictUnionImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length && j < b.pairs.length) {
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) { out.push(a.pairs[i]); i++; }
        else if (c > 0) { out.push(b.pairs[j]); j++; }
        else { out.push(a.pairs[i]); i++; j++; }
      }
      while (i < a.pairs.length) out.push(a.pairs[i++]);
      while (j < b.pairs.length) out.push(b.pairs[j++]);
      return VDict(out);
    });
    def('dictUnion', dictUnionImpl);
    def('Dict.union', dictUnionImpl);

    const dictIntersectImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length && j < b.pairs.length) {
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) i++;
        else if (c > 0) j++;
        else { out.push(a.pairs[i]); i++; j++; }
      }
      return VDict(out);
    });
    def('dictIntersect', dictIntersectImpl);
    def('Dict.intersect', dictIntersectImpl);

    const dictDiffImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length) {
        if (j >= b.pairs.length) { out.push(...a.pairs.slice(i)); break; }
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) { out.push(a.pairs[i]); i++; }
        else if (c > 0) { j++; }
        else { i++; j++; }
      }
      return VDict(out);
    });
    def('dictDiff', dictDiffImpl);
    def('Dict.diff', dictDiffImpl);

    // ---------- Set ----------
    def('setEmpty', VSet([]));
    def('Set.empty', VSet([]));

    const setSingletonImpl = native(1, ([k]) => VSet([k]));
    def('setSingleton', setSingletonImpl);
    def('Set.singleton', setSingletonImpl);

    const setInsertImpl = native(2, ([k, s]) => setInsertHelper(s, k));
    def('setInsert', setInsertImpl);
    def('Set.insert', setInsertImpl);

    const setRemoveImpl = native(2, ([k, s]) => {
      const { idx, found } = setSearch(s, k);
      if (!found) return s;
      const items = s.items.slice();
      items.splice(idx, 1);
      return VSet(items);
    });
    def('setRemove', setRemoveImpl);
    def('Set.remove', setRemoveImpl);

    const setIsEmptyImpl = native(1, ([s]) => VBool(s.items.length === 0));
    def('setIsEmpty', setIsEmptyImpl);
    def('Set.isEmpty', setIsEmptyImpl);

    const setMemberImpl = native(2, ([k, s]) => VBool(setSearch(s, k).found));
    def('setMember', setMemberImpl);
    def('Set.member', setMemberImpl);

    const setSizeImpl = native(1, ([s]) => VInt(s.items.length));
    def('setSize', setSizeImpl);
    def('Set.size', setSizeImpl);

    const setToListImpl = native(1, ([s]) => VList(s.items.slice()));
    def('setToList', setToListImpl);
    def('Set.toList', setToListImpl);

    const setFromListImpl = native(1, ([l]) => {
      let s = VSet([]);
      for (const e of l.xs) s = setInsertHelper(s, e);
      return s;
    });
    def('setFromList', setFromListImpl);
    def('Set.fromList', setFromListImpl);

    // Set.map can change the element type, so we re-sort/dedupe via
    // setInsertHelper rather than copy in place.
    const setMapImpl = native(2, ([fn, s]) => {
      let out = VSet([]);
      for (const it of s.items) out = setInsertHelper(out, apply(fn, it));
      return out;
    });
    def('setMap', setMapImpl);
    def('Set.map', setMapImpl);

    const setFoldlImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (const it of s.items) acc = apply(apply(fn, it), acc);
      return acc;
    });
    def('setFoldl', setFoldlImpl);
    def('Set.foldl', setFoldlImpl);

    const setFoldrImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (let i = s.items.length - 1; i >= 0; i--) acc = apply(apply(fn, s.items[i]), acc);
      return acc;
    });
    def('setFoldr', setFoldrImpl);
    def('Set.foldr', setFoldrImpl);

    const setFilterImpl = native(2, ([fn, s]) =>
      VSet(s.items.filter(it => apply(fn, it).b)));
    def('setFilter', setFilterImpl);
    def('Set.filter', setFilterImpl);

    const setPartitionImpl = native(2, ([fn, s]) => {
      const yes = [], no = [];
      for (const it of s.items) (apply(fn, it).b ? yes : no).push(it);
      return VTuple([VSet(yes), VSet(no)]);
    });
    def('setPartition', setPartitionImpl);
    def('Set.partition', setPartitionImpl);

    const setUnionImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length && j < b.items.length) {
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) { out.push(a.items[i++]); }
        else if (c > 0) { out.push(b.items[j++]); }
        else { out.push(a.items[i++]); j++; }
      }
      while (i < a.items.length) out.push(a.items[i++]);
      while (j < b.items.length) out.push(b.items[j++]);
      return VSet(out);
    });
    def('setUnion', setUnionImpl);
    def('Set.union', setUnionImpl);

    const setIntersectImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length && j < b.items.length) {
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) i++;
        else if (c > 0) j++;
        else { out.push(a.items[i++]); j++; }
      }
      return VSet(out);
    });
    def('setIntersect', setIntersectImpl);
    def('Set.intersect', setIntersectImpl);

    const setDiffImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length) {
        if (j >= b.items.length) { out.push(...a.items.slice(i)); break; }
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) { out.push(a.items[i++]); }
        else if (c > 0) j++;
        else { i++; j++; }
      }
      return VSet(out);
    });
    def('setDiff', setDiffImpl);
    def('Set.diff', setDiffImpl);

    // ---------- Char ----------
    //
    // Char in Mar is a Unicode code point — same model as Go's rune,
    // Swift's Unicode.Scalar, Elm's Char. `c` is the integer code
    // point on VChar.
    //
    // sanitizeCodePoint mirrors the Go side: out-of-range or surrogate
    // inputs collapse to U+FFFD ("replacement character"), keeping the
    // three runtimes able to represent every Char.fromCode result.
    function sanitizeCodePoint(n) {
      if (n < 0 || n > 0x10FFFF) return 0xFFFD;
      if (n >= 0xD800 && n <= 0xDFFF) return 0xFFFD;
      return n;
    }

    const charToCodeImpl = native(1, ([c]) => VInt(c.c));
    def('charToCode', charToCodeImpl);
    def('Char.toCode', charToCodeImpl);

    const charFromCodeImpl = native(1, ([n]) => VChar(sanitizeCodePoint(n.n)));
    def('charFromCode', charFromCodeImpl);
    def('Char.fromCode', charFromCodeImpl);

    // Char predicates — operate on the Unicode properties of the
    // code point. We use JS regex with the `u` flag so things like
    // `Char.isAlpha 'é'` work (and stay aligned with Go's unicode
    // package, which is also Unicode-aware not ASCII-only).
    const isDigitRe = /\p{Nd}/u;
    const isAlphaRe = /\p{L}/u;
    const isUpperRe = /\p{Lu}/u;
    const isLowerRe = /\p{Ll}/u;

    const charIsDigitImpl = native(1, ([c]) =>
      VBool(isDigitRe.test(String.fromCodePoint(c.c))));
    def('charIsDigit', charIsDigitImpl);
    def('Char.isDigit', charIsDigitImpl);

    const charIsAlphaImpl = native(1, ([c]) =>
      VBool(isAlphaRe.test(String.fromCodePoint(c.c))));
    def('charIsAlpha', charIsAlphaImpl);
    def('Char.isAlpha', charIsAlphaImpl);

    const charIsUpperImpl = native(1, ([c]) =>
      VBool(isUpperRe.test(String.fromCodePoint(c.c))));
    def('charIsUpper', charIsUpperImpl);
    def('Char.isUpper', charIsUpperImpl);

    const charIsLowerImpl = native(1, ([c]) =>
      VBool(isLowerRe.test(String.fromCodePoint(c.c))));
    def('charIsLower', charIsLowerImpl);
    def('Char.isLower', charIsLowerImpl);

    // toUpper / toLower — JS `.toUpperCase()` / `.toLowerCase()` on a
    // 1-char string. Take the first code point of the result to stay
    // in Char (e.g. 'ß'.toUpperCase() = "SS" in some locales —
    // unlikely with the default locale, but we take the first scalar
    // defensively to keep the type).
    const charToUpperImpl = native(1, ([c]) => {
      const up = String.fromCodePoint(c.c).toUpperCase();
      return VChar(up.codePointAt(0));
    });
    def('charToUpper', charToUpperImpl);
    def('Char.toUpper', charToUpperImpl);

    const charToLowerImpl = native(1, ([c]) => {
      const lo = String.fromCodePoint(c.c).toLowerCase();
      return VChar(lo.codePointAt(0));
    });
    def('charToLower', charToLowerImpl);
    def('Char.toLower', charToLowerImpl);

    // String <-> [Char] bridges. Iterating a JS string with for..of
    // (or spread) walks Unicode code points correctly (joins
    // surrogate pairs into one scalar). Same semantics as Go's
    // `for _, r := range s`.
    const stringToListImpl = native(1, ([s]) => {
      const out = [];
      for (const ch of s.s) out.push(VChar(ch.codePointAt(0)));
      return VList(out);
    });
    def('stringToList', stringToListImpl);
    def('String.toList', stringToListImpl);

    const stringFromListImpl = native(1, ([l]) => {
      let out = '';
      for (const c of l.xs) out += String.fromCodePoint(c.c);
      return VString(out);
    });
    def('stringFromList', stringFromListImpl);
    def('String.fromList', stringFromListImpl);

    const stringConsImpl = native(2, ([c, s]) =>
      VString(String.fromCodePoint(c.c) + s.s));
    def('stringCons', stringConsImpl);
    def('String.cons', stringConsImpl);

    // String higher-order ops over Char. Iterating with `for..of`
    // walks code points correctly (joins surrogate pairs into one
    // scalar). Matches the Go side's `for _, r := range s`.
    const stringMapImpl = native(2, ([fn, s]) => {
      let out = '';
      for (const ch of s.s) {
        const r = apply(fn, VChar(ch.codePointAt(0)));
        out += String.fromCodePoint(r.c);
      }
      return VString(out);
    });
    def('stringMap', stringMapImpl);
    def('String.map', stringMapImpl);

    const stringFilterImpl = native(2, ([fn, s]) => {
      let out = '';
      for (const ch of s.s) {
        if (apply(fn, VChar(ch.codePointAt(0))).b) out += ch;
      }
      return VString(out);
    });
    def('stringFilter', stringFilterImpl);
    def('String.filter', stringFilterImpl);

    // stringFoldl : (Char -> b -> b) -> b -> String -> b
    const stringFoldlImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (const ch of s.s) {
        acc = apply(apply(fn, VChar(ch.codePointAt(0))), acc);
      }
      return acc;
    });
    def('stringFoldl', stringFoldlImpl);
    def('String.foldl', stringFoldlImpl);

    // stringAny : (Char -> Bool) -> String -> Bool — short-circuit.
    const stringAnyImpl = native(2, ([fn, s]) => {
      for (const ch of s.s) {
        if (apply(fn, VChar(ch.codePointAt(0))).b) return VBool(true);
      }
      return VBool(false);
    });
    def('stringAny', stringAnyImpl);
    def('String.any', stringAnyImpl);

    // ---------- UI primitives — shared infrastructure ----------
    //
    // collectAttrs / makeAttr / flagAttr are used by every UI builtin
    // that takes an `[Attr]` list (containers, textField) and by the
    // input-kind / submit constants.

    function collectAttrs(attrsList) {
      const out = [];
      for (const a of attrsList.xs) {
        out.push({ name: a.fields.name.s, value: a.fields.value });
      }
      return out;
    }
    function makeAttr(name, value) {
      return VRecord({ name: VString(name), value }, ['name', 'value']);
    }
    function flagAttr(name) {
      return makeAttr(name, VUnit());
    }

    // submit / email / password / newPassword / numeric / oneTimeCode
    // are reached via UI.* qualified aliases (see the `UI.email` block
    // below). The bare runtime names start with `view` for historical
    // reasons; renaming them all would invalidate compiled program.json
    // wire payloads in flight. Keep as-is.
    const submitAttrCtor = native(1, ([msg]) => makeAttr('submit', msg));
    def('viewSubmit',       submitAttrCtor);
    def('viewEmail',        flagAttr('inputKindEmail'));
    def('viewPassword',     flagAttr('inputKindPassword'));
    def('viewNewPassword',  flagAttr('inputKindNewPassword'));
    def('viewNumeric',      flagAttr('inputKindNumeric'));
    def('viewOneTimeCode',  flagAttr('inputKindOneTimeCode'));

    // ---------- UI.* (SwiftUI-style declarative vocabulary) ----------
    //
    // Mirror of the Go runtime's UI builtins (internal/runtime/view.go).
    // Same VView / VAttr shapes — just a JS expression of the same
    // intermediate form. The renderer (createDOM further down) is the
    // authoritative interpreter; these builtins produce the data.

    // Containers
    function uiContainer(tag) {
      return native(2, ([attrsList, children]) =>
        VView(tag, collectAttrs(attrsList), children.xs, ''));
    }
    function uiContentOnly(tag) {
      return native(1, ([children]) =>
        VView(tag, [], children.xs, ''));
    }
    def('navigationStack', uiContainer('navigationStack'));
    def('UI.navigationStack', uiContainer('navigationStack'));
    def('form',  uiContentOnly('form'));   def('UI.form',    uiContentOnly('form'));
    def('list',  uiContainer('uiList'));   def('UI.list',    uiContainer('uiList'));
    def('uiSection', uiContainer('uiSection')); def('UI.section', uiContainer('uiSection'));
    def('uiKeyedList', uiContainer('uiKeyedList')); def('UI.keyedList', uiContainer('uiKeyedList'));
    def('hstack', uiContainer('hstack'));  def('UI.hstack',  uiContainer('hstack'));
    def('vstack', uiContainer('vstack'));  def('UI.vstack',  uiContainer('vstack'));

    // textField : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
    function uiTextField(args) {
      const [attrsList, placeholder, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'placeholder', value: placeholder });
      return VView('textField', attrs, [], value.s, onChange);
    }
    def('textField', native(4, uiTextField));
    def('UI.textField', native(4, uiTextField));

    // textArea : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
    // Multi-line text input. Same shape as textField — swapping
    // `textField` for `textArea` in user code is the only change
    // needed when the field needs to hold a paragraph instead of
    // a single line. iOS renders a TextEditor; web renders a
    // <textarea> with the same focus / submit / placeholder
    // styling so the two stack visually inside a section.
    //
    // Submit semantics on the web mirror SwiftUI: a bare Enter
    // inserts a newline (so users can write prose freely), and
    // Cmd/Ctrl+Enter fires the `submit` attr if one is attached.
    // The branching for that lives in attachSubmitDispatcher.
    function uiTextArea(args) {
      const [attrsList, placeholder, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'placeholder', value: placeholder });
      return VView('textArea', attrs, [], value.s, onChange);
    }
    def('textArea', native(4, uiTextArea));
    def('UI.textArea', native(4, uiTextArea));

    // picker : List Attr -> a -> List a -> (a -> String) -> (a -> msg) -> View msg
    // Single-selection field. Use when an enum has more than a
    // couple of variants and rendering one toggle per variant
    // would dominate the form's vertical real estate (priority,
    // assignee, milestone). iOS renders SwiftUI's Picker with the
    // platform's native menu / wheel; web renders a styled
    // <select>. `toLabel` is applied per option to produce the
    // displayed string — same shape user code already has for
    // status / priority badges (`Shared.priorityLabel`,
    // `Shared.statusLabel`).
    //
    // The selected value is identified structurally (eqValues),
    // so callers pass any ctor / int / string / record — whatever
    // the option list contains.
    function uiPicker(args) {
      const [attrsList, selected, options, toLabel, onChange] = args;
      const attrs = collectAttrs(attrsList);
      // Stash the selected value, the option list, and the label
      // function as attrs so createDOM/patchDOM can rebuild the
      // <select> from `view.attrs` alone. msg carries onChange so
      // the dispatcher path mirrors textField / toggle (apply
      // view.msg to the selected option).
      attrs.push({ name: 'selected', value: selected });
      attrs.push({ name: 'options',  value: options });
      attrs.push({ name: 'toLabel',  value: toLabel });
      return VView('picker', attrs, [], '', onChange);
    }
    def('picker', native(5, uiPicker));
    def('UI.picker', native(5, uiPicker));

    // datePicker : List Attr -> Maybe Time -> (Time -> msg) -> View msg
    // Date-only field. Stash the Maybe Time value as an attr so
    // createDOM / patchDOM can render the <input type="date"> from
    // view.attrs alone (Nothing -> today); msg carries onChange,
    // applied to the VTime parsed back from the input.
    function uiDatePicker(args) {
      const [attrsList, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'value', value: value });
      return VView('datePicker', attrs, [], '', onChange);
    }
    def('datePicker', native(3, uiDatePicker));
    def('UI.datePicker', native(3, uiDatePicker));

    // text — plain text leaf. The attrs list carries the universal
    // layout attrs (width / height); `text [width fill] "..."` is
    // the equal-columns idiom.
    const uiTextCtor = native(2, ([attrsList, s]) =>
      VView('text', collectAttrs(attrsList), [], s.s));
    def('uiText', uiTextCtor); def('UI.text', uiTextCtor);

    // paragraph : List (Inline msg) -> View msg
    // Block of flowing inline text. Children are `span` VViews
    // produced by uiSpan; renderer flows them into one <p>.
    const uiParagraphCtor = native(1, ([children]) =>
      VView('paragraph', [], children.xs, ''));
    def('uiParagraph', uiParagraphCtor); def('UI.paragraph', uiParagraphCtor);

    // span : List (Attr Inline) -> String -> Inline msg
    // Inline text run with styling attrs (bold/italic/code/link
    // composing freely).
    const uiSpanCtor = native(2, ([attrsList, s]) =>
      VView('span', collectAttrs(attrsList), [], s.s));
    def('uiSpan', uiSpanCtor); def('UI.span', uiSpanCtor);

    // Inline attrs. Bare style markers (bold/italic/strikethrough/
    // code) carry no payload; the renderer checks attr name to
    // toggle the corresponding CSS class. `link` is the one
    // parameterized inline attr — its payload is the destination URL.
    const inlineBoldAttr          = flagAttr('inlineBold');
    const inlineItalicAttr        = flagAttr('inlineItalic');
    const inlineStrikethroughAttr = flagAttr('inlineStrikethrough');
    const inlineCodeAttr          = flagAttr('inlineCode');
    def('inlineBold', inlineBoldAttr);          def('UI.bold', inlineBoldAttr);
    def('inlineItalic', inlineItalicAttr);      def('UI.italic', inlineItalicAttr);
    def('inlineStrikethrough', inlineStrikethroughAttr);
    def('UI.strikethrough', inlineStrikethroughAttr);
    def('inlineCode', inlineCodeAttr);          def('UI.code', inlineCodeAttr);
    const inlineLinkCtor = native(1, ([url]) => makeAttr('inlineLink', url));
    def('inlineLink', inlineLinkCtor);          def('UI.link', inlineLinkCtor);

    // button : List Attr -> msg -> String -> View msg
    // Attrs carry modifiers like `disabled` that the renderer reads
    // to grey-out the button and skip dispatch.
    const uiButtonCtor = native(3, ([attrsList, msg, label]) =>
      VView('button', collectAttrs(attrsList), [], label.s, msg));
    def('uiButton', uiButtonCtor); def('UI.button', uiButtonCtor);

    // disabled : Bool -> Attr — kept symmetric for both true/false so
    // user code can pass a derived Bool without conditionally building
    // the attrs list.
    const uiDisabledCtor = native(1, ([b]) => makeAttr('disabled', b));
    def('uiDisabled', uiDisabledCtor); def('UI.disabled', uiDisabledCtor);

    // keyed : String -> View msg -> KeyedView msg
    // Wraps a regular View in a stable identity (the key string) so
    // it can be a child of UI.keyedList. The KeyedView distinction
    // is compile-time only — at runtime we just append a `key` attr
    // to the inner VView. The reconciler reads that attr when
    // matching old/new children inside a keyedList.
    //
    // Returns a shallow copy with attrs extended (not the same VView
    // mutated) so callers that hold a reference to the unwrapped
    // view don\'t see their attrs grow as a side effect.
    const uiKeyedCtor = native(2, ([keyStr, view]) => {
      const attrs = view.attrs.slice();
      attrs.push({ name: 'key', value: keyStr });
      return VView(view.tag, attrs, view.children, view.text, view.msg);
    });
    def('uiKeyed', uiKeyedCtor); def('UI.keyed', uiKeyedCtor);

    // onMove : Bool -> (Int -> Int -> msg) -> Attr KeyedList
    // Reorder gesture handler for `list`. The Bool toggles whether
    // drag affordances render (handle + tabindex + ARIA wiring);
    // the function fires with (fromIdx, toIdx) once the user drops.
    //
    // Packs both into a single record value on the attr so the
    // renderer's `list` handler can pull editing + handler off in
    // one place. Storing them separately would make it easier to
    // forget one when wiring the drag listeners.
    const uiOnMoveCtor = native(2, ([editingV, handler]) => {
      const editing = editingV && editingV.b === true;
      return makeAttr('onMove', { editing, handler });
    });
    def('uiOnMove', uiOnMoveCtor); def('UI.onMove', uiOnMoveCtor);

    // onDelete : Bool -> (Int -> msg) -> Attr Section
    // Per-row delete affordance. Bool = "editing mode is currently
    // on"; in that state, every row shows a permanent red `−` on
    // the left (iOS-edit-mode style). When false, the affordance
    // reveals on hover instead (iCloud-Web style — see
    // docs/cli-surface-proposal.md for the design rationale).
    //
    // The handler receives the index of the deleted row. The app is
    // expected to remove that index from its model (and typically
    // fire a Service.call to persist), then optionally surface a
    // toast with an Undo affordance.
    //
    // Same packing shape as onMove so renderers can pick both off in
    // one place.
    const uiOnDeleteCtor = native(2, ([editingV, handler]) => {
      const editing = editingV && editingV.b === true;
      return makeAttr('onDelete', { editing, handler });
    });
    def('uiOnDelete', uiOnDeleteCtor); def('UI.onDelete', uiOnDeleteCtor);

    // title / subtitle — reuse the existing "title" / "subtitle"
    // tags so createDOM's existing handling applies; UI styles
    // (font-size / weight / color) live in ensureUIStyles().
    const uiTitleCtor = native(1, ([s]) => VView('title', [], [], s.s));
    def('uiTitle', uiTitleCtor); def('UI.title', uiTitleCtor);
    const uiSubtitleCtor = native(1, ([s]) => VView('subtitle', [], [], s.s));
    def('uiSubtitle', uiSubtitleCtor); def('UI.subtitle', uiSubtitleCtor);

    // errorText — same leaf shape as text/title/subtitle. Tag
    // 'errorText' triggers the .mar-error-text CSS class in
    // createDOM (red + semi-bold) and a role=alert for assistive
    // tech. Mirrors Go's runtime/view.go uiErrorText + iOS's
    // MarRenderer "errorText" case.
    const uiErrorTextCtor = native(1, ([s]) => VView('errorText', [], [], s.s));
    def('uiErrorText', uiErrorTextCtor); def('UI.errorText', uiErrorTextCtor);

    // image : List (Attr Image) -> { src, alt } -> View msg
    // Emits an "image" tag carrying src + alt (and any size/fit/fill
    // attrs) so createDOM can build an <img>. Mirrors Go runtime's
    // uiImage + iOS MarRenderer "image" case.
    const uiImageCtor = native(2, ([attrsList, rec]) => {
      const attrs = collectAttrs(attrsList);
      const src = rec.fields.src ? rec.fields.src.s : '';
      const alt = rec.fields.alt ? rec.fields.alt.s : '';
      attrs.push({ name: 'src', value: VString(src) });
      attrs.push({ name: 'alt', value: VString(alt) });
      return VView('image', attrs, [], '');
    });
    def('uiImage', uiImageCtor); def('UI.image', uiImageCtor);

    // chars / lines / fill — sizing values. chars/lines wrap an Int
    // in a record tagged with __unit so the renderer can dispatch on
    // what the number means (horizontal characters vs vertical
    // lines); fill is the axis-polymorphic "take the available
    // space" constant (same record shape, amount unused). Mirrors Go
    // runtime's lengthValue helper.
    const uiCharsCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('chars'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiChars', uiCharsCtor); def('UI.chars', uiCharsCtor);
    const uiLinesCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('lines'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiLines', uiLinesCtor); def('UI.lines', uiLinesCtor);
    const uiFillVal =
      VRecord({ __unit: VString('fill'), amount: VInt(0) }, ['__unit', 'amount']);
    def('uiFill', uiFillVal); def('UI.fill', uiFillVal);

    // width / height — the universal sizing attrs. applyLayoutAttrs
    // reads the Size record's __unit: chars/lines size the content
    // box (inputs keep their special-cased max-width / rows
    // handling in applySizing), fill claims the free space on that
    // axis via the contextual .mar-w-fill / .mar-h-fill classes.
    const uiWidthCtor  = native(1, ([v]) => makeAttr('width',  v));
    def('uiWidth',  uiWidthCtor);  def('UI.width',  uiWidthCtor);
    const uiHeightCtor = native(1, ([v]) => makeAttr('height', v));
    def('uiHeight', uiHeightCtor); def('UI.height', uiHeightCtor);

    // align — cross-axis alignment for a stack's hugging children.
    // The value is a plain alignment-name string; applyAlignAttr
    // maps it onto align-items, honoring only the axis that matches
    // the stack (vstack: leading/center/trailing; hstack:
    // top/center/bottom). Children with the matching `fill` have no
    // cross-axis slack, so align never moves them — align is
    // position, fill is size.
    const uiAlignCtor = native(1, ([v]) => makeAttr('align', v));
    def('uiAlign', uiAlignCtor); def('UI.align', uiAlignCtor);
    def('uiLeading', VString('leading'));   def('UI.leading', VString('leading'));
    def('uiCenter', VString('center'));     def('UI.center', VString('center'));
    def('uiTrailing', VString('trailing')); def('UI.trailing', VString('trailing'));
    def('uiTop', VString('top'));           def('UI.top', VString('top'));
    def('uiBottom', VString('bottom'));     def('UI.bottom', VString('bottom'));

    // px — pixel sizing unit for images (mirrors chars/lines, tagged
    // 'px'). size — fixed width+height attr for an image. fit/cover —
    // content-mode flags (CSS object-fit vocabulary; "cover", not
    // "fill", which is the sizing value above). createDOM's 'image'
    // case reads these.
    const uiPxCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('px'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiPx', uiPxCtor); def('UI.px', uiPxCtor);
    const uiSizeCtor = native(2, ([w, h]) =>
      makeAttr('size', VRecord({ w, h }, ['w', 'h'])));
    def('uiSize', uiSizeCtor); def('UI.size', uiSizeCtor);
    const fitAttr = flagAttr('contentModeFit');
    def('uiFit', fitAttr); def('UI.fit', fitAttr);
    const coverAttr = flagAttr('contentModeCover');
    def('uiCover', coverAttr); def('UI.cover', coverAttr);

    // navigationLink : List Attr -> Path r -> r -> View msg -> View msg
    // Mirror of SwiftUI's NavigationLink. The Path + record build
    // the destination URL via the typed-path machinery; the
    // child View becomes the tappable label. Renders as
    // <a class="mar-navigation-link"> wrapping the child DOM.
    // The leading attrs list carries `disabled` (and future
    // modifiers) — uniform shape with every other interactive
    // primitive.
    def('uiNavigationLink', native(4, ([attrsList, pathV, params, child]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('UI.navigationLink: expected Path, got ' + (pathV && pathV.k));
      }
      if (!child || child.k !== 'V') {
        throw new Error('UI.navigationLink: expected View label, got ' + (child && child.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'href', value: VString(url) });
      return VView('navigationLink', attrs, [child], '');
    }));
    def('UI.navigationLink', envLookup(env, 'uiNavigationLink'));

    // empty — no-op placeholder; same VView the existing renderer
    // already knows to handle (display: none).
    def('uiEmpty', VView('empty', [], [], ''));
    def('UI.empty', VView('empty', [], [], ''));

    // spacer — SwiftUI's `Spacer()`. Expands along the containing
    // stack's main axis to push siblings apart. On web that's a
    // `flex: 1` div the parent flex container absorbs.
    def('uiSpacer', VView('spacer', [], [], ''));
    def('UI.spacer', VView('spacer', [], [], ''));

    // toggle : List Attr -> String -> Bool -> (Bool -> msg) -> View msg
    // Mirror of SwiftUI's Toggle(label, isOn: $value). Current
    // state lives in the `isOn` attr, label in text, the
    // `Bool -> msg` callback in msg. createDOM renders a label
    // wrapping an iOS-style styled checkbox; on change the
    // checkbox dispatches `msg(newValue)`. The leading attrs
    // list carries modifiers like `disabled` — same shape every
    // other interactive primitive uses (textField / button /
    // picker), so the gating idiom is uniform.
    def('uiToggle', native(4, ([attrsList, label, isOn, onChange]) => {
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'isOn', value: VBool(!!isOn.b) });
      return VView('toggle', attrs, [], label.s, onChange);
    }));
    def('UI.toggle', envLookup(env, 'uiToggle'));

    // centered : View msg -> View msg
    // Wraps child in a "centered" view tag — pure two-axis
    // alignment. The renderer fills the space the PARENT provides
    // (never inventing a size) and centers the child in it.
    const uiCenteredCtor = native(1, ([child]) =>
      VView('centered', [], [child], ''));
    def('uiCentered', uiCenteredCtor); def('UI.centered', uiCenteredCtor);

    // sheet : { open, onDismiss, outlet } -> List (View msg) -> View msg
    //
    // iOS-style page sheet. Parent owns open/closed state; framework
    // renders the overlay + animation + history glue. See the
    // createDOM/patchDOM case 'sheet' for the rendering logic and the
    // `.mar-sheet-*` CSS rules for the visual treatment.
    const uiSheetCtor = native(2, ([config, children]) => {
      const open = config.fields.open;       // VBool
      const outlet = config.fields.outlet;   // VString
      const onDismiss = config.fields.onDismiss; // any Msg value
      return VView('sheet', [
        { name: 'open',   value: open },
        { name: 'outlet', value: outlet },
      ], children.xs, '', onDismiss);
    });
    def('uiSheet', uiSheetCtor); def('UI.sheet', uiSheetCtor);

    // confirm : { title, confirmLabel, destructive, onConfirm,
    //             onCancel } -> View msg
    //
    // Destructive-action confirmation dialog. Render-time semantics
    // is "if this view appears in the tree, mount the modal; if it
    // doesn't, the modal is gone." Apps therefore branch via `case`
    // returning `UI.confirm {...}` when active and `UI.empty` when
    // not — there's no explicit `isOpen` field.
    //
    // We stash both message handlers as attrs because the view has
    // two distinct dispatch paths (confirm + cancel) and VView's
    // single `msg` slot can only hold one. Renderer reads both off
    // attrs when wiring the dialog buttons + backdrop tap.
    const uiConfirmCtor = native(1, ([config]) => {
      return VView('confirmDialog', [
        { name: 'title',        value: config.fields.title },
        { name: 'confirmLabel', value: config.fields.confirmLabel },
        { name: 'destructive',  value: config.fields.destructive },
        { name: 'onConfirm',    value: config.fields.onConfirm },
        { name: 'onCancel',     value: config.fields.onCancel },
      ], [], '', null);
    });
    def('uiConfirm', uiConfirmCtor); def('UI.confirm', uiConfirmCtor);

    // Modifier attrs.
    const navTitleCtor   = native(1, ([s]) => makeAttr('navigationTitle', s));
    def('navigationTitle', navTitleCtor); def('UI.navigationTitle', navTitleCtor);
    // topBarTrailing / topBarLeading — toolbar items at the trailing
    // / leading edge of the top bar. Names match SwiftUI's
    // `.topBarTrailing` / `.topBarLeading` placement (iOS 17+).
    const topBarTrailingCtor = native(1, ([v]) => makeAttr('topBarTrailing', v));
    def('uiTopBarTrailing', topBarTrailingCtor);
    def('UI.topBarTrailing', topBarTrailingCtor);
    const topBarLeadingCtor  = native(1, ([v]) => makeAttr('topBarLeading', v));
    def('uiTopBarLeading',  topBarLeadingCtor);
    def('UI.topBarLeading',  topBarLeadingCtor);
    const headerCtor     = native(1, ([s]) => makeAttr('header', s));
    def('header', headerCtor); def('UI.header', headerCtor);
    const footerCtor     = native(1, ([s]) => makeAttr('footer', s));
    def('footer', footerCtor); def('UI.footer', footerCtor);

    // numericCode bundles numeric keypad + Code-from-Mail autofill —
    // single flag attr; the renderer expands it (see applyInputKind).
    const numericCodeAttr = flagAttr('inputKindNumericCode');
    def('numericCode', numericCodeAttr); def('UI.numericCode', numericCodeAttr);

    // Re-expose existing input-kind / submit attrs under UI.* so user
    // code that lives in the SwiftUI-style vocabulary stays in one
    // namespace. Same runtime values.
    def('UI.email',       flagAttr('inputKindEmail'));
    def('UI.password',    flagAttr('inputKindPassword'));
    def('UI.newPassword', flagAttr('inputKindNewPassword'));
    def('UI.numeric',     flagAttr('inputKindNumeric'));
    def('UI.oneTimeCode', flagAttr('inputKindOneTimeCode'));
    def('UI.submit',      submitAttrCtor);

    // Page.create takes a record { path, title?, init, update, view }.
    // Produces a VCtor "__Page" with positional args the mountPages
    // code consumes. `title` is optional; when omitted the browser's
    // existing tab title stays.
    const pageCreateImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__Page', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageCreate', pageCreateImpl);
    def('Page.create', pageCreateImpl);

    // Page.protected — same shape as Page.create plus User-aware
    // handler signatures (init/update/view receive the logged-in
    // User as first arg). The mountPages code recognizes the
    // "__ProtectedPage" tag, runs Auth.me on first entry, and
    // either threads the User into handlers or navigates to the
    // sign-in path declared in Auth.config.signInPage.
    const pageProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__ProtectedPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageProtected', pageProtectedImpl);
    def('Page.protected', pageProtectedImpl);

    // Page.adminProtected mints the opaque AdminSession capability and threads
    // it into init/update/view as the leading argument. There's no separate
    // client-side admin auth gate here: the admin session cookie authorizes the
    // Mar.Admin.* fetches server-side (the /_mar/admin/api/mar/* routes 401
    // without it). So we pre-apply a placeholder AdminSession and emit a plain
    // __Page that mounts like any other — the page only ever passes that value
    // to Mar.Admin.*, which ignore it. (Web-only; no iOS admin app.)
    const pageAdminProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      const adminSession = VString('admin');
      // init : AdminSession -> (Model, Effect) — pre-applying the
      // session yields the (model, effect) tuple a plain page's init
      // now IS (no vestigial unit arg). The tuple is pure data and
      // the effect inside is a lazy description, so evaluating here
      // at page-construction time is equivalent to first mount.
      const init = apply(f.init, adminSession);
      const update = native(2, ([msg, model]) => apply(apply(apply(f.update, adminSession), msg), model));
      const view = native(1, ([model]) => apply(apply(f.view, adminSession), model));
      const subscriptions = native(1, ([model]) => apply(apply(f.subscriptions, adminSession), model));
      return VCtor('__Page', [f.path, init, update, view, title, subscriptions]);
    });
    def('pageAdminProtected', pageAdminProtectedImpl);
    def('Page.adminProtected', pageAdminProtectedImpl);

    // Mar.Admin.* — privileged server-introspection, shaped like Service.call
    // (AdminSession -> (Result String resp -> msg) -> Effect String msg). The
    // panel performs them as Cmds; the result arrives through toMsg. The
    // AdminSession argument is the compile-time gate only — at runtime the
    // admin session cookie (same-origin) authorizes the request and the server
    // runs the real introspection body (internal/jsserve/admin_mar.go),
    // returning the Mar Value as JSON which jsToMar rebuilds here.
    const marAdminFetch = (path, toMsg) => VEffect(() => {
      fetch(path, { method: 'GET', credentials: 'same-origin' })
        .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
        .then(r => {
          if (r.status === 401) {
            // Admin session missing/expired — send the operator to the panel's
            // own sign-in page. Guarded so the panel's several concurrent
            // fetches redirect once.
            if (!globalThis.__marAdminRedirecting) {
              globalThis.__marAdminRedirecting = true;
              window.location.assign('/_mar/admin/login');
            }
            return;
          }
          if (!r.ok) {
            if (currentDispatch) currentDispatch(apply(toMsg, VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + r.status))])));
            return;
          }
          let parsed;
          try { parsed = jsToMar(JSON.parse(r.body)); }
          catch (e) {
            if (currentDispatch) currentDispatch(apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))])));
            return;
          }
          if (currentDispatch) currentDispatch(apply(toMsg, VCtor('Ok', [parsed])));
        })
        .catch(err => {
          if (currentDispatch) currentDispatch(apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))])));
        });
      return VUnit();
    }, 'marAdmin');

    def('marAdminServerInfo', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/server-info', toMsg)));
    def('Mar.Admin.serverInfo', envLookup(env, 'marAdminServerInfo'));
    def('marAdminDbStats', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/db-stats', toMsg)));
    def('Mar.Admin.dbStats', envLookup(env, 'marAdminDbStats'));
    def('marAdminRecentRequests', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/recent-requests', toMsg)));
    def('Mar.Admin.recentRequests', envLookup(env, 'marAdminRecentRequests'));
    def('marAdminListEntities', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/entities', toMsg)));
    def('Mar.Admin.listEntities', envLookup(env, 'marAdminListEntities'));
    def('marAdminListEntityRows', native(3, ([_s, entity, toMsg]) => marAdminFetch('/_mar/admin/api/mar/entity-rows?entity=' + encodeURIComponent(entity.s), toMsg)));
    def('Mar.Admin.listEntityRows', envLookup(env, 'marAdminListEntityRows'));
    def('marAdminListBackups', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/backups', toMsg)));
    def('Mar.Admin.listBackups', envLookup(env, 'marAdminListBackups'));
    // Admin sign-in flow — POST to the existing /_mar/admin/auth/* endpoints
    // (reuses authPost, same as the user Auth.*). Pre-auth, so no AdminSession.
    def('marAdminRequestCode', native(2, ([req, toMsg]) => authPost('/_mar/admin/auth/request-code', marToJs(req), toMsg, () => VUnit())));
    def('Mar.Admin.requestCode', envLookup(env, 'marAdminRequestCode'));
    def('marAdminVerifyCode', native(2, ([req, toMsg]) => authPost('/_mar/admin/auth/verify-code', marToJs(req), toMsg, (text) => jsToMar(JSON.parse(text)))));
    def('Mar.Admin.verifyCode', envLookup(env, 'marAdminVerifyCode'));
    def('marAdminSignOut', native(1, ([toMsg]) => authPost('/_mar/admin/auth/logout', null, toMsg, () => VUnit())));
    def('Mar.Admin.signOut', envLookup(env, 'marAdminSignOut'));

    // Page.dynamic — pattern path with `:param` segments. The runtime
    // matches the URL against the pattern at navigation time, threading
    // a Params record through init/update/view as the leading argument.
    // The wire-format ctor is __DynamicPage; mountPages parses the path
    // pattern lazily so the framework only walks segments once per page.
    const pageDynamicImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__DynamicPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageDynamic', pageDynamicImpl);
    def('Page.dynamic', pageDynamicImpl);

    // Page.dynamicProtected — pattern path + auth gate. Combines
    // __DynamicPage's URL matching with __ProtectedPage's Auth.me
    // bootstrap. Handler signature is `User -> Params -> ...` (User
    // first, mirroring Page.protected).
    const pageDynamicProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__DynamicProtectedPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageDynamicProtected', pageDynamicProtectedImpl);
    def('Page.dynamicProtected', pageDynamicProtectedImpl);

    // Page.dynamicAdminProtected — pattern path + admin session. Pre-applies a
    // placeholder AdminSession (the admin cookie does the real auth on the
    // Mar.Admin.* fetches) and emits a plain __DynamicPage, so the existing
    // dynamic-page machinery threads Params in. Web-only (no iOS admin).
    const pageDynamicAdminProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      const adminSession = VString('admin');
      // Real sigs thread (AdminSession, Params, …); pre-apply the session,
      // leaving (Params, …) — exactly __DynamicPage's shape.
      const init = native(1, ([params]) => apply(apply(f.init, adminSession), params));
      const update = native(3, ([params, msg, model]) => apply(apply(apply(apply(f.update, adminSession), params), msg), model));
      const view = native(2, ([params, model]) => apply(apply(apply(f.view, adminSession), params), model));
      const subscriptions = native(2, ([params, model]) => apply(apply(apply(f.subscriptions, adminSession), params), model));
      return VCtor('__DynamicPage', [f.path, init, update, view, title, subscriptions]);
    });
    def('pageDynamicAdminProtected', pageDynamicAdminProtectedImpl);
    def('Page.dynamicAdminProtected', pageDynamicAdminProtectedImpl);

    // Nav.* — programmatic navigation. The actual implementations
    // live inside mountPages (where `pages`/`render` are in scope) and
    // get attached to globalThis.__marNav at mount time. Calling them
    // before mountPages has run is a no-op (effect runs but has nothing
    // to navigate within).
    def('navPush', native(1, ([url]) => VEffect(() => {
      const nav = globalThis.__marNav;
      if (nav) nav.push(url.s);
      return VUnit();
    }, 'navPush')));
    def('Nav.push', envLookup(env, 'navPush'));

    def('navReplace', native(1, ([url]) => VEffect(() => {
      const nav = globalThis.__marNav;
      if (nav) nav.replace(url.s);
      return VUnit();
    }, 'navReplace')));
    def('Nav.replace', envLookup(env, 'navReplace'));

    // Auth.completeSignIn : Effect e ()
    //
    // The right way to navigate after a successful Auth.verifyCode.
    // Reads the `?next=` query parameter (set by the framework when a
    // 401 redirected the user here, or when they followed a deep link
    // while unauthed) and goes there. Falls back to "/" when next is
    // absent, malformed, or fails the same-origin path-only safety
    // check (open-redirect prevention).
    //
    // User code:
    //   SubmitDone (Ok ()) ->
    //       ( model, Auth.completeSignIn )
    //
    // Lives under Auth.* (not Nav.*) because it bundles auth-specific
    // cleanup (resetting the auth-expired redirect coalescer) with
    // the navigation step. Nav.* stays focused on pure navigation;
    // Auth.* owns the post-login transition end-to-end.
    def('authCompleteSignIn', VEffect(() => {
      const nav = globalThis.__marNav;
      if (!nav) return VUnit();
      let target = '/';
      try {
        const params = new URLSearchParams(window.location.search);
        const next = params.get('next');
        if (next && isSafeReturnPath(next)) {
          target = next;
        }
      } catch (_) {
        // Defensive — URLSearchParams is universally supported but
        // some test environments may not have window.location.
      }
      // Reset the auth-expired coalescer so the next genuine session
      // expiry triggers a fresh redirect. Has to happen BEFORE the
      // nav call because the post-replace render may itself fire a
      // Service.call that would race against a stale flag.
      globalThis.__marRedirectingToSignIn = false;
      // replaceFresh (not replace) so the back button on the
      // destination doesn't point at /sign-in/verify/<email>. The
      // auth flow is one-way: once signed in, going "back" to the
      // (now-irrelevant) code-entry screen would only confuse the
      // user. Resetting depth to 0 reflects that this is a fresh
      // starting point.
      nav.replaceFresh(target);
      return VUnit();
    }, 'authCompleteSignIn'));
    def('Auth.completeSignIn', envLookup(env, 'authCompleteSignIn'));

    // isSafeReturnPath validates that a `?next=` value is a same-
    // origin relative path with no protocol-injection escape hatches.
    // Rejects:
    //   - protocol URLs:    "https://evil.com/x", "javascript:alert(1)"
    //   - protocol-relative "//evil.com" (browsers treat as cross-origin)
    //   - path traversal:   "/\\..", "/.."
    //   - missing leading /: "evil.com/x"
    function isSafeReturnPath(p) {
      if (typeof p !== 'string' || p.length === 0) return false;
      if (!p.startsWith('/')) return false;
      if (p.startsWith('//') || p.startsWith('/\\')) return false;
      if (/^\/+\.\.(\/|$)/.test(p)) return false;
      // Reject anything that looks like a protocol scheme. The leading
      // / blocks the simple case but a chain like "/x/javascript:..."
      // could be misinterpreted by a buggy router; we just don't
      // accept any colon before the first slash after the prefix.
      // Conservative check: refuse colons in the first path segment.
      const firstSegEnd = p.indexOf('/', 1);
      const firstSeg = firstSegEnd < 0 ? p.slice(1) : p.slice(1, firstSegEnd);
      if (firstSeg.indexOf(':') >= 0) return false;
      return true;
    }

    // Cache parsed Path patterns by source string. Keeps linkTo and
    // navPushTo / navReplaceTo cheap when the same Path value is used
    // many times (e.g. across a list render).
    const pathCache = Object.create(null);
    function parsePathCached(src) {
      let p = pathCache[src];
      if (!p) {
        p = parsePathPattern(src);
        pathCache[src] = p;
      }
      return p;
    }

    // linkTo : Path r -> r -> String
    // Build a URL from a typed Path + the params record. Path values
    // are VStrings at runtime (the typechecker enforces the surface
    // type); the cached parsePathPattern turns them into typed segments
    // for the URL builder. Used in `href` attributes — pure, no Effect.
    def('linkTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('linkTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      return VString(buildPathURL(pattern, params));
    }));

    // Nav.pushTo : Path r -> r -> Effect e msg
    // Type-safe sibling of Nav.push — pre-renders the URL via the
    // typed Path, then reuses the same global navigation hook. The
    // URL build runs eagerly (so missing-param errors surface
    // synchronously), but the actual history mutation only fires
    // when the Effect's Run is invoked by the runtime.
    def('navPushTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('Nav.pushTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      return VEffect(() => {
        const nav = globalThis.__marNav;
        if (nav) nav.push(url);
        return VUnit();
      }, 'navPushTo');
    }));
    def('Nav.pushTo', envLookup(env, 'navPushTo'));

    def('navReplaceTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('Nav.replaceTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      return VEffect(() => {
        const nav = globalThis.__marNav;
        if (nav) nav.replace(url);
        return VUnit();
      }, 'navReplaceTo');
    }));
    def('Nav.replaceTo', envLookup(env, 'navReplaceTo'));

    // App.frontend : List Page -> Effect String ()
    // Mounts a page list with URL routing. Port comes from the host
    // server's mar.json, not user code.
    def('appFrontend', native(1, ([list]) => VEffect(() => mountPages(list.xs), 'mountPages')));
    def('App.frontend', native(1, ([list]) => VEffect(() => mountPages(list.xs), 'mountPages')));

    // App.backend : List Route -> Effect String ()
    // Backend is a server-side concept. The browser bundle never sees
    // backend routes; this builtin returns a no-op Effect on the JS side.
    def('appBackend', native(1, ([_]) => VEffect(() => VUnit(), 'noop')));
    def('App.backend', native(1, ([_]) => VEffect(() => VUnit(), 'noop')));

    // App.fullstack : { api, pages } -> Effect String ()
    // The browser only cares about `pages`; `api` runs server-side.
    def('appFullstack', native(1, ([rec]) =>
      VEffect(() => mountPages(rec.fields.pages.xs), 'mountPages')
    ));
    def('App.fullstack', native(1, ([rec]) =>
      VEffect(() => mountPages(rec.fields.pages.xs), 'mountPages')
    ));

    // Effect — sync versions (effects are run-on-demand thunks).
    def('effectSucceed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('Task.succeed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('effectMap', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('Task.map', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('effectAndThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('Task.andThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));

    // Effect.fail : e -> Effect e a — throws when run. Uncaught
    // failures surface as a JS exception in the dispatcher; user
    // code that wants typed recovery should use Result.* instead.
    const effectFailImpl = native(1, ([err]) => VEffect(() => {
      const msg = err && err.k === 'S' ? err.s : ('(' + (err && err.k) + ')');
      throw new Error('Task.fail: ' + msg);
    }, 'fail'));
    def('effectFail', effectFailImpl);
    def('Task.fail', effectFailImpl);

    // Effect.forEach : (a -> Effect e ()) -> List a -> Effect e ()
    // Sequential — halts on first failure (the thrown error
    // propagates out of run()).
    const effectForEachImpl = native(2, ([fn, list]) => VEffect(() => {
      for (const x of list.xs) {
        const eff = apply(fn, x);
        eff.run();
      }
      return VUnit();
    }, 'forEach'));
    def('effectForEach', effectForEachImpl);
    def('Task.forEach', effectForEachImpl);

    // Effect.sequence : List (Effect e a) -> Effect e (List a)
    const effectSequenceImpl = native(1, ([list]) => VEffect(() => {
      const out = new Array(list.xs.length);
      for (let i = 0; i < list.xs.length; i++) {
        out[i] = list.xs[i].run();
      }
      return VList(out);
    }, 'sequence'));
    def('effectSequence', effectSequenceImpl);
    def('Task.sequence', effectSequenceImpl);

    // Effect.batch : List (Effect e msg) -> Effect e msg
    // Fire-and-forget fan-out (the Cmd.batch of Mar). Each child's
    // run() kicks off its own work and delivers through its own
    // toMsg dispatch (Service.call effects self-dispatch when their
    // fetch resolves), so the children genuinely run concurrently.
    // The batch's own value is unit, same dynamic shape as
    // Effect.none.
    const effectBatchImpl = native(1, ([list]) => VEffect(() => {
      for (const e of list.xs) {
        if (e && e.k === 'E' && typeof e.run === 'function') e.run();
      }
      return VUnit();
    }, 'batch'));
    def('effectBatch', effectBatchImpl);
    def('Cmd.batch', effectBatchImpl);

    def('effectNone', VEffect(() => VUnit(), 'none'));
    def('Cmd.none', VEffect(() => VUnit(), 'none'));

    // Sub.none / Sub.batch — the frontend subscription monoid. A Sub is a
    // declarative descriptor (k:'SUB'); the IIFE-level reconcileSubs reads it.
    const subNoneVal = { k: 'SUB', items: [] };
    def('subNone', subNoneVal);
    def('Sub.none', subNoneVal);
    const subBatchImpl = native(1, ([list]) => {
      const items = [];
      const xs = (list && list.xs) || [];
      for (const sub of xs) {
        if (sub && sub.k === 'SUB' && sub.items) {
          for (const it of sub.items) items.push(it);
        }
      }
      return { k: 'SUB', items };
    });
    def('subBatch', subBatchImpl);
    def('Sub.batch', subBatchImpl);

    // Random — Elm-style generators. A Generator a is a unit-thunk (native(1)
    // ignoring its arg); applying it yields one random value from Math.random.
    // Random.generate runs it and dispatches the value as a Msg (a Cmd), like
    // Service.call. The type system keeps Generator a distinct from () -> a.
    const runGen = (g) => apply(g, VUnit());
    const asGen = (produce) => native(1, () => produce());
    const randomGenerate = native(2, ([toMsg, g]) => VEffect(() => {
      const v = runGen(g);
      if (currentDispatch) currentDispatch(apply(toMsg, v));
      return VUnit();
    }, 'randomGenerate'));
    def('randomGenerate', randomGenerate);
    def('Random.generate', randomGenerate);
    const randomInt = native(2, ([lo, hi]) => {
      let a = lo.n, b = hi.n;
      if (a > b) { const t = a; a = b; b = t; }
      return asGen(() => VInt(a + Math.floor(Math.random() * (b - a + 1))));
    });
    def('randomInt', randomInt);
    def('Random.int', randomInt);
    const randomConstant = native(1, ([v]) => asGen(() => v));
    def('randomConstant', randomConstant);
    def('Random.constant', randomConstant);
    const randomUniform = native(2, ([first, rest]) => {
      const items = [first].concat((rest && rest.xs) || []);
      return asGen(() => items[Math.floor(Math.random() * items.length)]);
    });
    def('randomUniform', randomUniform);
    def('Random.uniform', randomUniform);
    const randomList = native(2, ([n, g]) => asGen(() => {
      const count = Math.max(0, n.n);
      const out = [];
      for (let i = 0; i < count; i++) out.push(runGen(g));
      return VList(out);
    }));
    def('randomList', randomList);
    def('Random.list', randomList);
    const randomPair = native(2, ([g1, g2]) => asGen(() => VTuple([runGen(g1), runGen(g2)])));
    def('randomPair', randomPair);
    def('Random.pair', randomPair);
    const randomMap = native(2, ([f, g]) => asGen(() => apply(f, runGen(g))));
    def('randomMap', randomMap);
    def('Random.map', randomMap);
    const randomMap2 = native(3, ([f, g1, g2]) => asGen(() => apply(apply(f, runGen(g1)), runGen(g2))));
    def('randomMap2', randomMap2);
    def('Random.map2', randomMap2);
    const randomMap3 = native(4, ([f, g1, g2, g3]) => asGen(() => apply(apply(apply(f, runGen(g1)), runGen(g2)), runGen(g3))));
    def('randomMap3', randomMap3);
    def('Random.map3', randomMap3);
    const randomAndThen = native(2, ([f, g]) => asGen(() => runGen(apply(f, runGen(g)))));
    def('randomAndThen', randomAndThen);
    def('Random.andThen', randomAndThen);

    // Cmd.perform : (a -> msg) -> Task a -> Cmd msg
    // The Task->Cmd bridge (Elm's Task.perform): run the task and deliver
    // its produced value to the MVU loop as a msg. The only way a Task
    // (Time.now, future client reads) reaches `update` on the frontend.
    const cmdPerformImpl = native(2, ([toMsg, task]) => VEffect(() => {
      const v = task.run();
      if (currentDispatch) currentDispatch(apply(toMsg, v));
      return VUnit();
    }, 'perform'));
    def('cmdPerform', cmdPerformImpl);
    def('Cmd.perform', cmdPerformImpl);

    // Time — Duration-typed unit smart constructors. Mirrors the Go
    // runtime's timeBuiltins (internal/runtime/time.go). Each
    // constructor multiplies by its unit's seconds-count at build
    // time so the framework + user code only ever deals with
    // pre-normalized seconds.
    const mkDuration = (mult) => native(1, ([n]) => VDuration(n.n * mult));
    def('timeSeconds', mkDuration(1));         def('Time.seconds', mkDuration(1));
    def('timeMinutes', mkDuration(60));        def('Time.minutes', mkDuration(60));
    def('timeHours',   mkDuration(60*60));     def('Time.hours',   mkDuration(60*60));
    def('timeDays',    mkDuration(24*60*60));  def('Time.days',    mkDuration(24*60*60));
    def('timeWeeks',   mkDuration(7*24*60*60));def('Time.weeks',   mkDuration(7*24*60*60));
    def('timeToSeconds', native(1, ([d]) => VInt(d.seconds || 0)));
    def('Time.toSeconds', envLookup(env, 'timeToSeconds'));

    // Time.now — Effect e Time. Reads the wall clock; same shape as
    // Effect.succeed but the value is fresh on each run.
    def('timeNow', VEffect(() => VTime(Date.now()), 'timeNow'));
    def('Time.now', envLookup(env, 'timeNow'));

    // Time.every : Duration -> (Time -> msg) -> Sub msg — a recurring
    // subscription. Identity is the interval (seconds); the tagger is the
    // payload. The reconciler turns it into a setInterval that delivers the
    // current Time each tick. Fires first AFTER one interval (Elm's Time.every);
    // seed the immediate value with `Cmd.perform GotNow Time.now` in init.
    const timeEveryImpl = native(2, ([dur, tagger]) => {
      const seconds = (dur && typeof dur.seconds === 'number') ? dur.seconds : 0;
      return { k: 'SUB', items: [{ src: 'timeEvery', key: 'timeEvery:' + seconds, intervalMs: seconds * 1000, tagger }] };
    });
    def('timeEvery', timeEveryImpl);
    def('Time.every', timeEveryImpl);

    // Time arithmetic — durations are seconds, times are millis;
    // multiply by 1000 to align units when shifting.
    def('timeAdd', native(2, ([t, d]) => VTime((t.millis || 0) + (d.seconds || 0) * 1000)));
    def('Time.add', envLookup(env, 'timeAdd'));
    def('timeSub', native(2, ([t, d]) => VTime((t.millis || 0) - (d.seconds || 0) * 1000)));
    def('Time.sub', envLookup(env, 'timeSub'));
    def('timeDiff', native(2, ([a, b]) => VDuration(Math.floor(((b.millis || 0) - (a.millis || 0)) / 1000))));
    def('Time.diff', envLookup(env, 'timeDiff'));

    def('timeBefore', native(2, ([a, b]) => VBool((a.millis || 0) < (b.millis || 0))));
    def('Time.before', envLookup(env, 'timeBefore'));
    def('timeAfter',  native(2, ([a, b]) => VBool((a.millis || 0) > (b.millis || 0))));
    def('Time.after', envLookup(env, 'timeAfter'));

    // Time.toIso → ISO 8601 UTC. Wire format used by the server.
    def('timeToIso', native(1, ([t]) => VString(new Date(t.millis || 0).toISOString())));
    def('Time.toIso', envLookup(env, 'timeToIso'));
    // Time.fromIso → Maybe Time. Returns Nothing on parse failure.
    def('timeFromIso', native(1, ([s]) => {
      const ms = Date.parse(s.s);
      if (isNaN(ms)) return VCtor('Nothing', []);
      return VCtor('Just', [VTime(ms)]);
    }));
    def('Time.fromIso', envLookup(env, 'timeFromIso'));

    def('timeToMillis', native(1, ([t]) => VInt(t.millis || 0)));
    def('Time.toMillis', envLookup(env, 'timeToMillis'));

    // Calendar-aware constructors and arithmetic. Different from
    // Time.add (Duration shift) because months/years vary in
    // length; uses native Date methods so normalization (Jan 31 +
    // 1 month → Mar 3) matches the Go runtime.
    def('timeFromYMD', native(3, ([y, m, d]) =>
      VTime(Date.UTC(y.n, m.n - 1, d.n))));
    def('Time.fromYMD', envLookup(env, 'timeFromYMD'));

    const calendarShift = (yMul, mMul, dMul) => native(2, ([t, n]) => {
      const date = new Date(t.millis || 0);
      const k = n.n;
      date.setUTCFullYear(date.getUTCFullYear() + yMul * k);
      date.setUTCMonth(date.getUTCMonth() + mMul * k);
      date.setUTCDate(date.getUTCDate() + dMul * k);
      return VTime(date.getTime());
    });
    def('timeAddDays',   calendarShift(0, 0, 1));
    def('Time.addDays',  envLookup(env, 'timeAddDays'));
    def('timeAddMonths', calendarShift(0, 1, 0));
    def('Time.addMonths', envLookup(env, 'timeAddMonths'));
    def('timeAddYears',  calendarShift(1, 0, 0));
    def('Time.addYears', envLookup(env, 'timeAddYears'));

    // Component getters (UTC). Month is 1-indexed externally —
    // we add 1 to JS's getUTCMonth (0-indexed) so user code reads
    // the same values it'd see on the Go and iOS sides.
    const component = (extract) => native(1, ([t]) => VInt(extract(new Date(t.millis || 0))));
    def('timeYear',    component(d => d.getUTCFullYear()));
    def('Time.year',   envLookup(env, 'timeYear'));
    def('timeMonth',   component(d => d.getUTCMonth() + 1));
    def('Time.month',  envLookup(env, 'timeMonth'));
    def('timeDay',     component(d => d.getUTCDate()));
    def('Time.day',    envLookup(env, 'timeDay'));
    def('timeHour',    component(d => d.getUTCHours()));
    def('Time.hour',   envLookup(env, 'timeHour'));
    def('timeMinute', component(d => d.getUTCMinutes()));
    def('Time.minute', envLookup(env, 'timeMinute'));
    def('timeSecond', component(d => d.getUTCSeconds()));
    def('Time.second', envLookup(env, 'timeSecond'));

    // JSON.decode : String -> Result String α — uses the IIFE-level
    // jsToMar so that fetchAuthMe (in mountPages) can reach the
    // same decoder.
    def('jsonDecode', native(1, ([raw]) => {
      try {
        const parsed = JSON.parse(raw.s);
        return VCtor('Ok', [jsToMar(parsed)]);
      } catch (e) {
        return VCtor('Err', [VString(String(e && e.message || e))]);
      }
    }));
    def('JSON.decode', envLookup(env, 'jsonDecode'));

    // JSON.encode : α -> String — uses the IIFE-level marToJs so
    // every consumer (jsonEncode, Service.call body, authPost
    // body) shares the same encoder. Mar's Maybe/Result get
    // shorthand encodings; other ctors round-trip via the
    // {__ctor, __args} marker convention.
    def('jsonEncode', native(1, ([v]) => VString(JSON.stringify(marToJs(v)))));
    def('JSON.encode', envLookup(env, 'jsonEncode'));

    // Http.get / Http.post — async fetch wrapped in an Effect.
    //
    //   Http.get  : String -> (Result String String -> msg) -> Effect Never msg
    //   Http.post : String -> String -> (Result String String -> msg) -> Effect Never msg
    //
    // The third (toMsg) argument lets the call result be turned into a Msg
    // that gets dispatched into the running app. The Effect itself does not
    // produce a value synchronously — the response arrives asynchronously
    // and is delivered as a Msg.
    def('httpGet', native(2, ([url, toMsg]) => {
      return VEffect(() => {
        fetch(url.s)
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'httpGet');
    }));
    def('Http.get', envLookup(env, 'httpGet'));

    // Entity.* / Repo.* stubs — server-only constructs whose only purpose
    // on the client side is to evaluate without errors when a shared
    // module declares them. The actual SQL never runs in the browser;
    // server-side handlers (that reference Repo) live inside Service
    // closures and are dispatched via fetch (Service.call).
    const entityStub = native(2, ([_name, _schema]) => VCtor('__Entity', []));
    def('entityDefine', entityStub);
    def('Entity.define', entityStub);
    const colStub = (sql) => VCtor('__Column', [VString(sql)]);
    def('entitySerial', colStub('serial'));
    def('Entity.serial', colStub('serial'));
    const colCtor = native(1, ([_constraint]) => colStub('?'));
    def('entityInt',  colCtor); def('Entity.int',  colCtor);
    def('entityText', colCtor); def('Entity.text', colCtor);
    def('entityBool', colCtor); def('Entity.bool', colCtor);
    def('entityTimestamp', colCtor); def('Entity.timestamp', colCtor);
    const constraintStub = VCtor('__Constraint', []);
    def('entityNotNull', constraintStub);
    def('Entity.notNull', constraintStub);
    // Repo.* stubs — server-only. If accidentally called from the
    // client, return an Effect that errors clearly. Most calls don't
    // run in the client because they live inside Service handler
    // closures that the client never invokes.
    const repoServerOnly = (name) => native(1, () =>
      VEffect(() => { throw new Error(name + ' runs only server-side'); }, name)
    );
    def('repoAll',         repoServerOnly('Repo.all'));
    def('Repo.all',        repoServerOnly('Repo.all'));
    def('repoFindById',    native(2, () => VEffect(() => { throw new Error('Repo.findById runs only server-side'); }, 'repoFindById')));
    def('Repo.findById',   envLookup(env, 'repoFindById'));
    def('repoFindBy',      native(2, () => VEffect(() => { throw new Error('Repo.findBy runs only server-side'); }, 'repoFindBy')));
    def('Repo.findBy',     envLookup(env, 'repoFindBy'));
    def('repoCreate',      native(2, () => VEffect(() => { throw new Error('Repo.create runs only server-side'); }, 'repoCreate')));
    def('Repo.create',     envLookup(env, 'repoCreate'));
    def('repoUpdate',      native(3, () => VEffect(() => { throw new Error('Repo.update runs only server-side'); }, 'repoUpdate')));
    def('Repo.update',     envLookup(env, 'repoUpdate'));
    def('repoDeleteById',  native(2, () => VEffect(() => { throw new Error('Repo.deleteById runs only server-side'); }, 'repoDeleteById')));
    def('Repo.deleteById', envLookup(env, 'repoDeleteById'));

    // Service (RPC over HTTP). Server-side wraps a handler; browser-side
    // the handler is never invoked — the contract just carries the verb +
    // path so Service.call can build the request.
    //
    // Service.declare VERB "path": a typed RPC contract carrying the HTTP
    // verb and URL pattern. The browser never runs the handler; it only
    // needs the verb + path so Service.call can build the request.
    def('serviceDeclare', native(2, ([method, path]) => {
      const svc = VCtor('__Service', []);
      svc.verb = (method && method.tag) || 'POST';
      svc.path = (path && path.s) || '/';
      return svc;
    }));
    def('Service.declare', envLookup(env, 'serviceDeclare'));

    // Service.implement / Auth.protect: pair a contract with a
    // handler. Server-side these produce ExposedService values the
    // dispatcher mounts; browser-side the handler is never invoked,
    // so we just return the contract back so the value evaluates and
    // any downstream references stay well-typed at runtime.
    def('serviceImplement', native(2, ([contract, _handler]) => contract));
    def('Service.implement', envLookup(env, 'serviceImplement'));

    def('authProtect', native(2, ([contract, _handler]) => contract));
    def('Auth.protect', envLookup(env, 'authProtect'));

    // PROPOSAL stubs (docs/authorization-proposal.md). The browser
    // never enforces auth — the server's dispatcher does. So these
    // are pass-throughs returning the ExposedService unchanged.
    def('authRequireRole',  native(2, ([_role, exposed]) => exposed));
    def('Auth.requireRole', envLookup(env, 'authRequireRole'));
    def('authAuthorize',    native(3, ([_load, _policy, exposed]) => exposed));
    def('Auth.authorize',   envLookup(env, 'authAuthorize'));
    def('authRequireOwner', native(3, ([_load, _sel, exposed]) => exposed));
    def('Auth.requireOwner', envLookup(env, 'authRequireOwner'));

    // Auth.config: server-side captures the user entity + signup hook
    // + email config into a global for the framework HTTP handlers.
    // Browser-side we additionally pull the `signInPage` field's
    // path off — Page.protected reads it as the redirect target when
    // the user has no session. Stored on globalThis so the closure
    // inside mountPages (set up later) can read it without explicit
    // wiring.
    def('authConfig', native(1, ([cfg]) => {
      if (cfg && cfg.fields && cfg.fields.signInPage) {
        const page = cfg.fields.signInPage;
        // The page is a __Page or __ProtectedPage ctor; first arg
        // is the path string.
        if (page.k === 'C' && page.args && page.args.length > 0) {
          const pathV = page.args[0];
          if (pathV && pathV.k === 'S') {
            globalThis.__marAuthSignInPath = pathV.s;
          }
        }
      }
      return VCtor('__Auth', [cfg]);
    }));
    def('Auth.config', envLookup(env, 'authConfig'));

    // Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
    //   - Builds the request from the contract's verb + path: typed path
    //     params go in the URL, the rest in the query (GET / DELETE) or
    //     the JSON body (POST / PUT / PATCH). Parses the JSON response into
    //     a mar value, dispatches msg(Ok | Err).
    def('serviceCall', native(3, ([svc, req, toMsg]) => {
      return VEffect(() => {
        const verb = svc.verb || 'POST';
        const built = buildServiceRequest(verb, svc.path || '/', req);
        const init = { method: verb, credentials: 'same-origin', headers: {} };
        if (built.body != null) {
          init.body = built.body;
          init.headers['Content-Type'] = 'application/json';
        }
        fetch(built.url, init)
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            // Auth-expiry interceptor: a 401 on a Service.call means
            // the session is gone (or never existed). Send the user to
            // the configured signInPath, capturing where they were so
            // Auth.completeSignIn can return them after a successful login.
            // The Err is NOT dispatched — user code never sees this
            // case. This keeps "session expired" out of every update
            // function across the app.
            if (r.status === 401 && handleAuthExpired()) return;
            if (!r.ok) {
              const msg = apply(toMsg, VCtor('Err', [serviceErrorFromResponse(r.status, r.body)]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            let parsed;
            try {
              parsed = jsToMar(JSON.parse(r.body));
            } catch (e) {
              // Response arrived but its body was not decodable: a server
              // contract bug, surfaced as a ServerError so the app can show it.
              const msg = apply(toMsg, VCtor('Err', [VCtor('ServerError', [VString('decode failed: ' + (e && e.message || e))])]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            // fetch() rejects only when the request never completed (offline,
            // DNS, connection dropped): that is the Offline case.
            const msg = apply(toMsg, VCtor('Err', [serviceErrorOffline()]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'serviceCall');
    }));
    def('Service.call', envLookup(env, 'serviceCall'));

    // friendlyFetchError translates a fetch() rejection into a
    // user-facing string. fetch() only rejects on actual network
    // failures (DNS down, server unreachable, request aborted,
    // CORS blocked, browser offline); HTTP error statuses come
    // through .then(r => ...) with r.ok=false, NOT here.
    //
    // The raw err.message varies by browser:
    //   - Safari:  "Load failed"
    //   - Chrome:  "Failed to fetch"
    //   - Firefox: "NetworkError when attempting to fetch resource"
    //
    // None of those are useful to an end user. We collapse them
    // into one calm sentence (two, when navigator.onLine confirms
    // we're offline) and stash the raw text in the console for the
    // developer debugging the issue.
    //
    // Used by every Service.call / Auth.* fetch
    // .catch handler so apps consistently see clean error strings
    // in their `Result String _` channels.
    function friendlyFetchError(err) {
      if (typeof console !== 'undefined' && console.warn) {
        console.warn('[mar] network failure:', err);
      }
      const offline = (typeof navigator !== 'undefined' && navigator.onLine === false);
      return offline
        ? "You appear to be offline. Check your connection and try again."
        : "Couldn't reach the server. Try again in a moment.";
    }

    // handleAuthExpired centralizes the "401 from Service.call" reaction:
    //
    //   1. If signInPath isn't configured (Auth.config absent), can't
    //      do anything useful — return false so the caller surfaces the
    //      Err to user code instead. Without this guard we'd swallow
    //      the error silently in apps that don't even use Auth.
    //   2. If we already navigated to sign-in for an earlier 401 in
    //      this batch (parallel Service.calls all expiring together),
    //      drop subsequent 401s on the floor — one redirect is enough.
    //      The flag is reset by Auth.completeSignIn after a successful
    //      verifyCode, so the next legitimate session expiry will
    //      redirect again.
    //   3. Otherwise: capture the current path as `?next=`, navigate
    //      to the sign-in URL via Nav.replace (which also invalidates
    //      the cached user — without that the next protected render
    //      would still see Just user and loop back to 401).
    //
    // Returns true if the redirect was triggered (or coalesced); false
    // means the caller should fall through to its default error path.
    function handleAuthExpired() {
      const signInPath = globalThis.__marAuthSignInPath;
      if (!signInPath) return false;
      if (globalThis.__marRedirectingToSignIn) return true;
      const nav = globalThis.__marNav;
      if (!nav) return false;

      globalThis.__marRedirectingToSignIn = true;

      // Capture where we were. Skip when the user is already on the
      // sign-in page (no point in `?next=/sign-in`).
      let target = signInPath;
      try {
        const here = window.location.pathname + window.location.search;
        if (here && here !== signInPath && isSafeReturnPath(here)) {
          target = signInPath + '?next=' + encodeURIComponent(here);
        }
      } catch (_) { /* fall through with bare signInPath */ }

      // replaceFresh resets marDepth to 0 — /sign-in is an entry
      // point, regardless of how deep into the app the user was
      // when their session expired. Without the reset, a user
      // bounced from /tasks/edit/42 (depth 1) would see a back
      // button on /sign-in pointing at a page they can no longer
      // access.
      nav.replaceFresh(target);
      return true;
    }

    // ----- Auth client Effects -----
    //
    // The four framework HTTP endpoints under /_auth/*. Each returns
    // an Effect that fires the request, parses the response, and
    // dispatches Ok/Err into the user's Msg via toMsg. Mirrors the
    // Service.call shape so user code looks identical.
    function authPost(path, body, toMsg, decodeOk) {
      return VEffect(() => {
        fetch(path, {
          method: 'POST',
          credentials: 'same-origin',
          body: body == null ? null : JSON.stringify(body),
          headers: body == null ? {} : { 'Content-Type': 'application/json' },
        })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            if (!r.ok) {
              const errStr = decodeAuthError(r.body) || ('HTTP ' + r.status);
              const msg = apply(toMsg, VCtor('Err', [VString(errStr)]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            let parsed;
            try {
              parsed = decodeOk(r.body);
            } catch (e) {
              const msg = apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'authPost');
    }

    // decodeServerError tries to extract a stable error code from an
    // HTTP error response body. The Mar runtime + framework endpoints
    // (auth, rate-limit middleware, admin) consistently shape error
    // bodies as `{"error": "snake_case_code", ...}`. When that shape
    // is present, return just the code so user code can `case` on it
    // cleanly. Otherwise fall back to the raw body or empty.
    //
    // Without this, a 429 from the gateway limiter would surface in
    // a Result.Err as the literal string
    //   `{"error":"rate_limited","retryAfterSeconds":3}`
    // which apps would have to substring-match against — defeating
    // the whole point of having stable codes server-side.
    function decodeServerError(body) {
      if (!body) return '';
      try {
        const j = JSON.parse(body);
        if (j && typeof j.error === 'string') {
          return j.error;
        }
      } catch (_) { /* fall through to raw body */ }
      return body;
    }

    // serviceErrorFromResponse / serviceErrorOffline build the Service.Error
    // union a Service.call delivers in its Err. Mirrors the Go runtime
    // (serviceErrorString) and Swift. Offline = request never reached the
    // server; Unauthorized = 401; ServerError = anything else, carrying the
    // server's message (the operator's snake_case code, intact).
    function serviceErrorOffline() { return VCtor('Offline'); }
    function serviceErrorFromResponse(status, body) {
      if (status === 401) return VCtor('Unauthorized');
      return VCtor('ServerError', [VString(decodeServerError(body) || ('HTTP ' + (status || 0)))]);
    }
    // serviceErrorToStringJS folds a Service.Error to its default display
    // string. Keep these messages identical to the Go and Swift runtimes.
    function serviceErrorToStringJS(e) {
      if (!e || e.tag === undefined) return '';
      switch (e.tag) {
        case 'Offline': return "Can't reach the server. Check your connection and try again.";
        case 'Unauthorized': return 'Your session has expired. Please sign in again.';
        case 'ServerError': return (e.args && e.args[0] && e.args[0].s) || '';
        default: return e.tag;
      }
    }

    // authOutcomePost drives Auth.requestCode / Auth.verifyCode. Domain
    // codes the endpoint is known to emit become typed outcome
    // constructors in the Ok (the screen cases on them); everything else
    // is transport and becomes a Service.Error in the Err, exactly like
    // Service.call. `mapCode` returns the outcome VCtor for a known
    // domain code or null. Mirrors the Swift MarHTTP boundary.
    function authOutcomePost(path, body, toMsg, okOutcome, mapCode) {
      return VEffect(() => {
        fetch(path, {
          method: 'POST',
          credentials: 'same-origin',
          body: body == null ? null : JSON.stringify(body),
          headers: body == null ? {} : { 'Content-Type': 'application/json' },
        })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            let result;
            if (r.ok) {
              try {
                result = VCtor('Ok', [okOutcome(r.body)]);
              } catch (e) {
                result = VCtor('Err', [VCtor('ServerError', [VString('decode failed: ' + (e && e.message || e))])]);
              }
            } else {
              const domain = mapCode(decodeServerError(r.body));
              result = domain
                ? VCtor('Ok', [domain])
                : VCtor('Err', [serviceErrorFromResponse(r.status, r.body)]);
            }
            if (currentDispatch) currentDispatch(apply(toMsg, result));
          })
          .catch(() => {
            if (currentDispatch) currentDispatch(apply(toMsg, VCtor('Err', [serviceErrorOffline()])));
          });
        return VUnit();
      }, 'authPost');
    }

    def('authRequestCode', native(2, ([req, toMsg]) => {
      // CodeSent never reveals whether the email has an account: the
      // server answers 200 for unknown emails on purpose.
      return authOutcomePost('/_auth/request-code', marToJs(req), toMsg,
        () => VCtor('CodeSent'),
        code => code === 'rate_limited' ? VCtor('RateLimited')
              : code === 'invalid_email' ? VCtor('InvalidEmail')
              : null);
    }));
    def('Auth.requestCode', envLookup(env, 'authRequestCode'));

    def('authVerifyCode', native(2, ([req, toMsg]) => {
      return authOutcomePost('/_auth/verify-code', marToJs(req), toMsg,
        text => VCtor('SignedIn', [jsToMar(JSON.parse(text))]),
        code => code === 'invalid_code' ? VCtor('WrongCode')
              : code === 'too_many_attempts' ? VCtor('TooManyAttempts')
              : null);
    }));
    def('Auth.verifyCode', envLookup(env, 'authVerifyCode'));

    // Auth outcome constructors — qualified-only, like Service.Error.
    def('Auth.CodeSent', VCtor('CodeSent'));
    def('Auth.InvalidEmail', VCtor('InvalidEmail'));
    def('Auth.RateLimited', VCtor('RateLimited'));
    def('Auth.WrongCode', VCtor('WrongCode'));
    def('Auth.TooManyAttempts', VCtor('TooManyAttempts'));
    def('Auth.SignedIn', native(1, args => VCtor('SignedIn', [args[0]])));

    def('authLogout', native(1, ([toMsg]) => {
      // Optimistic logout: dispatch the result message IMMEDIATELY
      // and fire the server request in the background as
      // fire-and-forget. This is what production webapps
      // (Gmail / Slack / Twitter) do — logout is fundamentally
      // a UI gesture, not a transaction. Waiting for the server
      // to clear its session before the user sees ANY response
      // means a slow network leaves the operator stuck on a
      // protected page with no feedback ("did my tap register?").
      //
      // Trade-off: if the server never receives the POST (network
      // dropped, server down), the session cookie remains valid
      // server-side until it expires naturally. The client UI has
      // already moved to the sign-in page; if the user reloads,
      // Page.protected's /_auth/whoami check would see the still-
      // valid cookie and route them back to the protected page.
      // Acceptable in practice: rare network failure + rare
      // reload-immediately-after-logout combo. The operator
      // explicitly asked for this trade in favor of responsive UX.
      //
      // We always dispatch Ok(()) — the request's HTTP outcome is
      // irrelevant to the user's intent. The .catch on the fetch
      // just swallows the error so it doesn't surface in console.
      return VEffect(() => {
        fetch('/_auth/logout', {
          method: 'POST',
          credentials: 'same-origin',
        }).catch(() => { /* fire-and-forget: server-side cleanup is best-effort */ });
        const msg = apply(toMsg, VCtor('Ok', [VUnit()]));
        if (currentDispatch) currentDispatch(msg);
      }, 'authLogout');
    }));
    def('Auth.logout', envLookup(env, 'authLogout'));

    def('authMe', native(1, ([toMsg]) => {
      // GET /_auth/whoami — Maybe user.
      return VEffect(() => {
        fetch('/_auth/whoami', { credentials: 'same-origin' })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t })))
          .then(r => {
            if (!r.ok) {
              const msg = apply(toMsg, VCtor('Err', [VString('HTTP ' + r.body)]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            let parsed;
            try {
              const raw = JSON.parse(r.body);
              parsed = raw == null
                ? VCtor('Nothing', [])
                : VCtor('Just', [jsToMar(raw)]);
            } catch (e) {
              const msg = apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'authMe');
    }));
    def('Auth.me', envLookup(env, 'authMe'));

    def('httpPost', native(3, ([url, body, toMsg]) => {
      return VEffect(() => {
        fetch(url.s, { method: 'POST', body: body.s })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'httpPost');
    }));
    def('Http.post', envLookup(env, 'httpPost'));

    return env;
  }

  // ---------- DOM rendering ----------
  //
  // Two-phase: createDOM builds a fresh element from a VView; patchDOM
  // diffs an existing DOM node against a new VView and applies the
  // minimal mutations.
  //
  // Listeners use the indirect-view trick: each interactive element gets
  // its current VView stashed at `node.__marView`. The listener reads the
  // latest msg from there, so on patch we can update the view reference
  // without removing/re-adding listeners.

  function setMarView(node, view) {
    node.__marView = view;
  }

  // Attach a listener once. Uses node.__marView so the closure reads the
  // latest view after subsequent patches. Respects the `disabled` attr
  // (so a re-render that toggled the flag suppresses the next click
  // even though the listener is still bound).
  function attachClickDispatcher(node) {
    node.addEventListener('click', (ev) => {
      ev.preventDefault();
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return;
      if (isDisabled(v)) return;
      currentDispatch(v.msg);
    });
  }

  // getAttr reads a named attribute's value from a view, returning
  // null when absent. Generic helper for attrs whose value is a
  // record / object (not just a primitive). Used by onMove which
  // packs { editing, handler } into the attr value.
  function getAttrRaw(view, name) {
    if (!view || !view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name === name) return a.value;
    }
    return null;
  }

  // dispatchOnMove applies a Mar `(Int -> Int -> msg)` handler to
  // the (from, to) indices and pushes the resulting Msg through
  // the current MVU dispatcher.
  //
  // Used by the list reorder gesture — both the mouse/touch drag
  // (attachListReorderDrag) and the keyboard grab (Padrão 2 in
  // attachListReorderKeyboard, P6 below).
  function dispatchOnMove(handler, from, to) {
    if (!handler || from === to || !currentDispatch) return;
    try {
      const partial = apply(handler, VInt(from));
      const msg = apply(partial, VInt(to));
      currentDispatch(msg);
    } catch (e) {
      console.error('[mar] onMove dispatch failed:', e);
    }
  }

  // attachListReorderDrag wires up the mouse/touch drag gesture on a
  // `mar-list` element in edit mode.
  //
  // Behavior:
  //   - Each child row gets a drag handle `<button.mar-drag-handle>`
  //     prepended on the right side (added in createDOM when the
  //     list has an onMove attr with editing=true).
  //   - Pointer-down on a handle "lifts" the row (CSS class for
  //     shadow + elevation), records starting index.
  //   - Pointer-move calculates which row the cursor is currently
  //     over (by comparing Y position to each row's mid-point),
  //     showing a thin blue drop indicator line above that row.
  //   - Pointer-up: if the cursor is over a different row,
  //     dispatch onMove(start, target); reset all visual state.
  //   - Escape key or pointer-cancel: cancel without dispatching.
  //
  // Realtime visual reorder: as the cursor moves over rows, those
  // rows shift in place to "open up" the drop slot — no separate
  // drop indicator needed (the visible gap IS the indicator). This
  // mirrors iOS table-view drag where the list reflows continuously
  // under the finger rather than waiting for the release.
  function attachListReorderDrag(listEl, handler) {
    let dragging = null;
    const rows = () => Array.from(listEl.children).filter(c =>
      !c.classList.contains('mar-drop-indicator') &&
      !c.classList.contains('mar-live-region'));

    function onPointerDown(ev) {
      // Only react to the drag handle, not the whole row — clicking
      // text or other content shouldn't start a drag.
      const handle = ev.target.closest('.mar-drag-handle');
      if (!handle) return;
      const rowEl = handle.closest('.mar-section-body > *');
      if (!rowEl || rowEl.parentNode !== listEl) return;

      ev.preventDefault();
      const rs = rows();
      const startIdx = rs.indexOf(rowEl);
      if (startIdx < 0) return;

      // Attach pointermove/up/cancel listeners on DOCUMENT (not
      // listEl) and DON'T use setPointerCapture. This is the
      // "modern drag pattern" used by dnd-kit / react-dnd / etc.:
      //
      //   - Document-level listeners receive pointer events
      //     anywhere on the page, so the user can drag past the
      //     section's bounds without us losing the events. Same
      //     benefit setPointerCapture would give, but without the
      //     capture's iOS Safari side effects (a long-debugged
      //     class of bugs where the gesture state machine doesn't
      //     fully release on pointerup, leaving the page in a
      //     "next tap is consumed" state — operator reported
      //     EXACTLY this symptom after every drag: "any button
      //     needs to be tapped twice; tapping somewhere first
      //     releases it").
      //
      //   - Listeners are added per-drag and removed on
      //     pointerup/cancel. No state outlives the drag. The
      //     page is back to clean after release — same as if no
      //     drag ever happened.
      //
      // Bound once here so add/remove use the same function
      // reference; closures over `dragging` capture the same
      // state the previous implementation had.
      document.addEventListener('pointermove', onPointerMove);
      document.addEventListener('pointerup', onPointerUp);
      document.addEventListener('pointercancel', onPointerCancel);

      rowEl.classList.add('mar-row-dragging');
      // The drag-active class on the list enables a CSS transition
      // on non-dragging siblings (transform 200ms ease-out). Setting
      // transforms on them in onPointerMove then animates smoothly
      // — without this class the shifts would snap on each cursor
      // tick and feel jittery. See the CSS rule for the full pair.
      listEl.classList.add('mar-section-drag-active');

      // Snapshot every row's natural geometry BEFORE any shift is
      // applied. We use this snapshot for two things in onPointerMove:
      //
      //   (a) Target-row hit testing. If we used live
      //       getBoundingClientRect, the rects would reflect the
      //       realtime shifts we ourselves applied — moving the
      //       cursor 10px would cause a chain reaction (shift → new
      //       rect → new target → new shift → ...) and the drop
      //       slot would oscillate.
      //
      //   (b) The dragged row's natural height, which is the offset
      //       OTHER rows shift by ("make room for one row"). We
      //       pull it from the snapshot rather than measure live so
      //       the value is stable across the gesture.
      const rowGeometry = rs.map(r => {
        const rect = r.getBoundingClientRect();
        return { el: r, top: rect.top, height: rect.height };
      });

      dragging = {
        rowEl,
        startIdx,
        lastTargetIdx: startIdx,
        pointerId: ev.pointerId,
        startPointerY: ev.clientY,
        rowGeometry,
      };
    }

    function onPointerMove(ev) {
      if (!dragging) return;
      ev.preventDefault();

      // Translate the dragged row to follow the cursor. The delta is
      // relative to where the pointer was at drag-start, so the row
      // moves 1:1 with the finger / mouse. We compose the translate
      // with a tiny scale-up (1.02) for the "picked up" feel that
      // iOS uses on table-view drag — gives clear visual signal that
      // THIS row is the one being moved, beyond the shadow alone.
      const deltaY = ev.clientY - dragging.startPointerY;
      dragging.rowEl.style.transform = 'translateY(' + deltaY + 'px) scale(1.02)';

      // Find the conceptual drop slot using PRE-DRAG geometry (not
      // live rects — see the snapshot rationale in onPointerDown).
      // We do NOT skip the dragging row's entry in geom: including it
      // lets a cursor on the dragging row's own slot resolve to
      // startIdx (no-op), which is the correct UX for "small drag
      // that doesn't cross a neighbor's midpoint". The previous skip
      // caused the loop to leap over startIdx and bias the target
      // one slot toward the cursor direction, producing wrong drops
      // when dragging from the middle.
      //
      // Default = geom.length (the slot AFTER the last row) so a
      // cursor below every midpoint resolves to "drop at the very
      // end". Combined with the effectiveTarget adjustment below,
      // this keeps the end-of-list drop reachable for DOWN drags.
      const y = ev.clientY;
      const geom = dragging.rowGeometry;
      let target = geom.length;
      for (let i = 0; i < geom.length; i++) {
        if (y < geom[i].top + geom[i].height / 2) {
          target = i;
          break;
        }
      }
      // listMove removes `from` first then inserts at `to` in the
      // POST-REMOVAL list. For UP drags (target <= startIdx) the
      // slots above startIdx are unaffected by the removal so target
      // is dispatched as-is. For DOWN drags (target > startIdx)
      // every slot after startIdx shifts up by one when from is
      // removed, so the dispatched index is one less than the
      // conceptual drop slot — without this -1 a drag that should
      // land "above row N" instead lands BELOW row N.
      const startIdx = dragging.startIdx;
      const effectiveTarget =
        target > startIdx ? target - 1 : target;

      // Skip the shift recalc if the resolved target hasn't changed
      // since the last move — the rows are already in the right
      // positions and writing the same inline styles would just
      // churn CSSOM.
      if (effectiveTarget === dragging.lastTargetIdx) return;
      dragging.lastTargetIdx = effectiveTarget;

      // Realtime shifts on OTHER rows. Each row between the dragged
      // row's home slot and the current target slot translates by
      // ±rowHeight to open up the landing space. The combined
      // effect: the user sees the list "reflowing" under their
      // finger, the gap moving with them, no separate drop indicator
      // needed.
      //
      // Indexed against effectiveTarget (not the raw cursor-side
      // target) so the visual gap matches where listMove will
      // actually drop the row. Using raw target would put the gap
      // one row off for DOWN drags.
      const rowHeight =
        geom.find(g => g.el === dragging.rowEl).height;
      for (let i = 0; i < geom.length; i++) {
        const r = geom[i].el;
        if (r === dragging.rowEl) continue;
        let shift = 0;
        if (effectiveTarget > startIdx && i > startIdx && i <= effectiveTarget) {
          shift = -rowHeight;
        } else if (effectiveTarget < startIdx && i >= effectiveTarget && i < startIdx) {
          shift = rowHeight;
        }
        r.style.transform = shift === 0
          ? ''
          : 'translateY(' + shift + 'px)';
      }
    }

    function onPointerUp(ev) {
      if (!dragging) return;
      // No preventDefault here — drop the pointerup default
      // suppression. Modern drag-and-drop libraries (dnd-kit,
      // react-dnd) intentionally don't preventDefault on pointerup
      // because it can interfere with how iOS Safari processes the
      // touch-sequence end (including the synthetic click that
      // should — or shouldn't — fire). The drag itself has already
      // happened; nothing useful to suppress at this point.
      const { startIdx, lastTargetIdx, rowEl, rowGeometry } = dragging;

      // Detach the document-level listeners we attached in
      // onPointerDown. This is the cleanup that pointer-capture
      // would have done implicitly (per spec) — but doing it
      // ourselves means no dependency on the browser's release
      // implementation. iOS Safari's broken implicit-release was
      // the original "Done needs 2 taps after drag" bug; this
      // structure sidesteps that entirely.
      document.removeEventListener('pointermove', onPointerMove);
      document.removeEventListener('pointerup', onPointerUp);
      document.removeEventListener('pointercancel', onPointerCancel);


      // FLIP drop animation — slide the dragged row smoothly from
      // where the cursor released it to its final slot in the list,
      // instead of snapping. Without this the row jumps wherever
      // the reorder puts it, which reads as glitchy after the
      // smooth follow-cursor drag.
      //
      // The OTHER rows DO NOT need a FLIP: their drag-time visual
      // positions already match their post-reorder natural
      // positions (the realtime shifts were rehearsing the final
      // layout). So we clear their transforms synchronously and
      // they stay visually still — the dispatch + DOM-reorder +
      // transform-clear all happen in one sync block, so the
      // browser paints a single coherent frame: rows in their new
      // DOM slots, no transforms, no visible motion.
      //
      // Step-by-step (dragged row only):
      //
      //   1. FIRST: snapshot the dragged row's visual position
      //      (cursor pos, with follow-cursor transform applied).
      //   2. Disable transitions on all rows so the cleanups below
      //      don't accidentally animate things we want instant.
      //   3. Clear transforms on all rows. Dragged row briefly
      //      snaps to its OLD-slot natural — invisible because we
      //      stay in the sync block. Other rows stay put visually
      //      (their drag-shift == post-reorder natural).
      //   4. Dispatch reorder → MVU + render moves the dragged row
      //      to its new DOM slot synchronously.
      //   5. LAST: measure the dragged row's NEW natural position.
      //   6. INVERT: re-apply a transform that visually puts the
      //      dragged row back at the cursor's spot.
      //   7. void offsetHeight forces a sync layout so the browser
      //      commits the inverted state before the transition
      //      target is set — without it Chrome may collapse the
      //      next two style writes into a no-op.
      //   8. PLAY: next frame, clear the transform. The transition
      //      we set in step 7 animates from inverted (cursor) to
      //      natural (final slot).

      const cursorRect = rowEl.getBoundingClientRect();

      // Step 2: disable transitions everywhere so the cleanups
      // below don't kick off animations.
      for (const g of rowGeometry) {
        g.el.style.transition = 'none';
      }

      // Step 3: clear all inline transforms. Other rows' visual
      // positions are unaffected (their drag-shifted position ==
      // their post-reorder natural position). The dragged row
      // would visually snap to its OLD slot if the browser painted
      // here — but it doesn't, because we stay sync until step 6.
      for (const g of rowGeometry) {
        g.el.style.transform = '';
      }

      listEl.classList.remove('mar-section-drag-active');
      dragging = null;

      // Step 4: trigger the model update + re-render.
      if (lastTargetIdx !== startIdx) {
        dispatchOnMove(handler, startIdx, lastTargetIdx);
      }

      // Step 5: dragged row is now at its final slot. Measure.
      const finalRect = rowEl.getBoundingClientRect();
      const dx = cursorRect.left - finalRect.left;
      const dy = cursorRect.top - finalRect.top;

      // Restore inline transition='' on other rows so future drags
      // can re-enable the realtime-shift transition via the
      // .mar-section-drag-active class. Leaving inline 'none' on
      // them would override the class rule and break the next
      // drag.
      for (const g of rowGeometry) {
        if (g.el !== rowEl) g.el.style.transition = '';
      }

      // If the dragged row's cursor position already coincides
      // with its final slot (rare — pointer didn't move much),
      // skip the FLIP. Nothing to animate.
      if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) {
        rowEl.classList.remove('mar-row-dragging');
        rowEl.style.transition = '';
        rowEl.style.transform = '';
        return;
      }

      // Step 6: invert — put the dragged row back where the
      // cursor was. scale(1.02) lift carries through the
      // animation so the row "lands" by shrinking to natural
      // size + sliding simultaneously (iOS Reminders feel).
      rowEl.style.transform = 'translate(' + dx + 'px, ' + dy + 'px) scale(1.02)';
      // Step 7: force layout flush so the inverted state is
      // committed before we declare the transition.
      void rowEl.offsetHeight;
      rowEl.style.transition = 'transform 260ms cubic-bezier(0.2, 0.9, 0.3, 1)';

      // Step 8: play — clear transform on next frame to trigger
      // the transition. rAF ensures the inverted state was
      // painted at least once before the animation starts.
      requestAnimationFrame(() => {
        rowEl.style.transform = '';
        const cleanup = () => {
          rowEl.classList.remove('mar-row-dragging');
          rowEl.style.transition = '';
          rowEl.style.transform = '';
          rowEl.removeEventListener('transitionend', cleanup);
        };
        rowEl.addEventListener('transitionend', cleanup);
      });
    }

    function onPointerCancel() {
      if (!dragging) return;
      const { rowEl, rowGeometry } = dragging;
      // Detach document-level listeners (mirror of onPointerUp's
      // cleanup) — see onPointerDown for the architecture.
      document.removeEventListener('pointermove', onPointerMove);
      document.removeEventListener('pointerup', onPointerUp);
      document.removeEventListener('pointercancel', onPointerCancel);
      rowEl.classList.remove('mar-row-dragging');
      rowEl.style.transform = '';
      rowEl.style.transition = '';
      // Reset shifts on other rows too.
      for (const g of rowGeometry) {
        g.el.style.transform = '';
        g.el.style.transition = '';
      }
      listEl.classList.remove('mar-section-drag-active');
      dragging = null;
    }

    // Only pointerdown stays on listEl — that's the gesture-start
    // detector (needs to fire when the user touches the drag
    // handle inside this section). The move / up / cancel
    // listeners get attached to DOCUMENT inside onPointerDown so
    // they catch events anywhere on the page during the drag, and
    // are detached on pointerup/cancel — no state outlives the
    // gesture (the iOS Safari "stuck capture" bug was caused by
    // listeners + setPointerCapture not unwinding cleanly).
    listEl.addEventListener('pointerdown', onPointerDown);
  }

  // makeDragHandle creates the `≡` handle element prepended to each
  // row in a reorderable list when edit mode is active. Pure DOM
  // factory; the listener attachment is on the LIST element (event
  // delegation) — see attachListReorderDrag.
  function makeDragHandle() {
    const h = document.createElement('button');
    h.type = 'button';
    h.className = 'mar-drag-handle';
    h.setAttribute('aria-label', 'Drag to reorder');
    // Three short horizontal bars — iOS-style drag affordance.
    for (let i = 0; i < 3; i++) {
      const bar = document.createElement('span');
      bar.className = 'mar-drag-handle-bar';
      h.appendChild(bar);
    }
    return h;
  }

  // ensureRowEditAffordances guarantees a row inside an editing
  // section carries its drag handle + ARIA wiring, idempotently.
  //
  // Why this exists: the section's createDOM appends a handle to
  // every child during the initial render. BUT keyed reconciliation
  // can create new row DOM via createDOM(rowView) in two places —
  //
  //   (a) patchChildrenKeyed when newKey isn't in oldByKey (a row
  //       added in edit mode, e.g. via the Add Task button), and
  //   (b) patchDOM's tag-mismatch replacement (toggle → hstack on
  //       edit-mode entry, or any future shape-shift)
  //
  // — and neither path goes through the section's createDOM loop,
  // so the fresh row would be missing its handle. Calling this
  // helper from those creation sites (and re-running it on the
  // section's patch path) makes the handle's presence robust to
  // however the row landed in the DOM.
  //
  // Idempotent: bails if a handle is already present, so the cost
  // on the common case (row was created with handle) is one
  // querySelector.
  function ensureRowEditAffordances(rowEl, posIndex, totalCount) {
    if (rowEl.querySelector(':scope > .mar-drag-handle') == null) {
      rowEl.appendChild(makeDragHandle());
    }
    if (!rowEl.hasAttribute('tabindex')) rowEl.setAttribute('tabindex', '0');
    if (!rowEl.hasAttribute('role')) rowEl.setAttribute('role', 'listitem');
    if (!rowEl.hasAttribute('aria-grabbed')) rowEl.setAttribute('aria-grabbed', 'false');
    // posinset / setsize ALWAYS get refreshed — these change on
    // every reorder / insert / delete, and stale values would
    // mislead screen readers.
    rowEl.setAttribute('aria-posinset', String(posIndex + 1));
    rowEl.setAttribute('aria-setsize', String(totalCount));
    // Position-based classes drive the CSS that rounds the focus
    // outline / grabbed bg at the section card's outer corners. Set
    // here (not via CSS :first-child / :last-child) because the
    // section body has non-row children too (mar-live-region for
    // ARIA, mar-drop-indicator during active drag) that would
    // confuse positional selectors. CSS gets a definitive boolean
    // from JS, where we already know the true row count.
    rowEl.classList.toggle('mar-row-first', posIndex === 0);
    rowEl.classList.toggle('mar-row-last', posIndex === totalCount - 1);
  }

  // attachRowDeleteAffordance — appends the per-row delete button
  // (iOS-edit-mode minus circle on the row's left). Only renders
  // when the row is in edit mode: web's normal mode shows just the
  // primary affordance (toggle / link) without a destructive option
  // — the user enters Edit explicitly to do CRUD on the list.
  //
  // We considered the iCloud-Web hover-reveal pattern but it
  // conflicts with apps that have a separate Edit mode (the
  // canonical Mar shape): hovering rows in browse mode then
  // surfaces a destructive action the user hasn't asked for, on
  // top of the toggle they're trying to click. Edit-mode-only is
  // less ambiguous and matches the iOS Mail/Notes/Reminders
  // ergonomics exactly.
  //
  // The click handler dispatches `handler(idx)` — the framework's
  // standard apply-then-dispatch — so the Mar app's `update` sees
  // a `Msg.SomeDelete idx` and decides what to do (typically:
  // remove from the local model + Service.call to persist +
  // surface a toast with Undo).
  //
  // Idempotent: re-rendering a row that already has the button
  // reuses the existing DOM node + listener (no stacking). When
  // `editing=false`, any leftover button from a previous render
  // gets removed, so flipping editing mode off cleans up after
  // itself.
  function attachRowDeleteAffordance(rowEl, posIndex, handler, isEditing) {
    let btn = rowEl.querySelector(':scope > .mar-row-delete');
    if (!isEditing) {
      // Browse mode — remove any stale button from a previous
      // render that had editing=true. This is the cleanup leg of
      // toggling Edit → Done while the same DOM node persists.
      if (btn) btn.remove();
      return;
    }
    if (!btn) {
      btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'mar-row-delete';
      btn.setAttribute('aria-label', 'Delete');
      // Minus glyph as SVG so it stays crisp on retina without a
      // font dependency. A 4-unit-tall bar in a 24-unit viewBox
      // renders ~2.3px tall at the 14×14 SVG size — clearly
      // visible against the red disc. The previous 2-unit bar
      // disappeared to roughly 1px on standard DPI screens.
      btn.innerHTML =
        '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
        '<rect x="5" y="10" width="14" height="4" rx="2" fill="currentColor"/>' +
        '</svg>';
      btn.addEventListener('click', async (ev) => {
        ev.stopPropagation();
        ev.preventDefault();

        const dispatch = () => {
          try {
            const idxV = VInt(parseInt(btn.dataset.idx || '0', 10));
            const msg = apply(handler, idxV);
            if (currentDispatch) currentDispatch(msg);
          } catch (e) {
            console.error('[mar] onDelete dispatch failed:', e);
          }
        };

        // iOS Mail / Notes swipe-delete animation, played in two
        // beats so neither motion clips the other:
        //
        //   1. (0 -> 0.45) the row's contents fade out and slide
        //      ~40px left while the row keeps its full height. The
        //      horizontal motion gets to finish instead of being
        //      cut short.
        //   2. (0.45 -> 1) the now-empty row collapses its height,
        //      padding, and border to zero, so the rows below slide
        //      up to close the gap.
        //
        // Doing both at once is what made it feel truncated: with
        // `overflow: hidden`, the collapsing height swallowed the
        // leftward slide about halfway through, so the row appeared
        // to just snap shut. Separating the beats (and giving the
        // whole thing a touch more time) reads as one fluid motion.
        //
        // We hold the dispatch until the animation finishes so
        // the patcher doesn't yank the DOM node out from under
        // us. Cost is a ~340ms delay before the network round
        // trip, acceptable for a destructive action where the
        // visual confirmation is the primary feedback.
        //
        // Fallback path: if Element.animate isn't available
        // (very old browser, JSDOM in tests), we skip the
        // animation and dispatch immediately so the action
        // still works.
        const r = ev.currentTarget.closest('.mar-section-body > *');
        if (!r || typeof r.animate !== 'function') {
          dispatch();
          return;
        }

        // Capture the row\'s current concrete dimensions so the
        // first keyframe is "this exact size" rather than the
        // implicit 'auto' value, which can\'t interpolate.
        const cs = getComputedStyle(r);
        const h  = r.offsetHeight;
        const pt = cs.paddingTop;
        const pb = cs.paddingBottom;
        const bb = cs.borderBottomWidth;

        // `overflow: hidden` keeps the leaving content from
        // bleeding into the row below as height shrinks. The
        // style attribute survives the animation (Web Animations
        // API doesn\'t clear it).
        r.style.overflow = 'hidden';

        try {
          await r.animate([
            // Beat 1 start: full size, fully visible.
            {
              offset: 0,
              height: h + 'px',
              opacity: 1,
              transform: 'translateX(0)',
              paddingTop: pt,
              paddingBottom: pb,
              borderBottomWidth: bb,
              easing: 'cubic-bezier(0.4, 0, 0.2, 1)',
            },
            // Beat 1 end: content has slid left + faded, but the row
            // still occupies its full height (so the slide isn't
            // clipped by a shrinking box).
            {
              offset: 0.45,
              height: h + 'px',
              opacity: 0,
              transform: 'translateX(-40px)',
              paddingTop: pt,
              paddingBottom: pb,
              borderBottomWidth: bb,
              easing: 'cubic-bezier(0.4, 0, 0.2, 1)',
            },
            // Beat 2: the empty row collapses, closing the gap.
            {
              offset: 1,
              height: '0px',
              opacity: 0,
              transform: 'translateX(-40px)',
              paddingTop: '0px',
              paddingBottom: '0px',
              borderBottomWidth: '0px',
            },
          ], {
            duration: 340,
            fill: 'forwards',
          }).finished;
        } catch (_) {
          // Animation can be cancelled if the node is removed
          // from the DOM mid-play (e.g., a programmatic
          // re-render races with the user click). Ignore and
          // proceed to dispatch — the row is going away either
          // way.
        }

        dispatch();
      });
      rowEl.appendChild(btn);
    }
    // Refresh per-render: the index shifts when rows above are
    // inserted/removed. Stored on data-idx so the click handler
    // (which closes over `btn` but not the current idx) reads it
    // fresh at click time.
    btn.dataset.idx = String(posIndex);
  }

  // attachListReorderKeyboard — Padrão 2 (WAI-ARIA grab + arrow
  // keys) for keyboard / screen-reader users. Invisible to mouse
  // users (no extra UI), discoverable via Tab focus + screen
  // reader announcements.
  //
  // Flow:
  //   1. Each row in edit mode is focusable (tabindex="0",
  //      role="listitem", aria-label="<name>, item N of M").
  //   2. Space or Enter on a focused row → enter "grabbed" state.
  //      Visual focus ring stays; aria-grabbed="true".
  //   3. ArrowUp / ArrowDown while grabbed → reorder live (moves
  //      the DOM node, updates aria-posinset).
  //   4. Space or Enter again → drop (fires onMove from original
  //      idx to current idx).
  //   5. Escape → cancel (revert the live moves, fire nothing).
  //
  // Live position announcements go through a single
  // <div aria-live="polite"> attached to the list.
  function attachListReorderKeyboard(listEl, handler, liveHost) {
    const rows = () => Array.from(listEl.children).filter(c =>
      !c.classList.contains('mar-drop-indicator') &&
      !c.classList.contains('mar-live-region'));

    // One shared live region per list — assistive tech reads
    // updates as polite announcements.
    //
    // Hosted on `liveHost` (the section wrapper) — NOT on `listEl`
    // (the section-body) — because `.mar-section-body > *` applies
    // padding (10px 0), min-height (22px), and border-bottom (0.5px)
    // to every direct child. Even though the live region is
    // `position: absolute` (out of flow, so it doesn\'t push siblings),
    // appending it as the last child of the body kicks the previous
    // actual last row out of `:last-child` — gaining a hairline
    // border, and visually nudging the section card\'s end by enough
    // to read as a "phantom extra row" the moment Edit is toggled.
    // Hosting on the section wrapper sidesteps all of that — the
    // wrapper has no descendant rules to inherit.
    const host = liveHost || listEl;
    let live = host.querySelector(':scope > .mar-live-region');
    if (!live) {
      live = document.createElement('div');
      live.className = 'mar-live-region';
      live.setAttribute('aria-live', 'polite');
      live.setAttribute('aria-atomic', 'true');
      host.appendChild(live);
    }
    function announce(msg) {
      // Clearing first forces re-announcement when the same text
      // would otherwise repeat (e.g., consecutive arrow-up presses).
      live.textContent = '';
      // setTimeout 0 = next microtask, gives the screen reader
      // time to register the clear before the new text.
      setTimeout(() => { live.textContent = msg; }, 0);
    }

    let grabbed = null; // { rowEl, startIdx }

    // onGrabbedBlur cancels the grab when focus leaves the grabbed
    // row. Without this, pressing Tab while grabbed creates a
    // "zombie" state: the row stays styled as `.mar-row-grabbed`
    // (dark blue fill) AND another row picks up the browser's
    // `:focus-visible` ring (blue outline) — two rows look
    // "selected", subsequent Space presses can't resolve which
    // row to act on, and arrow keys move the grabbed (invisible-
    // to-the-user attention) instead of the focused one.
    //
    // We defer the check to a microtask via setTimeout(…, 0):
    // some browsers fire transient blur/focus pairs when a node
    // is moved via insertBefore (the very thing moveGrabbed does
    // on each arrow keypress). By the time the microtask runs,
    // focus has settled. If it landed back on the grabbed row,
    // we keep the grab. If it landed elsewhere, the user
    // intentionally shifted attention — cancel.
    function onGrabbedBlur() {
      setTimeout(() => {
        if (!grabbed) return;
        if (document.activeElement === grabbed.rowEl) return;
        endGrab(false);
      }, 0);
    }

    function startGrab(rowEl) {
      const idx = rows().indexOf(rowEl);
      if (idx < 0) return;
      grabbed = { rowEl, startIdx: idx };
      rowEl.setAttribute('aria-grabbed', 'true');
      rowEl.classList.add('mar-row-grabbed');
      rowEl.addEventListener('blur', onGrabbedBlur);
      announce('Grabbed item ' + (idx + 1) + ' of ' + rows().length + '. Use up and down arrows to move.');
    }

    function endGrab(commit) {
      if (!grabbed) return;
      const { rowEl, startIdx } = grabbed;
      const currentIdx = rows().indexOf(rowEl);
      rowEl.setAttribute('aria-grabbed', 'false');
      rowEl.classList.remove('mar-row-grabbed');
      rowEl.removeEventListener('blur', onGrabbedBlur);
      grabbed = null;
      if (commit && currentIdx !== startIdx) {
        // Fire the handler. The model update will cause a re-
        // render that "officially" places the row; our local DOM
        // moves were just preview.
        dispatchOnMove(handler, startIdx, currentIdx);
        announce('Moved to position ' + (currentIdx + 1) + ' of ' + rows().length + '.');
      } else if (commit) {
        announce('Dropped in place.');
      } else {
        announce('Move cancelled.');
      }
    }

    function moveGrabbed(delta) {
      if (!grabbed) return;
      const rs = rows();
      const idx = rs.indexOf(grabbed.rowEl);
      const target = Math.max(0, Math.min(rs.length - 1, idx + delta));
      if (target === idx) return;
      // Move DOM live — gives instant visual + screen-reader
      // feedback. The final model update happens on drop.
      const anchor = (delta > 0)
        ? (rs[target].nextSibling || null)
        : rs[target];
      listEl.insertBefore(grabbed.rowEl, anchor);
      // Refresh position-driven classes + aria attrs on EVERY row,
      // not just the grabbed one. `mar-row-first` / `mar-row-last`
      // drive the rounded-corner border-radius (18px at the card's
      // outer corners vs 8px between rows). insertBefore moves the
      // grabbed row but leaves these classes stale on it AND on the
      // rows it traded places with — so the focused outline shows
      // an 8px corner where the card has an 18px curve, visible as
      // a "broken" ring at the top or bottom row.
      const afterMove = rows();
      afterMove.forEach((r, i) => {
        r.classList.toggle('mar-row-first', i === 0);
        r.classList.toggle('mar-row-last', i === afterMove.length - 1);
        r.setAttribute('aria-posinset', String(i + 1));
        r.setAttribute('aria-setsize', String(afterMove.length));
      });
      // Restore focus to the moved row. Browsers vary: some
      // preserve focus across insertBefore on the active element,
      // some momentarily transfer it to `<body>` and the user-
      // agent never moves it back. In the latter case our
      // onGrabbedBlur handler sees `activeElement !== grabbed.rowEl`
      // and cancels the grab — so after one arrow press, the next
      // arrow / Space silently does nothing (the symptom the user
      // saw: "ArrowDown worked, ArrowUp did nothing"). Explicitly
      // re-focusing right after the move makes the gesture stable
      // across all browsers.
      grabbed.rowEl.focus();
      announce('Position ' + (target + 1) + ' of ' + afterMove.length + '.');
    }

    listEl.addEventListener('keydown', (ev) => {
      const rowEl = ev.target.closest('.mar-section-body > *');
      if (!rowEl || rowEl.parentNode !== listEl) return;
      if (rowEl.classList.contains('mar-drop-indicator') ||
          rowEl.classList.contains('mar-live-region')) return;

      switch (ev.key) {
        case ' ':
        case 'Enter':
          ev.preventDefault();
          if (grabbed) {
            // Commit the drop regardless of which row currently
            // has focus. The blur handler above should have
            // already ended the grab if focus left the grabbed
            // row — this is a defensive fallback for browsers /
            // edge cases where the blur didn't fire (e.g.,
            // focus moved via JS rather than user interaction).
            endGrab(true);
          } else {
            startGrab(rowEl);
          }
          break;
        case 'ArrowUp':
          if (grabbed) { ev.preventDefault(); moveGrabbed(-1); }
          break;
        case 'ArrowDown':
          if (grabbed) { ev.preventDefault(); moveGrabbed(1); }
          break;
        case 'Escape':
          if (grabbed) {
            ev.preventDefault();
            // Revert the live moves by putting the row back at
            // its starting index.
            const rs = rows();
            const currentIdx = rs.indexOf(grabbed.rowEl);
            if (currentIdx !== grabbed.startIdx) {
              const anchor = rs[grabbed.startIdx] || null;
              listEl.insertBefore(grabbed.rowEl, anchor);
            }
            endGrab(false);
          }
          break;
      }
    });
  }

  // Reads the `disabled` attr off a view; returns false when absent
  // or when the value isn't `true`. Renderer-side guard for buttons.
  function isDisabled(view) {
    if (!view || !view.attrs) return false;
    for (const a of view.attrs) {
      if (a.name === 'disabled' && a.value && a.value.k === 'B' && a.value.b) {
        return true;
      }
    }
    return false;
  }

  // Sync the DOM `disabled` property with the view's `disabled` attr.
  // Setting the property (not just the attribute) is what suppresses
  // hover/focus styling and makes the browser skip the click event.
  function applyDisabledAttr(node, view) {
    node.disabled = isDisabled(view);
  }

  // Sync disabled state onto an <a> — which doesn't have a native
  // `disabled` property. We set aria-disabled (assistive tech +
  // [aria-disabled=true] CSS selector hook) and toggle a class so
  // the stylesheet can fade the row + kill pointer events. The
  // delegated link-click handler additionally consults isDisabled()
  // before navigating, so keyboard activation (Enter on a focused
  // link) is also blocked.
  function applyAnchorDisabled(node, view) {
    const disabled = isDisabled(view);
    if (disabled) {
      node.setAttribute('aria-disabled', 'true');
      node.classList.add('mar-disabled');
      // Pull the link out of tab order while disabled. Without
      // this, Tab still lands on it (because we force tabindex=0
      // on every navigationLink), and Enter activates the
      // navigation — defeating the disabled state for keyboard
      // users. Returns to tabindex=0 when re-enabled below.
      node.setAttribute('tabindex', '-1');
    } else {
      node.removeAttribute('aria-disabled');
      node.classList.remove('mar-disabled');
      node.setAttribute('tabindex', '0');
    }
  }

  // applyInputKind reads the input-kind flag attrs (email / password
  // / newPassword / numeric) and translates them into the right HTML
  // attributes. Defaults to type=text when no kind attr is present.
  // Multiple input-kind attrs on one input — last one wins; the user
  // shouldn't combine them anyway.
  function applyInputKind(node, view) {
    let type = 'text';
    let autocomplete = null;
    let inputmode = null;
    let pattern = null;
    if (view.attrs) {
      for (const a of view.attrs) {
        switch (a.name) {
          case 'inputKindEmail':
            type = 'email'; autocomplete = 'email'; inputmode = 'email';
            break;
          case 'inputKindPassword':
            type = 'password'; autocomplete = 'current-password';
            break;
          case 'inputKindNewPassword':
            type = 'password'; autocomplete = 'new-password';
            break;
          case 'inputKindNumeric':
            inputmode = 'numeric'; pattern = '[0-9]*';
            break;
          case 'inputKindOneTimeCode':
            // `autocomplete="one-time-code"` activates iOS Mail's
            // "Code from Mail" autofill — when the user receives an
            // email with a recent code, Safari surfaces it as a
            // keyboard suggestion. Chrome's "Smart Lock" does the
            // same thing. Orthogonal to `UI.numeric` so a real OTP
            // field uses both: `[ numeric, oneTimeCode ]`.
            autocomplete = 'one-time-code';
            break;
          case 'inputKindNumericCode':
            // Convenience: bundles inputKindNumeric + inputKindOneTimeCode.
            // The UI.numericCode attr emits this single flag; the
            // renderer expands it. Matches the typical OTP / 2FA case
            // (numeric keypad on mobile + Code-from-Mail autofill).
            inputmode = 'numeric'; pattern = '[0-9]*';
            autocomplete = 'one-time-code';
            break;
        }
      }
    }
    // Only set type for <input>; <textarea> ignores it.
    if (node.tagName === 'INPUT') node.type = type;
    if (autocomplete) node.setAttribute('autocomplete', autocomplete);
    if (inputmode) node.setAttribute('inputmode', inputmode);
    if (pattern) node.setAttribute('pattern', pattern);
  }

  function attachInputDispatcher(node) {
    node.addEventListener('input', (ev) => {
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return;
      currentDispatch(apply(v.msg, VString(node.value)));
    });
  }

  // attachSubmitDispatcher wires the UI.submit attr (if present) to
  // Enter on text inputs.
  function attachSubmitDispatcher(node) {
    node.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Enter') return;
      const v = node.__marView;
      if (!currentDispatch || !v || !v.attrs) return;
      const submitAttr = v.attrs.find(a => a.name === 'submit');
      if (!submitAttr) return;
      // Textareas: only Cmd/Ctrl+Enter so the user can type newlines
      // freely. Inputs: any Enter.
      if (node.tagName === 'TEXTAREA' && !ev.metaKey && !ev.ctrlKey) return;
      ev.preventDefault();
      currentDispatch(submitAttr.value);
    });
  }

  function getAttr(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a ? a.value.s : '';
  }

  // applySizing reads `width` / `height` attrs (records with __unit
  // and amount fields, built by UI.chars / UI.lines) and applies them
  // to the DOM element. Both attrs are optional — sizing without them
  // falls back to the input's default (full-width, browser/CSS
  // default height).
  //
  // Padding constants here MUST stay in sync with the .mar-textfield
  // CSS rule (10px 14px) so the calc() math lands on a width that
  // actually fits N character columns visually.
  function applySizing(node, view) {
    const w = readLengthAttr(view, 'width');
    if (w && w.unit === 'chars') {
      // The width budget has to cover four things, in order:
      //
      //   - 28px horizontal padding (14px × 2 from .mar-textfield)
      //   - 2px border (1px × 2)
      //   - 2-4px slack for the caret + browser-specific scroll
      //     padding (Safari adds a few px when the caret sits at the
      //     trailing edge so it doesn't get clipped by the border)
      //   - the actual N×ch content area
      //
      // box-sizing on .mar-textfield is `border-box`, so max-width
      // INCLUDES padding + border — we have to bake them back in or
      // the content area ends up ~Nch - 30px wide instead of Nch.
      // Total constant: 28 + 2 + 4 = 34px.
      //
      // The accompanying `font-variant-numeric: tabular-nums` rule
      // in the CSS makes every digit (0-9) render at exactly `1ch`
      // wide. Without it, "888888" is ~5% wider than "111111" in
      // proportional fonts (system-ui, SF Pro), causing horizontal
      // scroll-flicker when the cursor passes near the trailing
      // edge of code/numeric inputs.
      node.style.maxWidth = 'calc(' + w.amount + 'ch + 34px)';
      // Opt the input out of the hstack flex:1 stretch rule so a
      // sized field beside a button doesn't get re-expanded by the
      // flex layout. `0 0 auto` = don't grow, don't shrink, use the
      // declared (max-)width.
      node.style.flex = '0 0 auto';
    }
    const h = readLengthAttr(view, 'height');
    if (h && h.unit === 'lines') {
      if (node.tagName === 'TEXTAREA') {
        // For <textarea>, the HTML-native `rows` attribute is the
        // right way to set initial height in lines. The user can
        // still drag the resize handle to grow it further (CSS rule
        // `resize: vertical` is preserved).
        node.setAttribute('rows', String(h.amount));
      }
    }
  }

  // applyImageAttrs reads image-only attrs and applies them to an
  // <img>: `size` (fixed width + height, each a px length-value built
  // by UI.size (px w) (px h)) and the content-mode flags (fit/fill →
  // object-fit contain/cover). All optional. Without `size` the image
  // fills its container width and keeps its aspect ratio (the
  // .mar-image CSS default). Default content mode is contain (no crop).
  function applyImageAttrs(node, view) {
    const sz = getAttrRaw(view, 'size');
    if (sz && sz.k === 'R' && sz.fields) {
      const w = readPxAmount(sz.fields.w);
      const h = readPxAmount(sz.fields.h);
      if (w != null) { node.style.width = w + 'px'; node.style.maxWidth = 'none'; }
      if (h != null) node.style.height = h + 'px';
    }
    if (getAttrRaw(view, 'contentModeCover')) node.style.objectFit = 'cover';
    else if (getAttrRaw(view, 'contentModeFit')) node.style.objectFit = 'contain';
  }

  // readPxAmount unwraps a px length-value record ({__unit:'px',
  // amount:N}) to its integer N, or null if malformed.
  function readPxAmount(v) {
    if (v && v.k === 'R' && v.fields && v.fields.amount && v.fields.amount.k === 'I') {
      return v.fields.amount.n;
    }
    return null;
  }

  // applyLayoutAttrs applies the UNIVERSAL sizing attrs — width /
  // height carrying chars / lines / fill Size values — to any view's
  // node. Runs from createDOM's shared tail and the patch path, so
  // swapping `width fill` ↔ `width (chars 12)` between renders stays
  // in sync (classList.toggle + unconditional style writes make it
  // idempotent).
  //
  // `fill` is contextual: the .mar-w-fill / .mar-h-fill classes
  // resolve against the parent's flex direction (main axis → grow,
  // cross axis → stretch), so the class IS the mechanism. chars /
  // lines become inline styles.
  //
  // Tags with their own sizing pipelines opt out of the style half:
  // inputs (textField / textArea / picker) keep applySizing's
  // padding-aware max-width + rows semantics, images size via the
  // px-based `size` attr. The fill classes still apply to them.
  function applyLayoutAttrs(node, view) {
    const w = readLengthAttr(view, 'width');
    const h = readLengthAttr(view, 'height');
    node.classList.toggle('mar-w-fill', !!(w && w.unit === 'fill'));
    node.classList.toggle('mar-h-fill', !!(h && h.unit === 'fill'));
    const ownSizing = view.tag === 'image' || view.tag === 'textField'
      || view.tag === 'textArea' || view.tag === 'picker' || view.tag === 'datePicker';
    if (ownSizing) return;
    node.style.width = (w && w.unit === 'chars') ? w.amount + 'ch' : '';
    node.style.height = (h && h.unit === 'lines') ? h.amount + 'lh' : '';
  }

  // applyAlignAttr maps a stack's `align` attr onto its cross axis
  // (align-items). Each stack honors only its own axis's values plus
  // center — vstack: leading/center/trailing, hstack: top/center/
  // bottom; a wrong-axis value is ignored rather than guessed. No
  // attr → the base-class default (vstack stretch, hstack center).
  // Align only places HUGGING children: a child with the matching
  // fill has no cross-axis slack (the per-child .mar-*-fill stretch
  // rule wins), so align is pure position, never size.
  function applyAlignAttr(node, view) {
    const wanted = getAttr(view, 'align');
    const valid = view.tag === 'vstack'
      ? ['leading', 'center', 'trailing']
      : ['top', 'center', 'bottom'];
    for (const v of ['leading', 'center', 'trailing', 'top', 'bottom']) {
      node.classList.toggle('mar-align-' + v, wanted === v && valid.includes(v));
    }
  }

  // readLengthAttr unwraps a sizing-attr value into { unit, amount }.
  // The Mar-side type system enforces the axis — `width` carries a
  // Size Width (chars N / fill), `height` a Size Height (lines N /
  // fill) — so the unit check here is defensive; a malformed value
  // just returns null.
  function readLengthAttr(view, name) {
    if (!view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name !== name) continue;
      const v = a.value;
      if (v && v.k === 'R' && v.fields) {
        const u = v.fields.__unit;
        const amt = v.fields.amount;
        if (u && u.k === 'S' && amt && amt.k === 'I') {
          return { unit: u.s, amount: amt.n };
        }
      }
      return null;
    }
    return null;
  }

  // toggleIsOn reads the `isOn` Bool attr off a UI.toggle view.
  // Defaults to false when missing so the DOM has a deterministic
  // starting state.
  function toggleIsOn(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'isOn');
    return !!(a && a.value && a.value.b);
  }

  // renderPickerOptions rebuilds the <option> children of a picker's
  // <select> from the view's `options` / `selected` / `toLabel` attrs.
  // Used by both createDOM (initial mount) and patchDOM (when the
  // user's update swaps the model's options or selected value).
  // Clears the select first so the patch path doesn't accumulate
  // stale entries when an option is removed.
  function renderPickerOptions(select, view) {
    while (select.firstChild) select.removeChild(select.firstChild);
    const optionsAttr = view.attrs.find(a => a.name === 'options');
    const selectedAttr = view.attrs.find(a => a.name === 'selected');
    const toLabelAttr = view.attrs.find(a => a.name === 'toLabel');
    if (!optionsAttr || !optionsAttr.value || optionsAttr.value.k !== 'L') return;
    if (!toLabelAttr || !toLabelAttr.value || toLabelAttr.value.k !== 'Fn') return;
    const options = optionsAttr.value.xs;
    const selected = selectedAttr && selectedAttr.value;
    let selectedIdx = -1;
    for (let i = 0; i < options.length; i++) {
      const opt = document.createElement('option');
      opt.value = String(i);
      const labelV = apply(toLabelAttr.value, options[i]);
      opt.textContent = (labelV && labelV.k === 'S') ? labelV.s : String(labelV);
      select.appendChild(opt);
      if (selected !== undefined && eqValues(options[i], selected)) {
        selectedIdx = i;
      }
    }
    // Set the select's value *after* options are appended — assigning
    // before they exist silently fails and the dropdown shows the
    // wrong default. -1 (no match) leaves the browser's default of
    // index 0 in place, which is the safest fallback when the model's
    // selected value isn't in the option list.
    if (selectedIdx >= 0) select.value = String(selectedIdx);
  }

  // buildNavigationChrome pulls navigationTitle / trailing / leading
  // attrs off a navigationStack view and returns the TWO chrome
  // nodes that precede the nav body, in slot order:
  //
  //   [0] <div.mar-nav-toolbar-row>  ← the PINNED bar. Auto-back /
  //       leading on the left, trailing on the right (glass pills,
  //       iOS 26 "Liquid Glass" toolbar pattern), and the inline
  //       title in the center, which fades in once the large title
  //       scrolls out from under it.
  //   [1] <header.mar-nav-bar>       ← the 32px bold large title.
  //
  // This mirrors iOS large-title navigation: the bar is pinned on
  // screen (position: sticky in CSS — back button and actions never
  // scroll away), while the large title is CONTENT and scrolls with
  // the page. When the large title disappears under the bar, the
  // bar's small centered title takes over — the same handoff iOS
  // does. Both nodes are always returned (display:none when empty)
  // so the create and patch paths can address slots positionally.
  function buildNavigationChrome(view) {
    const titleAttr = view.attrs && view.attrs.find(a => a.name === 'navigationTitle');
    const trailingAttr = view.attrs && view.attrs.find(a => a.name === 'topBarTrailing');
    const leadingAttr  = view.attrs && view.attrs.find(a => a.name === 'topBarLeading');
    const depth = currentNavDepth();
    // Back chevron is framework-owned and independent of any
    // user-provided topBarLeading content — it appears whenever
    // there's a previous page to return to. SwiftUI works the same
    // way: .navigationBarBackButtonHidden(true) is the explicit
    // opt-out, not the default consequence of setting toolbar
    // items. Without this, a sub-page that uses topBarLeading for
    // a custom button (Edit, Filter, ...) silently loses the
    // ability to navigate back, which was the bug that motivated
    // this whole refactor.
    const autoBack = depth > 0;
    const hasButtons = autoBack || !!leadingAttr || !!trailingAttr;

    // Slot 0: pinned toolbar row.
    const toolbarRow = document.createElement('div');
    toolbarRow.className = 'mar-nav-toolbar-row';
    if (!hasButtons && !titleAttr) {
      toolbarRow.style.display = 'none';
    } else {
      const left = document.createElement('div');
      left.className = 'mar-nav-side';
      // Order matters: back chevron first (closest to the leading
      // edge of the screen), custom topBarLeading after it. This
      // mirrors SwiftUI's automatic placement — toolbar items go
      // BESIDES the auto-injected back button, not in place of it.
      if (autoBack) {
        left.appendChild(buildBackButton());
      }
      if (leadingAttr) {
        left.appendChild(createDOM(leadingAttr.value));
      }
      toolbarRow.appendChild(left);

      // Inline title — hidden until the large title scrolls out
      // (wireNavInlineTitle toggles .mar-nav-scrolled). The large
      // title is the page's real heading; this is a visual echo,
      // so hide it from the accessibility tree.
      if (titleAttr) {
        const inline = document.createElement('div');
        inline.className = 'mar-nav-inline-title';
        inline.textContent = titleAttr.value.s;
        inline.setAttribute('aria-hidden', 'true');
        toolbarRow.appendChild(inline);
      }

      const right = document.createElement('div');
      right.className = 'mar-nav-side mar-nav-side-trailing';
      if (trailingAttr) right.appendChild(createDOM(trailingAttr.value));
      toolbarRow.appendChild(right);

      if (!hasButtons) {
        // Title-only page: the bar reserves no space at rest, so
        // the large title keeps its position; only the inline
        // title floats in once the page scrolls.
        toolbarRow.classList.add('mar-nav-toolbar-bare');
      } else if (!titleAttr) {
        // Buttons-only page: the row is the entire chrome, so it
        // owns the 24px gap to the body that the large-title
        // header normally provides.
        toolbarRow.classList.add('mar-nav-toolbar-solo');
      }
    }

    // Slot 1: large title. Scrolls away with the content, like iOS.
    const header = document.createElement('header');
    header.className = 'mar-nav-bar';
    if (titleAttr) {
      const titleRow = document.createElement('div');
      titleRow.className = 'mar-nav-title-row';
      const titleEl = document.createElement('div');
      titleEl.className = 'mar-nav-title';
      titleEl.textContent = titleAttr.value.s;
      titleRow.appendChild(titleEl);
      header.appendChild(titleRow);
    } else {
      header.style.display = 'none';
    }

    wireNavInlineTitle(toolbarRow, titleAttr ? header : null);
    return [toolbarRow, header];
  }

  // wireNavInlineTitle drives the large-title → inline-title
  // handoff: when the large-title header scrolls out of the strip
  // the pinned bar occupies, the bar's centered title fades in;
  // scroll back to the top and it fades out again. One observer per
  // chrome build, stored on the row — the patch path disconnects
  // the old row's observer explicitly, and rows dropped by full
  // page navigation take theirs to the garbage collector.
  function wireNavInlineTitle(toolbarRow, header) {
    if (!header || typeof IntersectionObserver === 'undefined') return;
    // Shrink the viewport's top by the strip the pinned bar
    // occupies, so the title only counts as "gone" once it has
    // fully slid under where the bar sits. The strip depends on
    // the bar's resting geometry: with buttons the row is 36px
    // tall below the stack's 24px top padding; on a bare
    // (title-only) page the row is zero-height, the title rests
    // at 24px, and using the larger strip would swallow the title
    // at rest and show the inline pill before any scrolling.
    const bare = toolbarRow.classList.contains('mar-nav-toolbar-bare');
    const obs = new IntersectionObserver(([entry]) => {
      toolbarRow.classList.toggle('mar-nav-scrolled', !entry.isIntersecting);
    }, {
      rootMargin: (bare ? '-28px' : '-64px') + ' 0px 0px 0px',
    });
    obs.observe(header);
    toolbarRow._marNavObserver = obs;
  }

  // buildBackButton returns the iOS 26-style circular glass pill
  // with just the chevron inside. No visible text — the previous
  // screen\'s title goes into the `title` attribute as a hover
  // tooltip + aria-label so the affordance is accessible without
  // taking horizontal space in the toolbar.
  function buildBackButton() {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mar-nav-back';
    const prev = (history.state && history.state.prevTitle) || '';
    btn.setAttribute('aria-label', prev ? 'Back to ' + prev : 'Back');
    if (prev) btn.setAttribute('title', 'Back to ' + prev);
    const chev = document.createElement('span');
    chev.className = 'mar-nav-back-chev';
    chev.textContent = '‹';
    btn.appendChild(chev);
    btn.addEventListener('click', () => {
      // Native browser back — fires popstate, which calls render()
      // with the previous URL + depth, triggering the "back"
      // view-transition direction.
      history.back();
    });
    return btn;
  }

  // currentNavDepth reads our SPA-internal "how deep into the
  // navigation stack are we" counter, stamped onto history.state
  // by every push / replace. Returns 0 when the entry has no state
  // yet (e.g. the very first page load before mountPages runs the
  // initial replace).
  function currentNavDepth() {
    return (history.state && typeof history.state.marDepth === 'number')
      ? history.state.marDepth
      : 0;
  }

  // Respect the OS-level "reduce motion" accessibility preference.
  // Users who set this don't want page-transition slide animations
  // — disable them entirely and just do the DOM swap.
  function prefersReducedMotion() {
    return window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }

  // ensureUIStyles injects the CSS for the UI.* vocabulary once. The
  // styling aims for the iOS Form/List card-list look on web — light
  // gray page background, white sections with rounded corners, gray
  // section headers, dividers between rows.
  function ensureUIStyles() {
    // Always replace the existing <style> with the current build's
    // content. Hot reload re-runs the IIFE but document.head's
    // <style> element survives the page lifecycle — without removing
    // and re-appending, edits to this CSS string wouldn't show up
    // in the browser until the user did a full reload.
    const old = document.getElementById('mar-ui-style');
    if (old) old.remove();
    const style = document.createElement('style');
    style.id = 'mar-ui-style';
    // Desktop-first design language. Avoids the iOS-card aesthetic
    // (rounded white cards on gray, big shadows, blue accents) in
    // favor of a flatter, wider layout closer to Linear / Notion /
    // modern dashboards: 960px content column, subtle borders instead
    // of cards, neutral grayscale, system font stack at 15-16px.
    // The same DSL still renders as proper iOS Forms on Swift —
    // only the WEB rendering picks the desktop idiom.
    style.textContent = [
      // System-flavored vocabulary: native system font stack,
      // generous space, pill-shaped buttons, near-black `#1d1d1f`
      // text, link-blue (#0071e3) accents, soft 0.5px dividers.
      // The page reads as content (the notes), not chrome.

      // height: 100% on html, min-height on body so the body's box
      // grows past the viewport when content is taller. Without
      // min-height (just height: 100%), the body stays exactly one
      // viewport tall: anything below the fold shows html's solid
      // background instead of the body's gradient, producing a
      // visible horizontal "seam" partway down long pages.
      'html { margin: 0; padding: 0; height: 100%; }',
      'body { margin: 0; padding: 0; min-height: 100%; }',
      // color-scheme tells the browser the page supports both light
      // and dark UAs. Without it, form-control internals (input /
      // textarea / select default backgrounds, scrollbar tracks,
      // focus rings) pick the LIGHT branch even when the OS is in
      // dark mode — visible as a stark white input on an otherwise
      // dark page. With `light dark` listed, the UA flips its
      // internals to match `prefers-color-scheme`, and our explicit
      // .mar-textfield rules layer over a dark-correct UA default.
      ':root { color-scheme: light dark; }',
      // <html> picks up its own solid background matching the bottom
      // of the body gradient. Two reasons:
      //
      //  1. Overscroll bounce (Safari, mobile in particular) reveals
      //     the html element above/below the body. Without this it
      //     defaults to white — jarring on a dark-mode page that's
      //     otherwise charcoal.
      //
      //  2. The View Transitions API renders its snapshot pair
      //     inside a transient overlay anchored to <html>. While the
      //     two snapshots slide horizontally, the gap between them
      //     briefly shows <html>'s background. Same default-white
      //     story: would flash light during every page transition
      //     in dark mode.
      //
      // The light-mode rule pairs with the @media (prefers-color-
      // scheme: dark) override further down.
      'html { background-color: #efeff2; }',
      'body {',
      // Subtle vertical gradient — "Liquid Glass" needs a hint of
      // directional light to look right (glass without context
      // looks like flat translucency). Light at the top fades to
      // a slightly darker tone toward the bottom, so the glass
      // pills and cards above pick up a soft top highlight.
      '  background: linear-gradient(180deg, #fafafc 0%, #f5f5f7 60%, #efeff2 100%);',
      '  background-attachment: fixed;',
      '  color: #1d1d1f;',
      '  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Display",',
      '    "SF Pro Text", "Helvetica Neue", system-ui, sans-serif;',
      '  font-size: 17px;',
      '  line-height: 1.47;',
      '  -webkit-font-smoothing: antialiased;',
      '  text-rendering: optimizeLegibility;',
      '}',

      // Page container — content stays inside a ~1024px column to
      // keep line lengths readable on wide displays. Big top padding
      // gives the large title breathing room; the page is
      // content-first, no nav chrome on top.
      // iOS-specific notes:
      //
      // 1. Padding uses `max(<min>, env(safe-area-inset-*))` so the
      //    page never renders content under the notch / Dynamic
      //    Island / home indicator. On desktop and Android (where
      //    the env() values are 0), the min wins and layout is
      //    identical to before. On iPhone with notch the top padding
      //    grows to ~44pt so the back button + title clear the
      //    status bar properly.
      //
      // 2. `min-height` is declared twice: 100vh first as the
      //    fallback, 100dvh second so supporting browsers (Safari
      //    15.4+, Chrome 108+, Firefox 110+) use the dynamic value
      //    that tracks URL-bar collapse. With plain 100vh, iOS
      //    Safari reports the FULL viewport (URL bar hidden),
      //    causing content to appear shifted up when the URL bar
      //    is visible.
      '.mar-nav-stack {',
      '  display: block;',
      '  max-width: 1024px;',
      '  width: 100%;',
      '  margin: 0 auto;',
      '  padding-top: max(24px, calc(env(safe-area-inset-top) + 12px));',
      '  padding-right: max(32px, env(safe-area-inset-right));',
      '  padding-bottom: max(48px, calc(env(safe-area-inset-bottom) + 24px));',
      '  padding-left: max(32px, env(safe-area-inset-left));',
      '  min-height: 100vh;',
      '  min-height: 100dvh;',
      '  box-sizing: border-box;',
      '}',

      // Large-title header — scrolls away with the content, exactly
      // like iOS. Only the toolbar row below is pinned.
      '.mar-nav-bar {',
      '  margin-bottom: 24px;',
      '}',
      // Pinned toolbar row — back/leading pills on the left, the
      // inline title in the center, trailing pills on the right.
      // `top` repeats the nav stack\'s padding-top formula on
      // purpose: the row\'s resting position IS its pinned position,
      // so the bar never visibly moves — it just stops following
      // the scroll, which is the iOS navigation-bar behavior. The
      // pills carry their own backdrop blur, so content scrolling
      // under them blurs through the glass with no opaque bar strip
      // (iOS 26 floating-toolbar model).
      '.mar-nav-toolbar-row {',
      '  position: sticky;',
      '  top: max(24px, calc(env(safe-area-inset-top) + 12px));',
      '  z-index: 10;',  // above in-flow content; far below sheet (2000)
      '  display: flex;',
      '  align-items: center;',
      '  justify-content: space-between;',
      '  gap: 12px;',
      // Hangs the pills slightly outside the content column so
      // they sit in the safe-area gutter — same visual gravity as
      // iOS 26\'s floating toolbar.
      '  margin-left: -8px;',
      '  margin-right: -8px;',
      '  margin-bottom: 16px;',
      '  min-height: 36px;',
      '}',
      // Equal flexible sides keep the inline title truly centered
      // regardless of how the left/right pill clusters differ.
      '.mar-nav-toolbar-row > .mar-nav-side { flex: 1 1 0; }',
      // Buttons-only page (no navigationTitle): the row is the
      // entire chrome, so it owns the full 24px gap to the body.
      '.mar-nav-toolbar-solo { margin-bottom: 24px; }',
      // Title-only page (no buttons): the row reserves no space at
      // rest — the large title keeps its position — and only the
      // inline title floats in over the content once the page
      // scrolls. pointer-events stays off so the empty strip never
      // eats clicks meant for content (the pill is not interactive).
      '.mar-nav-toolbar-bare {',
      '  min-height: 0;',
      '  height: 0;',
      '  margin-bottom: 0;',
      '  align-items: flex-start;',
      '  pointer-events: none;',
      '}',
      // Inline title — the small centered glass pill that takes
      // over when the large title scrolls out (.mar-nav-scrolled,
      // toggled by wireNavInlineTitle). Same glass recipe as the
      // nav pills.
      '.mar-nav-inline-title {',
      '  font-size: 14px;',
      '  font-weight: 600;',
      '  color: #1d1d1f;',
      '  background: rgba(255, 255, 255, 0.62);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.06);',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 2px 8px rgba(0, 0, 0, 0.05);',
      '  border-radius: 980px;',
      '  padding: 8px 18px;',
      '  max-width: 50%;',
      '  white-space: nowrap;',
      '  overflow: hidden;',
      '  text-overflow: ellipsis;',
      '  opacity: 0;',
      '  transform: translateY(-4px);',
      '  transition: opacity 200ms ease, transform 200ms ease;',
      '  pointer-events: none;',
      '}',
      '.mar-nav-scrolled .mar-nav-inline-title { opacity: 1; transform: none; }',
      '.mar-nav-title-row { display: block; }',
      '.mar-nav-title {',
      '  font-size: 32px;',
      '  font-weight: 700;',
      '  letter-spacing: -0.02em;',
      '  line-height: 1.15;',
      '  white-space: nowrap;',
      '  overflow: hidden;',
      '  text-overflow: ellipsis;',
      '}',
      '.mar-nav-side { display: flex; align-items: center; gap: 8px; }',
      '.mar-nav-side-trailing { justify-content: flex-end; }',

      // Trailing nav buttons — glass pills. iOS 26 dropped the
      // gray-fill toolbar buttons in favor of translucent pills
      // that pick up backdrop blur. The inset white highlight is
      // the "specular" — the cue your eye reads as glass rather
      // than just translucent.
      '.mar-nav-side button {',
      '  background: rgba(255, 255, 255, 0.62);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.06);',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 2px 8px rgba(0, 0, 0, 0.05);',
      '  color: #1d1d1f;',
      '  font-family: inherit;',
      '  font-size: 14px;',
      '  font-weight: 500;',
      '  padding: 8px 18px;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, transform 150ms;',
      // touch-action: manipulation tells iOS Safari "this button
      // accepts taps for click — don\'t hold the synthetic click
      // for ~300ms waiting for a possible double-tap-to-zoom".
      // Without it, after a touch-heavy gesture (like the drag
      // reorder), the next tap on this button gets absorbed by
      // Safari\'s gesture-disambiguation window: the operator
      // sees "Done did nothing" on the first press, has to tap
      // again. Applies cleanly to nav-bar action buttons (back,
      // Done, Sign out) which are tap-only — no pinch or scroll.
      '  touch-action: manipulation;',
      '}',
      '@media (hover: hover) { .mar-nav-side button:hover { background: rgba(255, 255, 255, 0.85); } }',
      '.mar-nav-side button:active { transform: scale(0.96); }',

      // Auto-inserted back button — circular glass pill with the
      // chevron only. iOS 26 dropped the "‹ Back" / "‹ Previous"
      // text label in favor of an icon-only pill that floats over
      // the page. Accessibility lives in `aria-label` and the
      // hover `title` tooltip (set in buildBackButton).
      '.mar-nav-back {',
      '  width: 36px; height: 36px;',
      '  padding: 0;',
      '  border-radius: 50%;',
      '  background: rgba(255, 255, 255, 0.62);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.06);',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 2px 8px rgba(0, 0, 0, 0.06);',
      '  color: #0071e3;',
      '  cursor: pointer;',
      '  display: inline-flex;',
      '  align-items: center;',
      '  justify-content: center;',
      '  transition: background 200ms, transform 150ms;',
      '  touch-action: manipulation;',  // skip iOS 300ms double-tap delay
      '}',
      '@media (hover: hover) { .mar-nav-back:hover { background: rgba(255, 255, 255, 0.85); } }',
      '.mar-nav-back:active { transform: scale(0.92); }',
      '.mar-nav-back-chev {',
      '  font-size: 22px; font-weight: 600; line-height: 1;',
      '  margin-top: -2px;',
      '}',

      // Form / list / nav body — vertical stack with snug gap.
      // Cards have visible boundaries; just enough gap to keep them
      // from touching, no more.
      '.mar-form, .mar-list, .mar-nav-body {',
      '  display: flex; flex-direction: column;',
      '  gap: 24px;',
      '  padding: 0; margin: 0;',
      '  list-style: none;',
      '  border: none;',
      '}',

      // Height propagation. #mar-root is a min-100dvh flex column
      // (see the shell CSS in server.go); the nav stack and its body
      // pass that height down so a `height fill` child or a
      // `centered` view has real space to claim — no magic
      // min-heights anywhere in the chain. Content taller than the
      // viewport still grows normally (flex-basis auto).
      '.mar-nav-stack { flex: 1 1 auto; display: flex; flex-direction: column; }',
      '.mar-nav-body { flex: 1 1 auto; }',

      // Section: small uppercase eyebrow header above a rounded
      // content card. Reset UA margin — some browsers give <section>
      // ~1em vertical margin by default, which shows up as a chunky
      // black gap between consecutive section cards on top of our
      // form gap.
      '.mar-section { display: block; margin: 0; padding: 0; }',
      '.mar-section-header {',
      '  font-size: 12px; font-weight: 600;',
      '  text-transform: uppercase; letter-spacing: 0.8px;',
      '  color: #86868b;',
      '  padding: 0 4px; margin: 0 0 4px 0;',
      '}',
      // Section card — iOS 26 "Liquid Glass" surface. Translucent
      // white with backdrop blur + saturation so what\'s behind
      // (the gradient page background) bleeds through softened.
      // The inset specular highlight on the top edge + outer drop
      // shadow give the "floating glass pane above the page" feel.
      //
      // The horizontal inset (16px) lives HERE on the parent rather
      // than as margin on individual rows. That way replaced
      // children — <input>, <textarea>, <select> — naturally fill
      // the (now-narrower) content area via plain `width: 100%`,
      // sidestepping the "width: auto means intrinsic on inputs"
      // CSS gotcha that bit us when the inset was per-row.
      '.mar-section-body {',
      '  background: rgba(255, 255, 255, 0.72);',
      '  -webkit-backdrop-filter: blur(40px) saturate(180%);',
      '  backdrop-filter: blur(40px) saturate(180%);',
      '  border-radius: 18px;',
      '  border: 0.5px solid rgba(0, 0, 0, 0.04);',
      '  overflow: hidden;',
      '  padding: 0 16px;',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.6),',
      '    0 8px 24px rgba(0, 0, 0, 0.05);',
      '}',
      // Rows: vertical padding only — horizontal inset now comes
      // from the parent. Border-bottom becomes "inset divider"
      // (matches iOS Settings rather than full-bleed dividers).
      //
      // `min-height: 22px` matches the height of the edit-mode
      // affordances (drag handle, delete circle). Without this,
      // tapping Edit would grow each row by 2-5px (depending on
      // the browser's default line-height for 16px text) because
      // the chrome is slightly taller than the natural text line.
      // Locking the content area to 22px in normal mode too means
      // the row dimensions are identical in both modes — no
      // jumpy reflow when the user toggles Edit.
      '.mar-section-body > * {',
      '  display: block;',
      '  padding: 10px 0;',
      '  min-height: 22px;',
      '  border-bottom: 0.5px solid rgba(0, 0, 0, 0.08);',
      '  font-size: 16px;',
      '}',
      '.mar-section-body > *:last-child { border-bottom: none; }',
      // A section with an empty body (e.g. `section [ footer "..." ] []`,
      // a footer- or header-only section) must not paint an empty glass
      // card between its neighbors. Collapse the body so only the
      // header/footer caption shows — matching iOS, where a footer-only
      // section renders as a lone gray caption with no row card.
      '.mar-section-body:empty { display: none; }',
      '.mar-section-footer {',
      '  font-size: 13px; color: #86868b;',
      '  padding: 0 4px; margin: 6px 0 0 0;',
      '}',

      // ----- Reorderable section (UI.onMove) -----
      //
      // The section body gets a `mar-section-editing` class when
      // edit mode is on; descendant rules hang off that so a
      // non-edit section pays zero extra cost. Position: relative
      // so the absolute-positioned drop indicator anchors here.
      '.mar-section-editing { position: relative; }',
      // Each row gets a trailing drag handle in edit mode. The
      // row's vertical padding stays (inherited from the
      // .mar-section-body > * rule); we just relayout the content
      // as a flex row so the handle sits at the trailing edge.
      '.mar-section-editing > * {',
      '  display: flex; align-items: center;',
      '  gap: 8px;',
      '}',
      // The handle: 36×22 — wide enough for a comfortable
      // horizontal tap target (the row's vertical padding extends
      // the hit area to the full 44pt Apple guideline), and
      // height-matched exactly to the delete button + the row's
      // min-height. Toggling edit mode doesn't cause any reflow
      // because every chrome element (text, delete, handle) is
      // ≤22px tall, and the row's min-height locks the inner
      // area at 22px in both modes.
      //
      // The three bars inside are 18×2 each with a 3px gap,
      // packing to 12px total — comfortably inside the 22px box.
      '.mar-drag-handle {',
      '  flex: 0 0 auto;',
      '  width: 36px; height: 22px;',
      '  display: flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  gap: 3px;',
      '  padding: 0;',
      // `margin-left: auto` makes the handle self-position to the
      // right edge of its flex row, consuming whatever slack the
      // row has. Works whether the row is a plain `text` leaf
      // (where the text node has no flex-grow) or an `hstack`
      // (where the hstack children already fill space). The handle
      // BELONGS at the trailing edge — putting the rule here makes
      // that property of the handle itself, not a side-effect of
      // whatever wrapper happens to be around it.
      '  margin-left: auto;',
      '  background: transparent;',
      '  border: none;',
      '  border-radius: 8px;',
      '  cursor: grab;',
      '  touch-action: none;',  // prevent the browser from scrolling on touch-drag
      '  -webkit-tap-highlight-color: transparent;',
      // Long-press on the handle on iOS shouldn\'t pop the
      // text-selection callout (copy/paste/Define) — the handle is
      // a grab affordance, not selectable content.
      '  -webkit-user-select: none; user-select: none;',
      '  -webkit-touch-callout: none;',
      '}',
      '.mar-drag-handle:active { cursor: grabbing; }',
      // The row that hosts this button is rendered as `.mar-hstack`
      // in edit mode (Mar wraps `keyed` rows as hstacks to attach
      // the reorder key). The generic `.mar-hstack > button`
      // chrome below already excludes .mar-drag-handle via
      // :not(), but the failure mode if that exclusion ever
      // misses (cache, chained-:not() bug, future selector
      // refactor) is severe — the handle turns into a solid
      // white pill on hover, making the bars invisible against
      // it. Belt-and-braces: force transparent at higher
      // specificity than the hstack rule, with !important on
      // hover/focus/active.
      '.mar-hstack > .mar-drag-handle,',
      '.mar-hstack > .mar-drag-handle:hover,',
      '.mar-hstack > .mar-drag-handle:focus,',
      '.mar-hstack > .mar-drag-handle:active {',
      '  background: transparent !important;',
      '  border: none !important;',
      '  box-shadow: none !important;',
      '}',
      '.mar-drag-handle-bar {',
      '  display: block;',
      '  width: 18px; height: 2px;',
      '  background: #c7c7cc;',
      '  border-radius: 1px;',
      '  pointer-events: none;',  // so the row's pointerdown fires on the button, not the bar
      '}',
      // Realtime reorder transition. When a drag is in progress the
      // section gets `.mar-section-drag-active`, and non-dragged
      // rows get an inline transform: translateY(±rowHeight) in
      // onPointerMove to "open up" the drop slot under the cursor.
      // This rule applies a CSS transition to those transforms so
      // the shifts animate smoothly instead of snapping per
      // cursor-tick. `:not(.mar-row-dragging)` excludes the dragged
      // row itself, whose follow-cursor transform must update 1:1
      // with the pointer (any transition there would lag the
      // pointer behind the finger).
      '.mar-section-drag-active > *:not(.mar-row-dragging) {',
      '  transition: transform 200ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',

      // Row being actively dragged — opaque background + elevated
      // shadow + above-siblings z-index so it reads as a card
      // floating over the rest of the list. The pointer-move
      // handler also applies an inline `transform: translateY(...)
      // scale(1.02)` so the row follows the cursor and feels
      // "picked up". The 10px border-radius rounds the floating
      // card slightly so it doesn't look like the same flush row
      // it was at rest.
      //
      // Border-bottom suppressed: the `.mar-section-body > *` rule
      // gives every child a hairline divider, which on a card
      // that's now floating reads as a stray underline.
      '.mar-row-dragging {',
      '  background: #ffffff;',
      '  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.22), 0 4px 8px rgba(0, 0, 0, 0.08);',
      '  border-radius: 10px;',
      '  border-bottom: none !important;',
      '  z-index: 2;',
      '  position: relative;',
      '}',
      // Keyboard focus on a row — Tab navigates between rows in
      // edit mode (each has tabindex="0"). Without an explicit
      // :focus-visible rule, the browser draws its default outline
      // which gets visually swallowed by the row's flush layout
      // and the dark page background. A subtle blue outline makes
      // "I just Tabbed here" obvious — same color as the grabbed
      // state below, but no background tint so the two states
      // remain distinguishable (focused = "could grab", grabbed =
      // "actively moving").
      '.mar-section-editing > *:focus-visible {',
      '  outline: 2px solid #0a84ff;',
      '  outline-offset: -2px;',
      '  border-radius: 8px;',
      '}',
      // Keyboard "grabbed" state — distinct from focused (adds the
      // background tint to say "arrow keys will move this row").
      // Both styles share the outline so the visual transition
      // from focused → grabbed is just the bg-fill flashing on.
      '.mar-row-grabbed {',
      '  outline: 2px solid #0a84ff;',
      '  outline-offset: -2px;',
      '  background: rgba(10, 132, 255, 0.08);',
      '  border-radius: 8px;',
      '}',
      // Focus, grab, and active-drag states all stretch the
      // highlight to the section card's edges. The section-body
      // has `padding: 0 16px` around its rows; without
      // compensating, the outline / bg tint / dragged-card surface
      // floats inset from the card's edges with a 16px gap that
      // reads as "this is some inner thing, not the whole row".
      //
      // Negative left/right margins (-16px) pull the row's BOX out
      // to the section's padding edges; equal left/right padding
      // (16px) restores the content position so text / buttons /
      // handle don't shift. Result: outline + bg + lift span the
      // full row width (edge to edge of the rounded card),
      // content stays in its place — the iOS Settings "selected
      // row" look.
      //
      // Including `.mar-row-dragging` here makes the lifted card
      // also fill the full width so the dragged surface visually
      // detaches from the section's inner padding rails.
      '.mar-section-editing > *:focus-visible,',
      '.mar-section-editing > *.mar-row-grabbed,',
      '.mar-section-editing > *.mar-row-dragging {',
      '  margin-left: -16px;',
      '  margin-right: -16px;',
      '  padding-left: 16px;',
      '  padding-right: 16px;',
      '}',
      // Corner rounding for the first and last rows — match the
      // section card's outer border-radius (18px) so the outline
      // and bg fill curve smoothly into the card's rounded
      // corners. Without these overrides, the rectangular 8px
      // radius of the row's highlight crosses through the card's
      // 18px curved corner area, leaving a visible "ledge" where
      // the row outline's straight edge cuts across the card's
      // bend. Middle rows keep the default 8px (flat top/bottom
      // edges, since they border other rows).
      //
      // The `:first-child:last-child` combo handles the single-row
      // case — both top AND bottom corners round.
      '.mar-row-first:focus-visible,',
      '.mar-row-first.mar-row-grabbed {',
      '  border-radius: 18px 18px 8px 8px;',
      '}',
      '.mar-row-last:focus-visible,',
      '.mar-row-last.mar-row-grabbed {',
      '  border-radius: 8px 8px 18px 18px;',
      '}',
      '.mar-row-first.mar-row-last:focus-visible,',
      '.mar-row-first.mar-row-last.mar-row-grabbed {',
      '  border-radius: 18px;',
      '}',
      // Drop indicator — thin blue line shown between rows during
      // drag. Absolutely positioned over the list root; the JS
      // updates `top` as the cursor moves.
      //
      // The explicit `padding: 0; border: 0` overrides the
      // `.mar-section-body > *` rule above which gives every child
      // 10px vertical padding + a hairline border-bottom. Without
      // these overrides the "2px line" actually renders as a ~22px
      // blob (2px content + 20px padding + 0.5px border) — visible
      // as a fat blue bar in the screenshot. Absolute positioning
      // takes the element OUT OF FLOW but it still inherits the
      // box-model properties applied by the descendant selector.
      '.mar-drop-indicator {',
      '  position: absolute;',
      '  left: 0; right: 0;',
      '  height: 2px;',
      '  padding: 0;',
      '  border: 0;',
      '  background: #0a84ff;',
      '  border-radius: 1px;',
      '  pointer-events: none;',
      '  z-index: 1;',
      '}',
      // ARIA live region — visually hidden but exposed to screen
      // readers. The reorder code writes "Grabbed item 2 of 5"
      // etc. here, the screen reader announces it politely.
      '.mar-live-region {',
      '  position: absolute;',
      '  width: 1px; height: 1px;',
      '  margin: -1px; padding: 0;',
      '  overflow: hidden;',
      '  clip-path: inset(50%);',  // modern replacement for the deprecated clip: rect()
      '  white-space: nowrap; border: 0;',
      '}',
      // Dark mode handle / grabbed adjustments.
      //
      // For the dragged row in dark mode: light-mode goes #ffffff
      // (clearly lighter than the page), so dark mode needs the
      // opposite contrast — a tint LIGHTER than the surrounding
      // section-body card, with a stronger shadow to read as
      // "floating above" against the near-black page. Bumping to
      // rgba(72,72,74,…) (≈ iOS systemGray3) lifts it visibly
      // above the section-body's rgba(44,44,46,…) backing.
      //
      '@media (prefers-color-scheme: dark) {',
      '  .mar-drag-handle-bar { background: rgba(255, 255, 255, 0.4); }',
      '  .mar-row-dragging {',
      '    background: rgb(72, 72, 74);',
      '    box-shadow: 0 12px 28px rgba(0, 0, 0, 0.7), 0 4px 8px rgba(0, 0, 0, 0.4);',
      '  }',
      '  .mar-row-grabbed {',
      '    background: rgba(10, 132, 255, 0.18);',
      '  }',
      '}',

      // Stacks
      '.mar-hstack {',
      '  display: flex; flex-direction: row; align-items: center;',
      '  gap: 12px;',
      '}',
      // SwiftUI-style layout: hstack children HUG their content — they
      // do NOT stretch to fill the row. To distribute, insert a
      // `spacer` (pushes siblings apart) or wrap a child in `expand`
      // (claims the free space). This is the single rule. The only
      // children that stay greedy are the ones that are intrinsically
      // flexible (textField / textArea, mirroring SwiftUI\'s TextField)
      // or ARE the fill mechanism (spacer, expand) — each opts back in
      // below. `flex: 0 1 auto` = hug, but shrink (truncate) rather
      // than overflow on a narrow row.
      '.mar-hstack > * { flex: 0 1 auto; min-width: 0; }',
      // Buttons keep their intrinsic size and never compress — a
      // clipped button label reads as broken.
      '.mar-hstack > button { flex: 0 0 auto; }',
      '.mar-vstack {',
      '  display: flex; flex-direction: column; align-items: stretch;',
      // 6px is the SwiftUI-feel default — tight enough that a
      // title/subtitle pair reads as a single row (the iOS Settings
      // pattern), loose enough that a vstack of three sibling
      // sentences still has breathing room. The previous 12px
      // pushed two-line nav rows to feel like two separate items.
      '  gap: 6px;',
      '}',

      // Inputs — soft fill, big, prominent. Focus shows the
      // link-blue ring. Always has visible chrome (border, fill,
      // rounded, focus ring). What changes with context is
      // POSITIONING:
      //   - inside a section card: vertical+horizontal margin so the
      //     input is visually inset from the card edges
      //   - inside an hstack: flex: 1 (no margin — hstack's gap
      //     handles spacing)
      //   - free-floating: full width
      //
      // -webkit-appearance: none kills Safari's UA textfield chrome
      // (white pill background) that otherwise wins over our styles.
      '.mar-textfield {',
      '  display: block; box-sizing: border-box;',
      '  width: 100%;',
      // Explicitly cancel the `max-width: 24rem` set by the HTML\'s
      // baseline `<style>` reset (see internal/jsserve/server.go). That
      // reset uses a `:where(input:not(...))` selector with zero
      // specificity, expecting any rule that mentions `.mar-textfield`
      // by class to win — but specificity comparison happens per
      // PROPERTY, not per rule. `.mar-textfield` declares `width` but
      // not `max-width`, so the reset\'s 24rem cap leaks through and
      // hard-caps every mar-styled input at 384px regardless of
      // container. Setting `max-width: none` here neutralizes it.
      '  max-width: none;',
      '  margin: 0;',
      '  padding: 10px 14px;',
      '  border: 1px solid rgba(0, 0, 0, 0.12);',
      '  border-radius: 8px;',
      '  outline: none;',
      '  -webkit-appearance: none; appearance: none;',
      '  background: rgba(0, 0, 0, 0.02);',
      '  font-size: 16px; font-family: inherit; color: inherit;',
      '  transition: background 200ms, border-color 200ms, box-shadow 200ms;',
      '}',
      '.mar-textfield:focus {',
      '  background: #ffffff;',
      '  border-color: rgba(0, 113, 227, 0.5);',
      '  box-shadow: 0 0 0 4px rgba(0, 113, 227, 0.12);',
      '}',
      '.mar-textfield::placeholder { color: #86868b; }',
      // Tabular numerals — every digit (0-9) renders at exactly the
      // same advance width, making CSS `ch` calculations exact for
      // numeric-content inputs. Without this, in proportional fonts
      // like system-ui / SF Pro, "888888" is ~5% wider than "111111",
      // causing horizontal scroll-flicker when the cursor passes the
      // trailing edge of a sized field (e.g. `width (chars 6)` on a
      // 6-digit code input).
      //
      // Scoped to inputs with `inputmode="numeric"` (set by `numeric` /
      // `numericCode` attrs) so non-numeric fields keep proportional
      // digits, which read better in regular prose.
      '.mar-textfield[inputmode="numeric"] {',
      '  font-variant-numeric: tabular-nums;',
      '}',
      // Disabled textfield / textarea — keeps the same visual frame
      // but dims the text + placeholder and switches the cursor so
      // the user knows clicking won\'t do anything. `not-allowed`
      // matches the cursor we use on disabled buttons.
      '.mar-textfield:disabled {',
      '  background: rgba(0, 0, 0, 0.02);',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '  border-color: rgba(0, 0, 0, 0.06);',
      '}',
      '.mar-textfield:disabled::placeholder { color: rgba(0, 0, 0, 0.24); }',
      // Multi-line textarea variant. Inherits all the .mar-textfield
      // base styling (borders, focus ring, padding) and adds the bits
      // that don't apply to <input>: a relaxed line-height, vertical
      // resize affordance, and a font-family pin so it renders in the
      // same sans the rest of the form uses (browsers default
      // <textarea> to a monospace stack on some platforms).
      '.mar-textarea {',
      '  line-height: 1.4;',
      '  font-family: inherit;',
      '  resize: vertical;',
      '  min-height: 80px;',
      '}',
      // Inside a section card: just a small vertical margin between
      // rows. The horizontal inset comes from the parent's padding
      // (see `.mar-section-body`), so the textfield's base
      // `width: 100%` already fills the card — no per-row width math.
      '.mar-section-body > .mar-textfield {',
      '  margin: 8px 0;',
      '}',
      // Inside an hstack, fill the available row, no margin.
      '.mar-hstack > .mar-textfield {',
      '  flex: 1; min-width: 0;',
      '  margin: 0;',
      '}',

      // Picker — single-selection dropdown. The wrapping div is the
      // visual chrome (border, focus state, custom chevron); the
      // inner <select> stays a plain native control so accessibility
      // (keyboard nav, screen-reader, mobile native popover) keeps
      // working. We hide the browser's default chevron via
      // appearance:none so the only chevron the user sees is our
      // overlay glyph, which we can position consistently across
      // platforms.
      // Date picker — a native <input type="date"> wearing the
      // textfield chrome (.mar-textfield). We only nudge the calendar
      // trigger so it reads as interactive; .mar-textfield's
      // appearance:none can otherwise dim it.
      '.mar-datepicker { cursor: pointer; }',
      '.mar-datepicker::-webkit-calendar-picker-indicator {',
      '  cursor: pointer; opacity: 0.55;',
      '}',
      '.mar-datepicker::-webkit-calendar-picker-indicator:hover { opacity: 0.9; }',

      '.mar-picker {',
      '  display: flex; align-items: center;',
      // `isolation: isolate` keeps the ::before's `z-index: -1`
      // inside the picker's local stacking context. Without it,
      // the negative z-index escapes to the html root and paints
      // behind the section card's white background — invisible.
      // Same fix as `a.mar-navigation-link`.
      '  position: relative;',
      '  isolation: isolate;',
      '  cursor: pointer;',
      '  user-select: none;',
      '}',
      '.mar-picker-select {',
      '  flex: 1; min-width: 0;',
      '  appearance: none; -webkit-appearance: none;',
      '  background: transparent;',
      '  border: none;',
      // 24px right pad reserves space for the chevron; the chevron
      // is absolutely positioned at right: 0 (see below) and would
      // otherwise overlap the longest option label.
      '  padding: 4px 24px 4px 0;',
      '  margin: 0;',
      '  font: inherit; color: inherit;',
      // Left-align the value. Matches the readability the user
      // asked for — the eye scans from the section header on the
      // left, and the trailing chevron at the row's right edge
      // closes the visual frame. (SwiftUI Form Picker right-aligns
      // the value flush against the chevron; we don\'t copy that
      // here because long values on web tend to feel disconnected
      // from the section header when right-aligned.)
      '  text-align: left;',
      '  text-align-last: left;',
      '  cursor: pointer;',
      '  outline: none;',
      '}',
      // The chevron sits in the trailing slot of the row, mirroring
      // SwiftUI's Picker (which shows the value flush-right + a
      // menu indicator). aria-hidden in markup; dimmed in CSS so
      // it reads as decorative chrome. Glyph is the two-arrow
      // "select-up-down" indicator — the iOS 17+ Picker(.menu)
      // convention. Apple's symbol is `chevron.up.chevron.down`;
      // we render it inline via a stacked SVG that ships with the
      // runtime so it renders identically across fonts (`›`
      // rotated is jagged on some renderers; standalone `⌄` looks
      // like a typo).
      //
      // Sized to roughly match the visual weight of the
      // navigationLink chevron (1.4em `›`). The SVG itself is
      // 18×22; container box matches so the strokes don\'t get
      // anti-aliased into a blur.
      '.mar-picker-chevron {',
      '  position: absolute; right: 2px;',
      '  top: 50%; transform: translateY(-50%);',
      '  display: inline-flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  width: 18px; height: 22px;',
      '  color: #8e8e93;',
      '  line-height: 1;',
      '  pointer-events: none;',
      '}',
      // Inside a section card — same row layout as toggle: label-ish
      // text on the left (the selected value) and the trailing
      // disclosure chevron on the right. The section-body > * rule
      // supplies the 10px/16px padding so we don\'t restate it here.
      '.mar-section-body > .mar-picker {',
      '  width: auto;',
      '}',
      // Keyboard-focus tint, full-bleed across the row. Same pattern
      // as navigationLink hover (inset: -1px -16px): a ::before
      // pseudo paints the focus background past the picker's own box
      // so the cyan tint reaches the card's inner edge instead of
      // leaving white strips where the parent's 16px padding lives.
      // The card's overflow:hidden + 18px border-radius clips the
      // ::before at the card corners; no mismatched-radii problem.
      // Anchored against the picker's existing `position: relative`.
      '.mar-picker::before {',
      '  content: "";',
      '  position: absolute;',
      '  inset: -1px -16px;',
      '  background: transparent;',
      '  pointer-events: none;',
      '  transition: background 120ms;',
      '  z-index: -1;',
      '}',
      // Hover tint matches navigationLink (a sibling row in the
      // same section card) so the two row shapes feel like one
      // affordance vocabulary. Source order matters: hover first,
      // focus-within second, so a focused-AND-hovered picker
      // shows the focus blue, not the hover gray. Same precedence
      // as navigationLink.
      //
      // `:not(.mar-disabled)` suppresses BOTH tints when the picker
      // is inert — matches the convention used by button/
      // navigationLink/etc: disabled = ZERO interactive feedback,
      // only the `not-allowed` cursor signals "this exists but
      // doesn\'t respond." Without this guard, a disabled picker
      // would light up gray on hover, falsely inviting interaction.
      // Same sticky-hover-on-touch guard as navigationLink: `:active` carries
      // the press tint everywhere (transient), `:hover` is gated to pointers.
      '.mar-picker:not(.mar-disabled):active::before {',
      '  background: rgba(0, 0, 0, 0.04);',
      '}',
      '@media (hover: hover) {',
      '  .mar-picker:not(.mar-disabled):hover::before {',
      '    background: rgba(0, 0, 0, 0.04);',
      '  }',
      '}',
      // Keyboard-only. A native <select> keeps focus after a mouse
      // click, so :focus-within left this tint stuck until a second
      // click elsewhere finally blurred it. :has(:focus-visible) shows
      // the indicator for keyboard (Tab) focus and drops it for mouse,
      // which is what "keyboard-focus tint" meant all along.
      '.mar-picker:not(.mar-disabled):has(.mar-picker-select:focus-visible)::before {',
      '  background: rgba(0, 113, 227, 0.10);',
      '}',
      // Disabled picker — fade the value text + chevron and switch
      // the cursor. The inner <select disabled> blocks the dropdown;
      // .mar-disabled is toggled on the wrapper by the renderer so
      // the chevron (a separate <span>) and any future overlay
      // chrome can react too.
      '.mar-picker.mar-disabled { cursor: not-allowed; }',
      '.mar-picker.mar-disabled .mar-picker-select {',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '}',
      '.mar-picker.mar-disabled .mar-picker-chevron {',
      '  color: rgba(0, 0, 0, 0.18);',
      '}',

      // Buttons split into two roles (iOS 26 hierarchy):
      //
      //   PRIMARY CTA  — section-body direct child. Full-width
      //                  action like "Verify" / "Submit". Solid
      //                  blue fill, white text — the visual
      //                  weight reads "do this".
      //
      //   SECONDARY    — hstack child (inline next to other row
      //                  content, like "Add" beside an input, or
      //                  "Delete" in a task row). Glass pill with
      //                  link-blue TEXT instead of solid blue —
      //                  quiet-action treatment.
      '.mar-section-body > button {',
      '  display: block;',
      '  width: auto;',
      '  margin: 8px 16px;',
      '  padding: 10px 20px;',
      '  background: #0071e3; border: none; color: white;',
      '  font-family: inherit; font-weight: 500; font-size: 15px;',
      '  text-align: center;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, opacity 200ms;',
      '  touch-action: manipulation;',  // skip iOS 300ms double-tap delay
      '}',
      '.mar-section-body > button:hover { background: #0077ed; }',
      '.mar-section-body > button:disabled {',
      '  background: #c7c7cc; color: rgba(255, 255, 255, 0.85);',
      '  cursor: not-allowed; opacity: 0.55;',
      '}',
      '.mar-section-body > button:disabled:hover { background: #c7c7cc; }',
      // Generic `.mar-hstack > button` chrome — the pill / blur /
      // blue-link look for action buttons inline in a row (Add, Save,
      // etc.). Two buttons are EXCEPTIONS and need their own visual
      // identity preserved, so they're excluded via :not():
      //   - .mar-drag-handle: the three-bars reorder grip. The hstack
      //     style would give it the same pill look as Add and turn it
      //     into yet another generic button; we want the bare gray
      //     bars.
      //   - .mar-row-delete: the red minus-circle delete affordance.
      //     The hstack rule's background + border-radius:980px + blue
      //     color would turn it into a pill instead of the red round
      //     destructive-action button defined down in the
      //     onDelete-affordance CSS.
      // Both exclusions matter because the row is rendered as an
      // hstack in edit mode (see Frontend/Home.mar's renderTaskRow);
      // the delete button + drag handle get appended INTO that
      // hstack, where they would otherwise inherit these styles via
      // selector specificity ((0,1,1) > (0,1,0)).
      '.mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete) {',
      '  background: rgba(255, 255, 255, 0.62);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.06);',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 2px 8px rgba(0, 0, 0, 0.04);',
      '  color: #0071e3;',
      '  font-family: inherit; font-weight: 500;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, transform 150ms;',
      // touch-action: manipulation skips iOS\'s 300ms double-tap delay.
      // The :not() exclusions matter because the drag handle needs
      // touch-action: none for pointer-drag, and the delete button
      // has its own rule below.
      '  touch-action: manipulation;',
      '}',
      '.mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):hover { background: rgba(255, 255, 255, 0.88); }',
      '.mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):active { transform: scale(0.96); }',
      '.mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):disabled {',
      '  background: rgba(0, 0, 0, 0.04);',
      '  color: rgba(0, 0, 0, 0.28);',
      '  cursor: not-allowed;',
      '  box-shadow: none;',
      '}',
      '.mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):disabled:hover { background: rgba(0, 0, 0, 0.04); }',
      // Inline pill (next to an input).
      '.mar-hstack > button {',
      '  flex: 0 0 auto;',
      '  padding: 6px 16px;',
      '  font-size: 14px;',
      '}',

      // hstack inside section card — already gets row padding from
      // the section-body > * rule. No extra needed.

      // Title / subtitle — heading text in the body of a section.
      // Scoped via class to avoid clobbering h1/h2 used elsewhere
      // (e.g. .mar-section-header is also <h2>).
      '.mar-title {',
      '  font-size: 24px; font-weight: 700; letter-spacing: -0.015em;',
      '  margin: 0; line-height: 1.2;',
      '}',
      '.mar-subtitle {',
      '  font-size: 17px; font-weight: 400; color: #86868b;',
      '  margin: 0; line-height: 1.35;',
      '}',
      // image — UI.image. Default (no `size` attr): fills the
      // container width and keeps aspect ratio. A `size` attr sets
      // explicit width/height inline (see applyImageAttrs). Rounded
      // corners match the section-card radius language; object-fit
      // defaults to contain (no crop) and flips to cover under `fill`.
      '.mar-image {',
      '  display: block; max-width: 100%; height: auto;',
      '  border-radius: 10px; object-fit: contain;',
      '}',
      // errorText — red + semi-bold so "couldn't reach the server"
      // and similar destructive-state messages jump out from the
      // surrounding plain body text. The shade is iOS systemRed
      // (#ff3b30) which has good contrast on both light and dark
      // section-card backgrounds; the dark-mode override below
      // shifts to a slightly lighter variant for legibility against
      // the darker card.
      '.mar-error-text {',
      '  font-size: 15px; font-weight: 600;',
      '  color: #ff3b30;',
      '  margin: 0; line-height: 1.4;',
      '}',
      '@media (prefers-color-scheme: dark) {',
      '  .mar-error-text { color: #ff6961; }',
      '}',
      // paragraph + inline atoms — flowing block of mixed-styled
      // text. The block sets natural body-text dimensions; inline
      // atoms compose freely via additive CSS classes so `[bold,
      // code]` (bold inline code) works without special-casing.
      '.mar-paragraph {',
      '  font-size: 17px; line-height: 1.4;',
      '  margin: 0;',
      '  color: inherit;',
      // overflow-wrap so long URLs in `link` text break instead of
      // forcing horizontal scroll on narrow screens.
      '  overflow-wrap: anywhere;',
      '}',
      '.mar-inline { color: inherit; }',
      '.mar-inline-bold { font-weight: 700; }',
      '.mar-inline-italic { font-style: italic; }',
      '.mar-inline-strike { text-decoration: line-through; }',
      // Inline code — monospace + subtle background tint + small
      // radius. Padding kept tight (1px x 4px) so the run sits on
      // the same baseline as surrounding text without bumping the
      // line-height.
      '.mar-inline-code {',
      '  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;',
      '  font-size: 0.92em;',
      '  background: rgba(0, 0, 0, 0.06);',
      '  padding: 1px 4px; border-radius: 4px;',
      '}',
      '@media (prefers-color-scheme: dark) {',
      '  .mar-inline-code { background: rgba(255, 255, 255, 0.10); }',
      '}',
      // Inline link — accent color + underline. Underline is the
      // affordance signal; on hover the underline thickens slightly
      // for feedback without changing position (text-decoration-
      // thickness keeps layout stable).
      '.mar-inline-link {',
      '  color: #0a84ff; text-decoration: underline;',
      '  text-decoration-thickness: 1px;',
      '  text-underline-offset: 2px;',
      '}',
      '.mar-inline-link:hover { text-decoration-thickness: 2px; }',
      '.mar-inline-link:focus-visible {',
      '  outline: 2px solid #0a84ff; outline-offset: 2px;',
      '  border-radius: 2px;',
      '}',
      // navigationLink — full-width tappable area with a chevron
      // on the trailing edge, mirroring how iOS NavigationLink
      // renders inside a list. Child content keeps the inherited
      // text color so a vstack of text+subtitle (typical row label)
      // reads naturally without restyling.
      'a.mar-navigation-link {',
      '  display: flex; align-items: center; justify-content: space-between;',
      '  gap: 12px;',
      '  width: 100%;',
      '  color: inherit; text-decoration: none; cursor: pointer;',
      // No explicit padding — the parent `.mar-section-body > *`
      // rule supplies 10px 0 to every row inside a section card,
      // and the parent itself supplies the 16px horizontal inset
      // via its own `padding: 0 16px`. The link content sits
      // visually inset from the card edges that way.
      //
      // `position: relative` anchors the ::before that paints the
      // hover/focus tint full-bleed (see below).
      //
      // `isolation: isolate` forces a stacking context on the link.
      // Without it, the ::before's `z-index: -1` escapes to the
      // nearest stacking-context ancestor (the html root) and ends
      // up painting BEHIND the section card's white background —
      // so hover + focus tints become invisible. With isolation,
      // the ::before stays inside the link's local context: behind
      // the link's static content (text + chevron), above the
      // section card's background.
      '  position: relative;',
      '  isolation: isolate;',
      '}',
      'a.mar-navigation-link::after {',
      '  content: "›";',
      '  color: #c7c7cc; font-size: 1.4em; line-height: 1;',
      // The chevron is a decorative trailing ACCESSORY, so it must not
      // shrink the link's content box. As a flex sibling (the old
      // `flex-shrink: 0`) it consumed its own width + the 12px gap, so an
      // `expand`-ed two-column child inside the link laid its columns out
      // over (rowWidth - chevron - gap) — shifting a right-hand value
      // column ~10px left of the same column in a plain (chevron-less)
      // row. Taking it out of flow (absolute) gives the content the FULL
      // row width, so a value/detail column lines up identically whether
      // or not the row has a chevron. It still sits at the right edge,
      // vertically centred, painted over the (empty) trailing margin.
      '  position: absolute; right: 0; top: 50%; transform: translateY(-50%);',
      '}',
      // Hover background — paints full-bleed across the row including
      // the card's 16px horizontal padding. A naive
      // `:hover { background }` on the link only covers the link's
      // own box, leaving white strips where the parent padding sits
      // (the "weird border" bug).
      //
      // Geometry:
      //   - left/right -16px: extend past the parent card's
      //     horizontal padding so the tint reaches the card's inner
      //     border. `.mar-section-body` has overflow:hidden so any
      //     bleed past the card's content box is clipped.
      //   - top/bottom -1px: bleeds past the link's padding box just
      //     enough to cover the 0.5px row divider (this row's own
      //     border-bottom, and the previous row's border-bottom that
      //     sits flush against this row's top). Without this, the
      //     hover tint leaves a thin white sliver at the row's top
      //     and bottom edges — visible at standard zoom.
      //
      // Anchored against the link's `position: relative`. z-index: -1
      // keeps the paint within the link's stacking context (so it
      // doesn't bleed into siblings) while still appearing BELOW
      // the link's static content (text + chevron).
      'a.mar-navigation-link::before {',
      '  content: "";',
      '  position: absolute;',
      '  inset: -1px -16px;',
      '  background: transparent;',
      '  pointer-events: none;',
      '  transition: background 120ms;',
      '  z-index: -1;',
      '}',
      // Mobile browsers emulate `:hover` on tap and leave it STUCK on the
      // last-tapped element (sticky-hover / bfcache restore), so the tint
      // reappears for a moment when you navigate back to a list. Gate the
      // hover tint to real-pointer devices; `:active` gives the same press
      // feedback on touch but is transient (clears on touchend / navigation),
      // so nothing sticks on return.
      'a.mar-navigation-link:active::before { background: rgba(0,0,0,0.07); }',
      '@media (hover: hover) {',
      '  a.mar-navigation-link:hover::before { background: rgba(0,0,0,0.07); }',
      '}',
      // Keyboard-focus tint. Without this, Tab navigation lands on
      // the link with no visible change — users assume Tab is
      // skipping the row entirely. Same ::before painter as hover,
      // brighter color so focus reads distinctly from a casual
      // mouseover. `:focus-visible` (not `:focus`) so a click on
      // the row doesn't leave a ring behind after the page changes.
      // The link's own outline is suppressed since the tint plays
      // the role of focus indicator full-bleed.
      'a.mar-navigation-link:focus { outline: none; }',
      'a.mar-navigation-link:focus-visible::before { background: rgba(0, 113, 227, 0.10); }',
      // Disabled navigationLink — fade the text + chevron, kill the
      // hover background, cursor flips to `not-allowed`. The click
      // itself is swallowed by the delegated handler (onLinkClick
      // reads __marView.attrs and bails out for disabled links) —
      // so we DON\'T set `pointer-events: none` here: that would
      // also suppress the cursor change, leaving the link looking
      // identical to its enabled sibling on hover. Trade-off:
      // mouse events still reach the link, but the JS handler is
      // the authoritative gate, mirroring how `<button disabled>`
      // works (no pointer-events override; the browser handles
      // the click suppression).
      'a.mar-navigation-link.mar-disabled {',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '}',
      'a.mar-navigation-link.mar-disabled:hover::before { background: transparent; }',
      'a.mar-navigation-link.mar-disabled:focus-visible::before { background: transparent; }',
      'a.mar-navigation-link.mar-disabled::after { color: rgba(0, 0, 0, 0.18); }',
      // Row-style subtitle: when the subtitle is the second line of a
      // navigation-link\'s title/subtitle pair, render it smaller and
      // closer to the title (the iOS Settings two-line row look).
      // Outside of that context, .mar-subtitle stays at body-text size
      // so subtitles in section bodies (captions, paragraphs) read
      // naturally.
      'a.mar-navigation-link .mar-subtitle {',
      '  font-size: 14px;',
      '  line-height: 1.3;',
      '}',
      'a.mar-navigation-link .mar-vstack { gap: 2px; }',

      // Centered — pure two-axis alignment. It claims whatever space
      // its PARENT provides (flex: 1 inside the page\'s height-
      // propagating column — #mar-root → .mar-nav-stack →
      // .mar-nav-body all pass the viewport height down) and centers
      // the child in it. No own height: in a parent that hugs (a
      // section card) it simply centers horizontally at content
      // height. Used for full-screen Loading / EmptyState / Error.
      '.mar-centered {',
      '  display: flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  flex: 1 1 auto;',
      '  align-self: stretch;',
      '  width: 100%;',
      '  text-align: center;',
      '}',

      // Spacer — SwiftUI Spacer(). Flex filler that pushes siblings
      // along the parent flex container\'s main axis. Inside an
      // hstack it expands horizontally; inside vstack, vertically.
      '.mar-spacer { flex: 1 1 auto; align-self: stretch; }',

      // Universal sizing — the `width fill` / `height fill` attr
      // classes (set by applyLayoutAttrs on any view). The rules are
      // contextual because what "fill" means depends on the parent\'s
      // flex direction: on the parent\'s MAIN axis the child grows as
      // a flex item (min-size 0 so it truncates instead of
      // overflowing); on the CROSS axis it stretches. So
      // `hstack [] [ text [width fill] a, text [width fill] b ]`
      // yields equal-width columns, and `height fill` in a vstack
      // creates the slack that spacer / centered distribute.
      '.mar-hstack > .mar-w-fill,',
      'a.mar-navigation-link > .mar-w-fill { flex: 1 1 0; min-width: 0; }',
      '.mar-vstack > .mar-w-fill { align-self: stretch; }',
      '.mar-vstack > .mar-h-fill,',
      '.mar-nav-body > .mar-h-fill { flex: 1 1 0; min-height: 0; }',
      '.mar-hstack > .mar-h-fill { align-self: stretch; }',

      // Cross-axis alignment — the stack `align` attr (classes set
      // by applyAlignAttr). Positions HUGGING children in the
      // cross-axis slack; a child with the matching fill has no
      // slack (its stretch rule above wins), so align never resizes.
      // vstack default stays stretch, hstack default stays center —
      // these only fire when the attr is present.
      '.mar-vstack.mar-align-leading  { align-items: flex-start; }',
      '.mar-vstack.mar-align-center   { align-items: center; }',
      '.mar-vstack.mar-align-trailing { align-items: flex-end; }',
      '.mar-hstack.mar-align-top      { align-items: flex-start; }',
      '.mar-hstack.mar-align-center   { align-items: center; }',
      '.mar-hstack.mar-align-bottom   { align-items: flex-end; }',

      // Toggle — mobile-style switch. The native checkbox is hidden
      // via appearance:none and re-drawn as a 51x31 pill with a 27px
      // white thumb that slides on the :checked state. Standard
      // touch-target dimensions.
      '.mar-toggle {',
      '  display: flex; align-items: center; justify-content: space-between;',
      '  gap: 12px;',
      // No padding here — let the parent .mar-section-body > * rule
      // supply 10px 16px (same as text / subtitle / button rows), so
      // the toggle\'s label is inset from the card\'s leading edge
      // and the switch from the trailing edge. Setting `padding`
      // here would override on equal specificity (later wins) and
      // the row would butt against the card borders.
      '  cursor: pointer;',
      '  user-select: none;',
      '}',
      '.mar-toggle-label { flex: 1; min-width: 0; }',
      '.mar-toggle-switch {',
      '  appearance: none; -webkit-appearance: none;',
      '  flex: 0 0 auto;',
      '  width: 51px; height: 31px;',
      '  border-radius: 31px;',
      '  background: #e9e9eb;',
      '  position: relative;',
      '  cursor: pointer;',
      '  transition: background-color 0.2s ease;',
      '  margin: 0;',
      '}',
      '.mar-toggle-switch::before {',
      '  content: "";',
      '  position: absolute;',
      '  top: 2px; left: 2px;',
      '  width: 27px; height: 27px;',
      '  border-radius: 50%;',
      '  background: white;',
      '  box-shadow: 0 3px 8px rgba(0,0,0,0.15), 0 1px 1px rgba(0,0,0,0.06);',
      '  transition: transform 0.2s ease;',
      '}',
      '.mar-toggle-switch:checked { background: #34c759; }',
      '.mar-toggle-switch:checked::before { transform: translateX(20px); }',
      '.mar-toggle-switch:focus-visible {',
      '  outline: none;',
      '  box-shadow: 0 0 0 4px rgba(10, 132, 255, 0.25);',
      '}',
      // Disabled toggle — switch loses its color (or its green if
      // ON), and the label / cursor go inert. `.mar-disabled` is
      // toggled on the <label> wrapper so the label-text faded
      // tone matches the visually-disabled switch.
      '.mar-toggle.mar-disabled { cursor: not-allowed; }',
      '.mar-toggle.mar-disabled .mar-toggle-label {',
      '  color: rgba(0, 0, 0, 0.36);',
      '}',
      '.mar-toggle-switch:disabled {',
      '  opacity: 0.5;',
      '  cursor: not-allowed;',
      '}',

      // ---------- Sheet (modal page sheet) ----------
      //
      // iOS-style sheet: slides up from the bottom, leaves a sliver of
      // the parent visible at the top, dims + blurs the parent behind a
      // backdrop. The DOM structure is:
      //
      //   .mar-sheet-backdrop (full-screen overlay; click → dismiss)
      //     .mar-sheet-panel (the actual sheet card)
      //       .mar-sheet-handle (drag affordance — non-functional v1)
      //       {sheet content}
      //
      // The backdrop is ALWAYS in the DOM (so re-renders can flip
      // open/close without DOM churn) but display: none when closed so
      // it never blocks clicks on the parent.
      '.mar-sheet-backdrop {',
      '  position: fixed; inset: 0;',
      '  background: rgba(0, 0, 0, 0.4);',
      '  -webkit-backdrop-filter: blur(8px);',
      '  backdrop-filter: blur(8px);',
      // ALWAYS display: flex (not toggled to none) so CSS transitions
      // can animate from closed → open. `display: none → flex` is a
      // discrete change the browser never tweens — the sheet would
      // pop in instantly. opacity 0 + pointer-events: none makes the
      // closed state both invisible and click-through.
      '  display: flex; flex-direction: column;',
      '  justify-content: flex-end;',
      '  z-index: 2000;',
      '  opacity: 0;',
      '  pointer-events: none;',
      '  transition: opacity 280ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',
      '.mar-sheet-backdrop.mar-sheet-open {',
      '  opacity: 1;',
      '  pointer-events: auto;',
      '}',

      // Panel — the actual sheet card.
      // - Page sheet style: leaves ~10vh of the parent visible at top.
      // - Rounded top corners only (bottom flush with screen edge).
      // - White background with subtle inset highlight on top edge.
      // - Slide-up animation with iOS-spec cubic-bezier.
      // Panel sizes to its content (no min-height). Previously had
      // min-height: 50vh which forced a half-screen panel even with
      // a one-line message — large empty area under the content.
      // iOS native page-sheets also size to content; users can drag
      // the handle to expand to detents, which we don't have yet.
      // The 90vh cap keeps an over-long content from pinning the
      // backdrop offscreen.
      //
      // Padding moved here from the children: 20px horizontal,
      // 20px bottom, 4px top (the handle's own 8px margin
      // contributes the rest at the top). Content inside the sheet
      // gets its breathing room without each child having to
      // remember to indent.
      '.mar-sheet-panel {',
      '  background: #f5f5f7;',
      '  border-radius: 14px 14px 0 0;',
      '  max-height: 90vh;',
      '  width: 100%;',
      '  max-width: 1024px;',
      '  margin: 0 auto;',
      '  overflow-y: auto;',
      '  overflow-x: hidden;',
      '  padding: 4px 20px 20px;',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 -8px 32px rgba(0, 0, 0, 0.12);',
      '  transform: translateY(100%);',
      '  transition: transform 320ms cubic-bezier(0.32, 0.72, 0, 1);',
      '  position: relative;',
      '}',
      '.mar-sheet-open .mar-sheet-panel { transform: translateY(0); }',

      // Drag handle — small pill at the top-center, signals "this can
      // be dragged down to dismiss". Visual affordance only in v1; the
      // drag gesture itself is iOS-native (page sheet) and not yet
      // wired up on web.
      //
      // Negative horizontal margins cancel the panel\'s 20px side
      // padding so the handle stays centered against the panel\'s
      // outer edges, not the content area.
      '.mar-sheet-handle {',
      '  width: 36px; height: 5px;',
      '  background: rgba(0, 0, 0, 0.2);',
      '  border-radius: 2.5px;',
      '  margin: 8px auto 14px;',
      '  flex-shrink: 0;',
      '}',

      // Page-level containers (form, list, nav-body) inside a sheet
      // already supply their own card padding via .mar-section etc;
      // they shouldn\'t double-pad against the sheet\'s own. Cancel
      // the sheet's horizontal padding for them via negative margin
      // so they get full-width treatment like a normal page.
      '.mar-sheet-panel > .mar-form,',
      '.mar-sheet-panel > .mar-list,',
      '.mar-sheet-panel > .mar-nav-body {',
      '  margin-left: -20px;',
      '  margin-right: -20px;',
      '}',

      // Dark mode adjustments — match the rest of the runtime\'s dark
      // theme without re-stating every rule.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-sheet-backdrop { background: rgba(0, 0, 0, 0.6); }',
      '  .mar-sheet-panel {',
      '    background: #1c1c1e;',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.08),',
      '      0 -8px 32px rgba(0, 0, 0, 0.4);',
      '  }',
      '  .mar-sheet-handle { background: rgba(255, 255, 255, 0.3); }',
      '}',

      // ---------- Confirm dialog (UI.confirm) ----------
      //
      // Apple-Music-style destructive confirmation. Structure:
      //
      //   .mar-confirm-backdrop (fixed full-screen + blur)
      //     .mar-confirm-dialog (centered card, max 320px)
      //       .mar-confirm-title (the question)
      //       .mar-confirm-actions (horizontal row of buttons)
      //         .mar-confirm-cancel
      //         .mar-confirm-confirm[.destructive]
      //
      // Unlike .mar-sheet-backdrop, this one is mounted only when
      // the modal is showing — the parent's `case` returns
      // `UI.confirm {...}` only when active, `UI.empty` otherwise.
      // So no opacity-toggle hack needed; we just animate the
      // entrance from the moment the element mounts.
      '.mar-confirm-backdrop {',
      '  position: fixed; inset: 0;',
      '  background: rgba(0, 0, 0, 0.4);',
      '  -webkit-backdrop-filter: blur(8px) saturate(180%);',
      '  backdrop-filter: blur(8px) saturate(180%);',
      '  display: flex; align-items: center; justify-content: center;',
      '  z-index: 2100;',  // above sheet (2000)
      '  animation: mar-confirm-backdrop-in 200ms ease-out;',
      '  padding: 24px;',
      '}',
      '@keyframes mar-confirm-backdrop-in {',
      '  from { opacity: 0; }',
      '  to   { opacity: 1; }',
      '}',
      // Dialog card. Width capped at 320px (Apple Music print). Two
      // stacked sections (title up top, actions below). Subtle
      // shadow so it floats off the backdrop.
      '.mar-confirm-dialog {',
      '  background: rgba(245, 245, 247, 0.96);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.08);',
      '  border-radius: 14px;',
      '  width: 100%;',
      '  max-width: 320px;',
      '  padding: 18px 18px 12px;',
      '  box-shadow:',
      '    0 16px 48px rgba(0, 0, 0, 0.18),',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8);',
      '  animation: mar-confirm-dialog-in 220ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',
      '@keyframes mar-confirm-dialog-in {',
      '  from { opacity: 0; transform: scale(0.92); }',
      '  to   { opacity: 1; transform: scale(1); }',
      '}',
      '.mar-confirm-title {',
      '  margin: 0 0 16px;',
      '  font: 600 15px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;',
      '  color: #000;',
      '  text-align: center;',
      '  line-height: 1.35;',
      '}',
      '.mar-confirm-actions {',
      '  display: flex; gap: 8px;',
      '}',
      // Both buttons share the same chrome — pill shape, full
      // height. Cancel is system fill, Confirm has the accent color
      // (red when destructive=True, blue otherwise).
      '.mar-confirm-actions > button {',
      '  flex: 1;',
      '  appearance: none;',
      '  border: none;',
      '  padding: 10px 14px;',
      '  border-radius: 980px;',
      '  font: 500 15px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;',
      '  cursor: pointer;',
      '  transition: background 150ms ease, transform 100ms ease;',
      '  -webkit-tap-highlight-color: transparent;',
      '  touch-action: manipulation;',
      '}',
      '.mar-confirm-actions > button:active { transform: scale(0.97); }',
      '.mar-confirm-cancel {',
      '  background: rgba(0, 0, 0, 0.06);',
      '  color: #000;',
      '}',
      '.mar-confirm-cancel:hover { background: rgba(0, 0, 0, 0.10); }',
      // Non-destructive confirm — system blue accent.
      '.mar-confirm-confirm {',
      '  background: #007aff;',
      '  color: #ffffff;',
      '}',
      '.mar-confirm-confirm:hover { background: #0066d6; }',
      // Destructive variant — system red. Same iOS color as
      // .mar-row-delete and SwiftUI .destructive role.
      '.mar-confirm-confirm.destructive {',
      '  background: #ff3b30;',
      '  color: #ffffff;',
      '}',
      '.mar-confirm-confirm.destructive:hover { background: #e0352b; }',

      // Dark mode — switch the dialog card to dark glass + adjust
      // button colors so contrast stays readable.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-confirm-backdrop { background: rgba(0, 0, 0, 0.6); }',
      '  .mar-confirm-dialog {',
      '    background: rgba(44, 44, 46, 0.96);',
      '    border-color: rgba(255, 255, 255, 0.08);',
      '    box-shadow:',
      '      0 16px 48px rgba(0, 0, 0, 0.5),',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.1);',
      '  }',
      '  .mar-confirm-title { color: #ffffff; }',
      '  .mar-confirm-cancel {',
      '    background: rgba(255, 255, 255, 0.10);',
      '    color: #ffffff;',
      '  }',
      '  .mar-confirm-cancel:hover { background: rgba(255, 255, 255, 0.16); }',
      '  .mar-confirm-confirm { background: #0a84ff; }',
      '  .mar-confirm-confirm:hover { background: #0070dd; }',
      '  .mar-confirm-confirm.destructive { background: #ff453a; }',
      '  .mar-confirm-confirm.destructive:hover { background: #e63d34; }',
      '}',

      // ---------- Page transitions ----------
      //
      // The mar runtime wraps a navigation\'s DOM swap in
      // `document.startViewTransition(swap)` when the View
      // Transitions API exists (Chrome 111+, Safari 18+,
      // Firefox 129+ behind flag). The browser captures the old
      // tree as `::view-transition-old(root)` and the new tree
      // as `::view-transition-new(root)`; we drive the actual
      // animation here with CSS keyframes selected by the
      // `data-mar-nav-dir` attribute the runtime stamps on
      // <html> before the swap.
      //
      // The keyframes mirror iOS NavigationStack:
      //   forward (push) — new slides in from the right, old
      //     parallaxes slightly left and dims.
      //   back (pop)     — reversed: new slides in from the left,
      //     old slides out to the right.
      //
      // Timing curve is the standard "page push" easing —
      // cubic-bezier(0.32, 0.72, 0, 1) — applied at 400ms. Below
      // ~320ms the slide reads as a snap; above ~480ms it starts
      // feeling sluggish on a browser.
      '@keyframes mar-slide-in-right { from { transform: translateX(100%); } }',
      '@keyframes mar-slide-out-left  {  to  { transform: translateX(-25%); opacity: 0.5; } }',
      '@keyframes mar-slide-in-left  { from { transform: translateX(-25%); opacity: 0.5; } }',
      '@keyframes mar-slide-out-right {  to  { transform: translateX(100%); } }',
      'html[data-mar-nav-dir="forward"]::view-transition-old(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-out-left;',
      '}',
      'html[data-mar-nav-dir="forward"]::view-transition-new(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-in-right;',
      '}',
      'html[data-mar-nav-dir="back"]::view-transition-old(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-out-right;',
      '}',
      'html[data-mar-nav-dir="back"]::view-transition-new(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-in-left;',
      '}',
      // ----- Fade transition (replace / replaceFresh) -----
      //
      // Cross-fade with a subtle scale-up on the incoming view. Used
      // when the navigation isn't a stack movement but a context
      // change (logout, sign-in completed, auth-expired redirect).
      //
      // The visual idiom is what iOS uses for Sign in with Apple
      // confirmations, Apple Pay completion, FaceID success → app
      // unlock, and the Photos / Apple Music "you switched modes"
      // transitions: outgoing dissolves while the new content
      // settles in from slightly smaller (0.96 → 1.0). No
      // horizontal motion → reads as "new context", not "next/prev
      // screen". Duration sits at 280ms — fast enough to feel
      // responsive after a tap, slow enough that the scale registers
      // as deliberate rather than a flash.
      //
      // Easing is the same iOS curve used for the slide so the
      // perceived "personality" stays consistent across nav verbs.
      '@keyframes mar-fade-out { to { opacity: 0; } }',
      '@keyframes mar-fade-in-scale {',
      '  from { opacity: 0; transform: scale(0.96); }',
      '  to   { opacity: 1; transform: scale(1); }',
      '}',
      'html[data-mar-nav-dir="fade"]::view-transition-old(root) {',
      '  animation: 220ms cubic-bezier(0.32, 0.72, 0, 1) both mar-fade-out;',
      '}',
      'html[data-mar-nav-dir="fade"]::view-transition-new(root) {',
      '  animation: 280ms cubic-bezier(0.32, 0.72, 0, 1) both mar-fade-in-scale;',
      '  transform-origin: center center;',
      '}',
      // Dev dock pin. The dock element carries
      // `view-transition-name: mar-dev-dock` (see createDevDock),
      // which splits it out of the `root` snapshot group into its
      // own. `animation: none` here keeps that group static
      // through the page transition — so the bottom-right widget
      // stays anchored while the page slides under it. Without
      // this rule the browser would default to a cross-fade,
      // which on a static element with identical old/new content
      // looks like a brief flicker.
      '::view-transition-group(mar-dev-dock),',
      '::view-transition-old(mar-dev-dock),',
      '::view-transition-new(mar-dev-dock) {',
      '  animation: none !important;',
      '}',
      // Accessibility — kill the slide for users who set
      // `prefers-reduced-motion`. The runtime also skips the
      // startViewTransition call entirely in that case, but the
      // CSS guard belongs here too for any non-runtime triggers
      // (e.g. dev-tools forcing a transition).
      '@media (prefers-reduced-motion: reduce) {',
      '  ::view-transition-old(root), ::view-transition-new(root) {',
      '    animation: none !important;',
      '  }',
      '  .mar-nav-inline-title { transition: none; }',
      '}',

      // Dark mode — iOS 26 "Liquid Glass" on a near-black graphite
      // page. The gradient hints at light coming from above; the
      // glass surfaces (cards, pills) tint that light bluish-warm
      // via subtle saturation in the backdrop filter.
      '@media (prefers-color-scheme: dark) {',
      // Match <html> to the darkest stop of the body gradient. Same
      // rationale as the light-mode rule above: bottom-bounce
      // overscroll and view-transition gaps would otherwise flash
      // white through the dark page. Also add an explicit
      // ::selection so highlighted text stays legible — the
      // browser's default selection color on macOS is a light
      // blue/grey that washes out the page's #f5f5f7 text into
      // near-invisibility against the highlight.
      '  html { background-color: #161618; }',
      '  ::selection { background: rgba(10, 132, 255, 0.55); color: #ffffff; }',
      '  body {',
      '    background: linear-gradient(180deg, #232326 0%, #1d1d1f 60%, #161618 100%);',
      '    color: #f5f5f7;',
      '  }',
      // Glass pills (back button, trailing nav buttons, inline
      // hstack actions) all share the same translucent white tint
      // — the backdrop blur picks up the page gradient behind.
      '  .mar-nav-back, .mar-nav-side button, .mar-nav-inline-title {',
      '    background: rgba(255, 255, 255, 0.08);',
      '    border: 0.5px solid rgba(255, 255, 255, 0.12);',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.15),',
      '      0 2px 8px rgba(0, 0, 0, 0.3);',
      '  }',
      '  .mar-nav-back { color: #0a84ff; }',
      '  @media (hover: hover) { .mar-nav-back:hover { background: rgba(255, 255, 255, 0.14); } }',
      '  .mar-nav-side button, .mar-nav-inline-title { color: #f5f5f7; }',
      '  @media (hover: hover) { .mar-nav-side button:hover { background: rgba(255, 255, 255, 0.14); } }',
      // Section card in dark glass — translucent slate that lets
      // the gradient bleed through under the blur.
      '  .mar-section-body {',
      '    background: rgba(44, 44, 46, 0.7);',
      '    border: 0.5px solid rgba(255, 255, 255, 0.06);',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.08),',
      '      0 8px 24px rgba(0, 0, 0, 0.35);',
      '  }',
      '  .mar-section-body > * { border-bottom-color: rgba(255, 255, 255, 0.08); }',
      '  .mar-section-body > *:last-child { border-bottom: none; }',
      '  .mar-section-header, .mar-section-footer { color: #86868b; }',
      '  .mar-textfield::placeholder { color: #86868b; }',
      '  .mar-textfield {',
      '    border-color: rgba(255, 255, 255, 0.12);',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: #f5f5f7;',
      '  }',
      '  .mar-textfield:focus {',
      '    background: rgba(255, 255, 255, 0.06);',
      '    border-color: rgba(10, 132, 255, 0.6);',
      '    box-shadow: 0 0 0 4px rgba(10, 132, 255, 0.18);',
      '  }',
      // Disabled textfield in dark mode. The light-mode rule uses
      // `color: rgba(0,0,0,0.36)` — 36% black on a near-black
      // surface in dark mode lands the text essentially invisible.
      // Operator reported they couldn't read the email address
      // they had just typed while the "Sending…" request was in
      // flight. Mirror the dimming PROPORTIONALLY against white,
      // not by reusing the light-mode rgba: text goes to ~50%
      // white (still clearly readable), placeholder to ~30%, and
      // the surface stays the same translucent slate as the
      // enabled state so the field's outline doesn't shift.
      '  .mar-textfield:disabled {',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: rgba(255, 255, 255, 0.5);',
      '    border-color: rgba(255, 255, 255, 0.08);',
      '  }',
      '  .mar-textfield:disabled::placeholder { color: rgba(255, 255, 255, 0.3); }',
      // Primary CTAs stay solid blue, just brighter for legibility
      // on the darker surface.
      '  .mar-section-body > button {',
      '    background: #0a84ff; color: white;',
      '  }',
      '  .mar-section-body > button:hover { background: #2997ff; }',
      '  .mar-section-body > button:disabled {',
      '    background: #3a3a3c; color: rgba(255, 255, 255, 0.4);',
      '  }',
      '  .mar-section-body > button:disabled:hover { background: #3a3a3c; }',
      // Secondary (hstack) buttons in dark mode — same glass
      // treatment with blue text. Exclusions match the light-mode
      // selectors above (drag handle + delete button keep their
      // own chrome).
      '  .mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete) {',
      '    background: rgba(255, 255, 255, 0.08);',
      '    border: 0.5px solid rgba(255, 255, 255, 0.12);',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.15),',
      '      0 2px 8px rgba(0, 0, 0, 0.3);',
      '    color: #0a84ff;',
      '  }',
      '  .mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):hover { background: rgba(255, 255, 255, 0.14); }',
      '  .mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):disabled {',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: rgba(255, 255, 255, 0.3);',
      '    box-shadow: none;',
      '  }',
      '  .mar-hstack > button:not(.mar-drag-handle):not(.mar-row-delete):disabled:hover { background: rgba(255, 255, 255, 0.04); }',
      // Toggle in dark mode: off-track turns dark grey, on-track
      // keeps the same iOS green; thumb stays white.
      '  .mar-toggle-switch { background: #39393d; }',
      '  .mar-toggle-switch:checked { background: #34c759; }',
      '}',

      // ===== onDelete per-row affordance (edit-mode only) =====
      //
      // Single visual mode: when the section is in delete-editing
      // state, every row carries a red `−` circle at its LEFT (iOS
      // Mail/Notes/Reminders edit-mode chrome). Outside edit mode
      // the button isn't rendered at all — see
      // attachRowDeleteAffordance — so this CSS doesn't need to
      // cover that case.
      //
      // We use compound selectors (`.mar-section-body
      // .mar-row-delete`) for size + shape so we win the cascade
      // against the generic `.mar-hstack > button { flex: 0 0
      // auto; }` rule defined earlier; the row that hosts the
      // button IS an `.mar-hstack` in edit mode (Mar code wraps
      // the row in `hstack [key] [text ...]` to attach the
      // reorder key), and without enough specificity the chrome
      // styles bleed in and turn the disc into a stretched pill.
      '.mar-section-body .mar-row-delete {',
      '  flex: 0 0 22px;',
      '  width: 22px;',
      '  min-width: 22px;',
      '  height: 22px;',
      '  min-height: 22px;',
      '  border-radius: 50%;',
      '  border: none;',
      '  padding: 0;',
      '  background: #ff3b30;',
      '  color: #ffffff;',
      '  display: inline-flex;',
      '  align-items: center;',
      '  justify-content: center;',
      '  cursor: pointer;',
      '  -webkit-tap-highlight-color: transparent;',
      '  transition: transform 150ms ease;',
      '  box-shadow: none;',
      '}',
      '.mar-section-body .mar-row-delete:hover { background: #e0352b; }',
      '.mar-section-body .mar-row-delete:active { transform: scale(0.92); }',
      // Row layout in edit-with-delete mode: row becomes a flex
      // container so the delete button (order:-1) can sit on the
      // left of whatever the row's normal content is. The
      // negative margin pokes the disc out into the section
      // card\'s padding gutter — same offset iOS uses.
      '.mar-section-delete-edit > * {',
      '  display: flex;',
      '  align-items: center;',
      '  gap: 12px;',
      '}',
      '.mar-section-delete-edit > * > .mar-row-delete {',
      '  order: -1;',
      '  margin-left: -4px;',
      '}',
      // Exit animation is driven imperatively via Web Animations
      // API in attachRowDeleteAffordance — see that function for
      // the keyframes (height collapse + slide-left + fade). The
      // imperative path needs concrete `from` keyframes (current
      // height, current padding), so the animation has to be
      // built per-row at click time, not via a fixed CSS class.
      // Dark mode: tone the red down — full system-red against a
      // dark background reads harsh.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-section-body .mar-row-delete { background: #ff453a; }',
      '  .mar-section-body .mar-row-delete:hover { background: #e63d34; }',
      '}',
    ].join('\n');
    document.head.appendChild(style);
  }

  // domTagFor returns the HTML element name for a VView tag.
  // Tags map to semantic HTML so dark mode / accessibility work out of
  // the box; CSS in ensureUIStyles() gives each a SwiftUI-flavored look.
  function domTagFor(viewTag) {
    switch (viewTag) {
      case 'text':            return 'span';
      case 'title':           return 'h1';
      case 'subtitle':        return 'h2';
      case 'errorText':       return 'p';
      case 'image':           return 'img';
      case 'paragraph':       return 'p';
      // span: <a> when an inlineLink attr is present, <span>
      // otherwise. createDOM handles the link case directly because
      // the choice depends on attrs, not just tag. Leaving the
      // default here at 'span' keeps the no-link path simple.
      case 'span':            return 'span';
      case 'button':          return 'button';
      case 'navigationLink':  return 'a';
      case 'navigationStack': return 'main';
      case 'form':            return 'form';
      case 'uiList':          return 'ul';
      case 'uiSection':       return 'section';
      case 'uiKeyedList':     return 'section';
      case 'hstack':          return 'div';
      case 'vstack':          return 'div';
      case 'textField':       return 'input';
      case 'textArea':        return 'textarea';
      case 'picker':          return 'div';
      case 'datePicker':      return 'input';
      case 'spacer':          return 'div';
      case 'toggle':          return 'label';
      case 'sheet':           return 'div';  // .mar-sheet-backdrop wrapper
      case 'confirmDialog':   return 'div';  // .mar-confirm-backdrop wrapper
      default:                return 'div';
    }
  }

  // datePicker <-> <input type="date">. The native control speaks a
  // bare "YYYY-MM-DD" calendar date with no timezone, so we read and
  // write in the LOCAL calendar: the day shown is the day stored.
  // (Matches iOS DatePicker, which shows the device-local date.) The
  // bound value is a concrete `Time` (datePicker is pure — the program
  // owns the value and seeds "today" via `Cmd.perform GotToday Time.now`),
  // so we read it directly with no clock fallback. Changes parse back to
  // a VTime at local midnight.
  function dateInputValue(view) {
    const a = (view.attrs || []).find(x => x.name === 'value');
    const mv = a && a.value;
    let d;
    if (mv && typeof mv.millis === 'number') {
      d = new Date(mv.millis);
    } else {
      d = new Date(0); // unset — a pure datePicker always receives a Time
    }
    const p = (n) => (n < 10 ? '0' : '') + n;
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
  }
  function parseDateInput(str) {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(str || '');
    if (!m) return null;
    const ms = new Date(+m[1], +m[2] - 1, +m[3]).getTime(); // local midnight
    return Number.isFinite(ms) ? VTime(ms) : null;
  }

  function createDOM(view) {
    if (!view || view.k !== 'V') {
      return document.createElement('span');
    }
    if (view.tag === 'empty') {
      // Use a no-op span as a stable placeholder so diff has a 1:1 mapping.
      const e = document.createElement('span');
      e.style.display = 'none';
      setMarView(e, view);
      return e;
    }
    // span: tag depends on attrs (link → <a>, otherwise <span>).
    // Resolve up-front so we don\'t create a wrong element and
    // re-wrap it. All the styling lives in CSS classes added below.
    let tag = domTagFor(view.tag);
    if (view.tag === 'span') {
      const linkAttr = (view.attrs || []).find(a => a.name === 'inlineLink');
      if (linkAttr) tag = 'a';
    }
    const e = document.createElement(tag);
    setMarView(e, view);
    switch (view.tag) {
      case 'text':
        e.textContent = view.text;
        break;
      case 'paragraph':
        // Block of flowing inline content. Children are `span`
        // VViews; createDOM recurses to build the right inline
        // elements. Class drives the block-level styling (margin,
        // line-height); the children carry their own per-run
        // styling.
        ensureUIStyles();
        e.className = 'mar-paragraph';
        for (const child of (view.children || [])) {
          e.appendChild(createDOM(child));
        }
        break;
      case 'span': {
        // Inline run. Compose CSS classes from the attrs: bold,
        // italic, strikethrough, code; the linkAttr (resolved
        // above) becomes an href. All classes are independent so
        // `[bold, code]` (bold inline code) works without
        // special-casing.
        ensureUIStyles();
        const classes = ['mar-inline'];
        let href = null;
        for (const a of (view.attrs || [])) {
          switch (a.name) {
            case 'inlineBold':          classes.push('mar-inline-bold'); break;
            case 'inlineItalic':        classes.push('mar-inline-italic'); break;
            case 'inlineStrikethrough': classes.push('mar-inline-strike'); break;
            case 'inlineCode':          classes.push('mar-inline-code'); break;
            case 'inlineLink':          href = a.value && a.value.s; break;
          }
        }
        if (href) {
          classes.push('mar-inline-link');
          e.setAttribute('href', href);
          // External target — link inline text always opens off-
          // site (we don\'t have an "internal inline link"
          // primitive; navigationLink covers internal). New tab +
          // noopener so the source page can\'t be poked at.
          e.setAttribute('target', '_blank');
          e.setAttribute('rel', 'noopener noreferrer');
        }
        e.className = classes.join(' ');
        e.textContent = view.text;
        break;
      }
      case 'title':
        ensureUIStyles();
        e.className = 'mar-title';
        e.textContent = view.text;
        break;
      case 'subtitle':
        ensureUIStyles();
        e.className = 'mar-subtitle';
        e.textContent = view.text;
        break;
      case 'errorText':
        // Red + semi-bold via .mar-error-text. role="alert" so
        // assistive tech announces the message when it appears
        // (errors typically arrive after an action, not at page
        // load — the live-region announcement is the whole point).
        ensureUIStyles();
        e.className = 'mar-error-text';
        e.setAttribute('role', 'alert');
        e.textContent = view.text;
        break;
      case 'image':
        // <img> from src + alt. alt is always present (required record
        // field on the Mar side); "" is the decorative escape. lazy +
        // async so off-screen images don't block first paint.
        ensureUIStyles();
        e.className = 'mar-image';
        e.setAttribute('src', getAttr(view, 'src'));
        e.setAttribute('alt', getAttr(view, 'alt'));
        e.setAttribute('loading', 'lazy');
        e.setAttribute('decoding', 'async');
        applyImageAttrs(e, view);
        break;
      case 'button':
        e.textContent = view.text;
        applyDisabledAttr(e, view);
        attachClickDispatcher(e);
        break;
      case 'navigationLink':
        // Tappable navigation: full-width anchor wrapping the
        // child view DOM. The delegated SPA-routing interceptor
        // in mountPages catches the click via `[href^="/"]` and
        // pushes through history.pushState instead of doing a
        // full reload.
        //
        // `disabled` here can't use the HTML disabled property —
        // <a> doesn't have one. Instead we mark the link with
        // aria-disabled + a CSS class; the delegated click handler
        // (onLinkClick) reads __marView.attrs and skips
        // navigation when disabled, and the CSS dims + sets
        // pointer-events: none so hover stays inert.
        //
        // `tabindex="0"` forces the link into the keyboard tab
        // order. Without it, Safari on macOS skips <a> elements by
        // default (it considers links "content", not "controls",
        // unless the user enables Settings > Advanced > "Press
        // Tab to highlight each item on a webpage"). For an app
        // where navigationLink IS a primary control — Settings-row
        // style "go to next screen" — we want every browser to
        // tab through it without the user fiddling with system
        // preferences. tabindex="0" is a no-op in Chrome/Firefox
        // (they tab to links anyway) and the right knob for Safari.
        ensureUIStyles();
        e.className = 'mar-navigation-link';
        e.setAttribute('href', getAttr(view, 'href'));
        e.setAttribute('tabindex', '0');
        applyAnchorDisabled(e, view);
        for (const child of view.children) {
          e.appendChild(createDOM(child));
        }
        break;
      // ---------- UI.* (SwiftUI-style) ----------
      //
      // Each UI container uses a STABLE child-DOM structure so patchDOM
      // can update text/children in place without recreating nodes.
      // Critical for inputs: replacing a node mid-keystroke kills focus
      // and selection, which means the user types "teste" and only "t"
      // makes it through (every re-render after the first character
      // recreates the input, blurring it).
      case 'navigationStack':
        // <main>
        //   <div.mar-nav-toolbar-row> ← always present; pinned bar
        //   <header.mar-nav-bar>      ← always present; large title
        //   <div.mar-nav-body>        ← stable wrapper, children diffed
        // </main>
        ensureUIStyles();
        e.className = 'mar-nav-stack';
        for (const chromeEl of buildNavigationChrome(view)) e.appendChild(chromeEl);
        {
          const body = document.createElement('div');
          body.className = 'mar-nav-body';
          for (const c of view.children) body.appendChild(createDOM(c));
          e.appendChild(body);
        }
        break;
      case 'form':
        // <form>{children}</form> — children diffed positionally.
        // Suppress the default Enter-submits behavior; per-field
        // submit is handled by the UI.submit attr.
        ensureUIStyles();
        e.className = 'mar-form';
        e.addEventListener('submit', (ev) => ev.preventDefault());
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'uiList':
        // <div.mar-list>{sections-or-rows}</div> — always a div
        // wrapper with children rendered directly. Avoids <li>
        // wrapping (semantic noise without payoff for sectioned
        // lists). No reorder semantics on the list itself — those
        // live on its constituent `section`s (see the uiSection
        // ctor for handle wiring + keyboard a11y).
        ensureUIStyles();
        e.className = 'mar-list';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'uiSection':
      case 'uiKeyedList': {
        // <section>
        //   <h2.mar-section-header>  ← always present; hidden if empty
        //   <div.mar-section-body>   ← stable wrapper, children diffed
        //   <p.mar-section-footer>   ← always present; hidden if empty
        // </section>
        //
        // uiSection and uiKeyedList share the same visual chrome
        // (rounded card with header + footer). The semantic
        // difference is at the type level: uiKeyedList children
        // each carry a `key` attr (injected by `UI.keyed`), while
        // uiSection children don\'t. The reconciler reads the key
        // for keyed children automatically — no special-casing
        // needed here.
        ensureUIStyles();
        e.className = 'mar-section';
        const headerText = getAttr(view, 'header');
        const footerText = getAttr(view, 'footer');
        const h = document.createElement('h2');
        h.className = 'mar-section-header';
        h.textContent = headerText;
        if (!headerText) h.style.display = 'none';
        e.appendChild(h);
        const body = document.createElement('div');
        body.className = 'mar-section-body';
        // Reorder support — driven by the `onMove` attr's editing
        // flag. In edit mode each row gets a drag handle (mouse /
        // touch) AND becomes keyboard-focusable with grab + arrow
        // semantics (Padrão 2, screen-reader friendly). Both
        // gestures dispatch the same onMove(from, to) handler.
        const onMoveAttrS = getAttrRaw(view, 'onMove');
        const editingS = !!(onMoveAttrS && onMoveAttrS.editing);
        const handlerS = onMoveAttrS && onMoveAttrS.handler;
        const onDeleteAttrS = getAttrRaw(view, 'onDelete');
        const deleteEditingS = !!(onDeleteAttrS && onDeleteAttrS.editing);
        const deleteHandlerS = onDeleteAttrS && onDeleteAttrS.handler;
        if (editingS) body.classList.add('mar-section-editing');
        // `.mar-section-delete-edit` flips the section body into the
        // layout that hosts a per-row delete affordance on the left
        // of each row (iOS edit-mode minus circle). Only on when
        // the app explicitly opts into onDelete + editing=true; in
        // browse mode the rows are plain (no destructive control
        // surface).
        if (deleteEditingS) body.classList.add('mar-section-delete-edit');
        const totalS = view.children.length;
        for (let i = 0; i < totalS; i++) {
          const childEl = createDOM(view.children[i]);
          if (editingS) {
            ensureRowEditAffordances(childEl, i, totalS);
          }
          if (deleteHandlerS) {
            attachRowDeleteAffordance(childEl, i, deleteHandlerS, deleteEditingS);
          }
          body.appendChild(childEl);
        }
        if (editingS && handlerS) {
          attachListReorderDrag(body, handlerS);
          // Live-region hosted on the section wrapper (`e`), not on
          // the body. See the function for why — TLDR: appending to
          // body shifts the last actual row out of `:last-child` and
          // it gains a hairline border, reading as a phantom extra
          // row.
          attachListReorderKeyboard(body, handlerS, e);
        }
        e.appendChild(body);
        const f = document.createElement('p');
        f.className = 'mar-section-footer';
        f.textContent = footerText;
        if (!footerText) f.style.display = 'none';
        e.appendChild(f);
        break;
      }
      case 'hstack':
        ensureUIStyles();
        e.className = 'mar-hstack';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'vstack':
        ensureUIStyles();
        e.className = 'mar-vstack';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'textField': {
        ensureUIStyles();
        e.className = 'mar-textfield';
        applyInputKind(e, view);
        applySizing(e, view);
        const ph = getAttr(view, 'placeholder');
        if (ph) e.setAttribute('placeholder', ph);
        e.value = view.text;
        // `<input disabled>` natively suppresses focus, typing, AND
        // the `input` / `keydown` events — so neither
        // attachInputDispatcher nor attachSubmitDispatcher needs an
        // extra guard once we sync the DOM property here.
        applyDisabledAttr(e, view);
        attachInputDispatcher(e);
        attachSubmitDispatcher(e);
        break;
      }
      case 'textArea': {
        // Same dispatcher wiring as textField — the renderer just
        // emits a <textarea> instead of <input>. We reuse the
        // textfield CSS class so the borders / focus ring / padding
        // line up with neighboring textField rows; a small
        // textArea-specific override (min-height, line-height)
        // lives alongside.
        ensureUIStyles();
        e.className = 'mar-textfield mar-textarea';
        const ph = getAttr(view, 'placeholder');
        if (ph) e.setAttribute('placeholder', ph);
        e.value = view.text;
        // A reasonable default — three lines of room without
        // committing to a tall fixed block. User code can override
        // via `[ height (lines N) ]` (applySizing sets rows).
        if (!e.hasAttribute('rows')) e.setAttribute('rows', '3');
        applySizing(e, view);
        applyDisabledAttr(e, view);
        attachInputDispatcher(e);
        attachSubmitDispatcher(e);
        break;
      }
      case 'datePicker': {
        // Native <input type="date">. The browser supplies the
        // calendar popover; we reuse the textfield chrome so the row
        // lines up with neighboring textField / picker rows, and sync
        // the value as a local YYYY-MM-DD. Changes parse back to a
        // VTime (local midnight) and dispatch onChange — same shape as
        // picker / toggle (apply view.msg to the picked value).
        ensureUIStyles();
        e.className = 'mar-textfield mar-datepicker';
        e.type = 'date';
        applySizing(e, view);
        e.value = dateInputValue(view);
        applyDisabledAttr(e, view);
        e.addEventListener('change', () => {
          const v = e.__marView;
          if (!currentDispatch || !v || v.msg == null) return;
          const t = parseDateInput(e.value);
          if (!t) return;
          currentDispatch(apply(v.msg, t));
        });
        break;
      }
      case 'picker': {
        // <div.mar-picker>
        //   <select.mar-picker-select>
        //     <option value="0">Label1</option>
        //     <option value="1" selected>Label2</option>
        //     ...
        //   </select>
        //   <span.mar-picker-chevron aria-hidden>›</span>
        // </div>
        //
        // The wrapping div lets us layer a custom chevron over the
        // native select without losing the platform's accessible
        // dropdown UI (keyboard nav, screen-reader announcements,
        // mobile native popover). The chevron is decorative — the
        // <select> itself is the focusable / interactive surface.
        ensureUIStyles();
        e.className = 'mar-picker';
        applySizing(e, view);
        const select = document.createElement('select');
        select.className = 'mar-picker-select';
        renderPickerOptions(select, view);
        // `<select disabled>` natively blocks the dropdown from
        // opening and suppresses change events — so we sync the
        // property on the inner element, not the wrapping div.
        // Mirror the disabled state onto the wrapper too so CSS
        // can grey-out the chevron + text via :has() / class.
        applyDisabledAttr(select, view);
        e.classList.toggle('mar-disabled', isDisabled(view));
        e.appendChild(select);
        // Inline SVG renders consistently across browsers / fonts —
        // unlike a unicode chevron glyph which Safari + Chrome
        // sometimes render as a thin stub or fail to size with the
        // surrounding CSS font-size. Two stacked triangles mirror
        // SwiftUI's Picker(.menu) "select-up-down" indicator.
        const chevron = document.createElement('span');
        chevron.className = 'mar-picker-chevron';
        chevron.setAttribute('aria-hidden', 'true');
        chevron.innerHTML =
          '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="18" ' +
          'viewBox="0 0 14 18" fill="none" stroke="currentColor" ' +
          'stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
          '<polyline points="3,7 7,3 11,7"/>' +
          '<polyline points="3,11 7,15 11,11"/>' +
          '</svg>';
        e.appendChild(chevron);
        select.addEventListener('change', () => {
          const v = e.__marView;
          if (!currentDispatch || !v || v.msg == null) return;
          const optionsAttr = v.attrs.find(a => a.name === 'options');
          if (!optionsAttr || !optionsAttr.value || optionsAttr.value.k !== 'L') return;
          const idx = parseInt(select.value, 10);
          if (!Number.isFinite(idx)) return;
          const picked = optionsAttr.value.xs[idx];
          if (picked === undefined) return;
          currentDispatch(apply(v.msg, picked));
        });
        break;
      }
      case 'centered':
        // Pure two-axis alignment: fills whatever space the PARENT
        // provides (flex: 1 in the page's height-propagating column
        // — it never invents a height of its own) and centers the
        // child in it. Full-screen states (Loading, EmptyState, …).
        ensureUIStyles();
        e.className = 'mar-centered';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'spacer':
        // Flex filler — the parent flex container (hstack / vstack
        // / section-body) absorbs it and pushes siblings apart.
        ensureUIStyles();
        e.className = 'mar-spacer';
        break;
      case 'toggle': {
        // <label.mar-toggle>
        //   <span.mar-toggle-label>{text}</span>
        //   <input.mar-toggle-switch type=checkbox>
        // </label>
        //
        // Stable structure so patchDOM can update text + checked
        // state in place without recreating nodes (the checkbox's
        // styled track/thumb survives re-renders that way).
        ensureUIStyles();
        e.className = 'mar-toggle';
        const labelSpan = document.createElement('span');
        labelSpan.className = 'mar-toggle-label';
        labelSpan.textContent = view.text;
        e.appendChild(labelSpan);
        const sw = document.createElement('input');
        sw.type = 'checkbox';
        sw.className = 'mar-toggle-switch';
        sw.checked = toggleIsOn(view);
        // `<input type=checkbox disabled>` natively swallows clicks
        // and change events, so the dispatcher below never fires
        // while disabled. Sync the class on the <label> so CSS can
        // fade the label text + cursor along with the switch.
        applyDisabledAttr(sw, view);
        e.classList.toggle('mar-disabled', isDisabled(view));
        sw.addEventListener('change', () => {
          const v = e.__marView;
          if (!currentDispatch || !v || v.msg == null) return;
          currentDispatch(apply(v.msg, VBool(sw.checked)));
        });
        e.appendChild(sw);
        break;
      }
      case 'sheet': {
        // Sheet structure:
        //   <div.mar-sheet-backdrop[.open]>          ← e itself
        //     <div.mar-sheet-panel>
        //       <div.mar-sheet-handle></div>         ← drag affordance
        //       {sheet content children}
        //     </div>
        //   </div>
        //
        // The wrapper is always in the DOM (stable for diff). CSS
        // toggles via the `.open` class — slide-up + backdrop fade.
        // When `open=false` the wrapper is `display: none` so it
        // doesn't block clicks on the parent page.
        ensureUIStyles();
        e.className = 'mar-sheet-backdrop';
        applySheetOpenState(e, view);
        const panel = document.createElement('div');
        panel.className = 'mar-sheet-panel';
        const handle = document.createElement('div');
        handle.className = 'mar-sheet-handle';
        handle.setAttribute('aria-hidden', 'true');
        panel.appendChild(handle);
        for (const c of view.children) panel.appendChild(createDOM(c));
        e.appendChild(panel);
        attachSheetDismissDispatchers(e);
        break;
      }
      case 'confirmDialog': {
        // Modal confirmation structure:
        //   <div.mar-confirm-backdrop>                  ← e itself
        //     <div.mar-confirm-dialog>
        //       <p.mar-confirm-title>{title}</p>
        //       <div.mar-confirm-actions>
        //         <button.mar-confirm-cancel>Cancel</button>
        //         <button.mar-confirm-confirm[.destructive]>{label}</button>
        //       </div>
        //     </div>
        //   </div>
        //
        // The presence of the view in the tree IS the "is open"
        // state — when the parent's `case` returns `UI.empty`
        // instead, the entire backdrop unmounts and the modal
        // disappears with no extra wiring. Animation in / out is
        // handled by CSS keyframes on the backdrop class.
        //
        // Backdrop click + Escape both dispatch onCancel, matching
        // the iOS .confirmationDialog semantics of "tap outside =
        // cancel". The two buttons dispatch their respective msgs.
        ensureUIStyles();
        e.className = 'mar-confirm-backdrop';
        e.setAttribute('role', 'alertdialog');
        e.setAttribute('aria-modal', 'true');
        const dialog = document.createElement('div');
        dialog.className = 'mar-confirm-dialog';
        const title = document.createElement('p');
        title.className = 'mar-confirm-title';
        title.textContent = confirmDialogAttr(view, 'title');
        dialog.appendChild(title);
        const actions = document.createElement('div');
        actions.className = 'mar-confirm-actions';
        const cancelBtn = document.createElement('button');
        cancelBtn.type = 'button';
        cancelBtn.className = 'mar-confirm-cancel';
        cancelBtn.textContent = 'Cancel';
        const confirmBtn = document.createElement('button');
        confirmBtn.type = 'button';
        confirmBtn.className = 'mar-confirm-confirm';
        if (confirmDialogAttrRaw(view, 'destructive')) {
          confirmBtn.classList.add('destructive');
        }
        confirmBtn.textContent = confirmDialogAttr(view, 'confirmLabel');
        actions.appendChild(cancelBtn);
        actions.appendChild(confirmBtn);
        dialog.appendChild(actions);
        e.appendChild(dialog);
        attachConfirmDialogDispatchers(e);
        break;
      }
      default:
        for (const c of view.children) e.appendChild(createDOM(c));
    }
    // Universal layout pass — every view honors width / height
    // (chars / lines / fill), and stacks additionally honor `align`.
    // Runs after the per-tag case so the classes compose with the
    // tag's own className assignment.
    applyLayoutAttrs(e, view);
    if (view.tag === 'hstack' || view.tag === 'vstack') applyAlignAttr(e, view);
    return e;
  }

  // ---------- Sheet dispatch + history glue ----------
  //
  // A sheet's open/close state is owned by the parent's Model. To make
  // the browser back button close the sheet (matching iOS swipe-down),
  // the framework injects a synthetic history entry when the sheet
  // opens, and pops it when the sheet closes. The `outlet` field on
  // the view is the identifier used to avoid clobbering legitimate
  // history navigation.
  //
  // Tracking which sheets are currently open by outlet name; lets us
  // tell "back button popped our entry" apart from "the user clicked
  // a link". A push/pop pair is balanced when the parent flips
  // open=true→false on its own (e.g. after a successful create).
  const sheetHistory = { open: new Set() };

  function applySheetOpenState(node, view) {
    const open = sheetIsOpen(view);
    // Single class for the open state. Default (no class) = closed.
    // The .mar-sheet-closed class was redundant once display: none was
    // removed; transitions live on the base + .mar-sheet-open selectors.
    node.classList.toggle('mar-sheet-open', open);
    const outlet = sheetOutlet(view);
    syncSheetHistory(outlet, open);
  }

  // Read attrs directly off the view — getAttr() in this file
  // assumes string attrs (returns `.value.s`), which would silently
  // drop a VBool. Mirroring toggleIsOn's direct-lookup pattern.
  function sheetIsOpen(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'open');
    return !!(a && a.value && a.value.b);
  }
  function sheetOutlet(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'outlet');
    return a && a.value && typeof a.value.s === 'string' ? a.value.s : '';
  }

  // ---------- Confirm dialog helpers ----------
  //
  // Attrs come off the view typed (VString/VBool/Msg-shaped), unlike
  // the rest of the codebase that uses getAttr() and silently
  // assumes a string. Each helper reads its expected type and
  // returns a usable JS value with a safe default if anything's
  // missing.

  function confirmDialogAttr(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value && typeof a.value.s === 'string' ? a.value.s : '';
  }
  function confirmDialogAttrRaw(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value ? (a.value.b === true) : false;
  }
  function confirmDialogHandler(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value ? a.value : null;
  }

  // attachConfirmDialogDispatchers wires the three dispatch sources:
  //   - Click on the destructive button → onConfirm
  //   - Click on the cancel button     → onCancel
  //   - Click on the backdrop (not on the dialog itself) → onCancel
  //
  // We bind to the wrapper element (which IS the backdrop) and use
  // event delegation so the listeners survive child re-renders.
  // The escape-key handler is bound to document so it works
  // regardless of focus, but only fires while a confirm dialog is
  // mounted (we check via :scope traversal on each press).
  function attachConfirmDialogDispatchers(backdropEl) {
    if (backdropEl.__marConfirmWired) return;
    backdropEl.__marConfirmWired = true;

    backdropEl.addEventListener('click', (ev) => {
      const v = backdropEl.__marView;
      if (!v || !currentDispatch) return;
      // Find which logical button (if any) was hit.
      let kind = null;
      if (ev.target.classList.contains('mar-confirm-confirm')) {
        kind = 'onConfirm';
      } else if (ev.target.classList.contains('mar-confirm-cancel')) {
        kind = 'onCancel';
      } else if (ev.target === backdropEl) {
        // Bare-backdrop click (didn't hit the dialog) — treat as cancel.
        kind = 'onCancel';
      }
      if (!kind) return;
      const handler = confirmDialogHandler(v, kind);
      if (handler) currentDispatch(handler);
    });
  }

  // Document-level Escape handler — installed once. Walks the
  // currently-mounted DOM for a `.mar-confirm-backdrop` and
  // dispatches its onCancel. Doing this at the document level (vs.
  // on each backdrop) means we don't need focus inside the dialog
  // for Escape to work, which matches iOS / Apple Music behavior
  // (Escape always closes, regardless of where the focus ring
  // happens to be).
  if (typeof document !== 'undefined' && !document.__marConfirmEscWired) {
    document.__marConfirmEscWired = true;
    document.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Escape') return;
      const backdrop = document.querySelector('.mar-confirm-backdrop');
      if (!backdrop) return;
      const v = backdrop.__marView;
      if (!v || !currentDispatch) return;
      const handler = confirmDialogHandler(v, 'onCancel');
      if (handler) currentDispatch(handler);
    });
  }

  // syncSheetHistory mirrors the sheet's open state into the browser
  // history. push when open transitions false→true, back when
  // true→false (so the user-initiated dismiss leaves no orphan entry).
  // popstate handler in attachSheetDismissDispatchers fires the
  // user-back-button path separately.
  function syncSheetHistory(outlet, open) {
    if (!outlet) return;
    const wasOpen = sheetHistory.open.has(outlet);
    if (open && !wasOpen) {
      sheetHistory.open.add(outlet);
      const url = new URL(location.href);
      url.searchParams.set('sheet', outlet);
      history.pushState({ __marSheet: outlet }, '', url.toString());
    } else if (!open && wasOpen) {
      sheetHistory.open.delete(outlet);
      // history.back() to remove our pushed entry. The popstate
      // handler below checks our `closing` flag to avoid re-dispatching
      // onDismiss when WE caused the pop.
      sheetClosingProgrammatically = true;
      history.back();
    }
  }

  let sheetClosingProgrammatically = false;

  // attachSheetDismissDispatchers binds the backdrop click, Escape
  // key, and browser back button to the sheet's onDismiss Msg. Called
  // once per sheet wrapper element; reads node.__marView at event
  // time so the latest onDismiss is dispatched even after re-render.
  function attachSheetDismissDispatchers(node) {
    if (node.__sheetDispatchersBound) return;
    node.__sheetDispatchersBound = true;

    // Backdrop click — only dismiss when the user clicks the wrapper
    // itself, not bubbled from the panel content.
    node.addEventListener('click', (ev) => {
      if (ev.target !== node) return;
      dispatchSheetDismiss(node);
    });

    // Escape key — only when this sheet is open (sheets stack
    // semantically; outermost sheet closes first).
    document.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Escape') return;
      if (!node.__marView || !sheetIsOpen(node.__marView)) return;
      dispatchSheetDismiss(node);
    });

    // Browser back button — popstate fires when the user hits Back.
    // If WE just called history.back() to clean up an outlet, ignore
    // (the parent already closed the sheet via Msg). Otherwise the
    // user wants to close: dispatch onDismiss so the parent flips
    // its open flag.
    window.addEventListener('popstate', () => {
      if (sheetClosingProgrammatically) {
        sheetClosingProgrammatically = false;
        return;
      }
      if (!node.__marView || !sheetIsOpen(node.__marView)) return;
      // Forget the outlet — the URL entry is already gone.
      const outlet = sheetOutlet(node.__marView);
      if (outlet) sheetHistory.open.delete(outlet);
      dispatchSheetDismiss(node);
    });
  }

  function dispatchSheetDismiss(node) {
    const v = node.__marView;
    if (!currentDispatch || !v || v.msg == null) return;
    currentDispatch(v.msg);
  }

  // patchDOM updates `node` (currently rendering oldView, available as
  // node.__marView) so it matches newView, mutating in place where possible
  // and replacing only when the tag changes. Listeners stay attached
  // because they read view.msg from node.__marView.
  function patchDOM(node, newView) {
    const oldView = node.__marView;
    if (!oldView || oldView.tag !== newView.tag) {
      const replacement = createDOM(newView);
      node.parentNode.replaceChild(replacement, node);
      return replacement;
    }
    setMarView(node, newView);
    // Universal layout pass (mirror of createDOM's tail) — re-sync
    // the width / height classes + styles and the stack `align`
    // class on every patch, so dynamically swapped sizing attrs
    // (`if compact then width (chars 12) else width fill`) track the
    // model. Idempotent and cheap (classList.toggle + style writes).
    applyLayoutAttrs(node, newView);
    if (newView.tag === 'hstack' || newView.tag === 'vstack') applyAlignAttr(node, newView);

    switch (newView.tag) {
      case 'text':
      case 'title':
      case 'subtitle':
      case 'errorText':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      case 'image': {
        const newSrc = getAttr(newView, 'src');
        const newAlt = getAttr(newView, 'alt');
        if (node.getAttribute('src') !== newSrc) node.setAttribute('src', newSrc);
        if (node.getAttribute('alt') !== newAlt) node.setAttribute('alt', newAlt);
        // Clear inline sizing/object-fit before re-applying so a model
        // change that drops the size/contentMode attr doesn't leave a
        // stale style behind.
        node.style.width = ''; node.style.height = '';
        node.style.maxWidth = ''; node.style.objectFit = '';
        applyImageAttrs(node, newView);
        break;
      }
      case 'button':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        applyDisabledAttr(node, newView);
        break;
      case 'navigationLink': {
        const newHref = getAttr(newView, 'href');
        if (node.getAttribute('href') !== newHref) node.setAttribute('href', newHref);
        applyAnchorDisabled(node, newView);
        // Patch children in place — same shape as the other
        // container tags (hstack/vstack/section). Lets a focused
        // input nested inside a navigationLink survive a re-render.
        patchChildrenPositional(node, oldView.children || [], newView.children || [], 'navigationLink');
        break;
      }
      case 'textField':
      case 'textArea':
        // Only write if the value diverges — avoids resetting the cursor
        // mid-keystroke when the model just echoes what the user typed.
        // Same patch path for both shapes; the input vs textarea is
        // already pinned by node.tagName from the initial createDOM.
        if (node.value !== newView.text) node.value = newView.text;
        applyDisabledAttr(node, newView);
        // Re-apply sizing on every patch — cheap (idempotent: same
        // attrs produce the same inline style), and necessary when
        // user code dynamically swaps the width/height attr (rare
        // but valid — e.g. `if model.compact then width (chars 12)
        // else width (chars 40)`).
        applySizing(node, newView);
        break;
      case 'datePicker': {
        // Only write if the value diverges, so a re-render doesn't
        // fight a calendar the user just opened. The value comes from
        // the Time attr, formatted as the local YYYY-MM-DD.
        const next = dateInputValue(newView);
        if (node.value !== next) node.value = next;
        applyDisabledAttr(node, newView);
        applySizing(node, newView);
        break;
      }
      case 'picker': {
        // Re-render the option list only when something changed.
        // Comparing the underlying VList / VFn / VValue refs lets
        // the common "same options, only selected differs" path
        // avoid recreating the <option> nodes (which would flicker
        // the dropdown if open). When refs match, just sync the
        // select.value index.
        const select = node.querySelector(':scope > .mar-picker-select');
        if (!select) break;
        applyDisabledAttr(select, newView);
        node.classList.toggle('mar-disabled', isDisabled(newView));
        applySizing(node, newView);
        const oldOptions = oldView.attrs.find(a => a.name === 'options');
        const newOptions = newView.attrs.find(a => a.name === 'options');
        const oldToLabel = oldView.attrs.find(a => a.name === 'toLabel');
        const newToLabel = newView.attrs.find(a => a.name === 'toLabel');
        const optionsChanged =
          !oldOptions || !newOptions ||
          oldOptions.value !== newOptions.value ||
          (oldToLabel && newToLabel && oldToLabel.value !== newToLabel.value);
        if (optionsChanged) {
          renderPickerOptions(select, newView);
        } else {
          const selectedAttr = newView.attrs.find(a => a.name === 'selected');
          const opts = newOptions.value.xs;
          if (selectedAttr && selectedAttr.value) {
            for (let i = 0; i < opts.length; i++) {
              if (eqValues(opts[i], selectedAttr.value)) {
                if (select.value !== String(i)) select.value = String(i);
                break;
              }
            }
          }
        }
        break;
      }
      case 'empty':
        break;
      case 'spacer':
        // No content, no listeners — nothing to update. The CSS
        // class drives the layout entirely.
        break;
      case 'toggle': {
        // Slot 0: <span.mar-toggle-label> — sync text.
        // Slot 1: <input.mar-toggle-switch> — sync checked.
        // Listener on the input reads view.msg from the label via
        // closure, so __marView already updated above is enough.
        const labelSpan = node.querySelector(':scope > .mar-toggle-label');
        const sw = node.querySelector(':scope > .mar-toggle-switch');
        if (labelSpan && labelSpan.textContent !== newView.text) {
          labelSpan.textContent = newView.text;
        }
        if (sw) {
          const next = toggleIsOn(newView);
          if (sw.checked !== next) sw.checked = next;
          applyDisabledAttr(sw, newView);
        }
        node.classList.toggle('mar-disabled', isDisabled(newView));
        break;
      }
      case 'sheet': {
        // Apply new open-state (drives CSS class + history pushState/back
        // sync). Then diff the panel's content children.
        applySheetOpenState(node, newView);
        const panel = node.querySelector(':scope > .mar-sheet-panel');
        if (panel) {
          // Panel's first child is the persistent .mar-sheet-handle —
          // diff starts from index 1.
          const oldChildren = oldView.children || [];
          const newChildren = newView.children || [];
          // Children render at panel[1..], handle is at panel[0].
          const offset = 1;
          for (let i = 0; i < newChildren.length; i++) {
            const child = panel.children[offset + i];
            if (!child) {
              panel.appendChild(createDOM(newChildren[i]));
            } else if (child.__marView && child.__marView.tag === newChildren[i].tag) {
              patchDOM(child, newChildren[i]);
            } else {
              panel.replaceChild(createDOM(newChildren[i]), child);
            }
          }
          // Remove extras.
          while (panel.children.length > offset + newChildren.length) {
            panel.removeChild(panel.lastChild);
          }
        }
        break;
      }
      case 'confirmDialog': {
        // Refresh the title + button label + destructive class. The
        // attrs are stored on the view; we just rewrite the
        // corresponding DOM nodes in place. Listeners stay attached
        // via attachConfirmDialogDispatchers (it's idempotent and
        // bound to the backdrop wrapper, not the inner buttons).
        const titleEl = node.querySelector(':scope > .mar-confirm-dialog > .mar-confirm-title');
        if (titleEl) titleEl.textContent = confirmDialogAttr(newView, 'title');
        const confirmBtn = node.querySelector(':scope > .mar-confirm-dialog > .mar-confirm-actions > .mar-confirm-confirm');
        if (confirmBtn) {
          confirmBtn.textContent = confirmDialogAttr(newView, 'confirmLabel');
          confirmBtn.classList.toggle('destructive', confirmDialogAttrRaw(newView, 'destructive'));
        }
        // No children to diff — the dialog's content is fully
        // attribute-driven. The view's onConfirm / onCancel
        // handlers live on the (newly-stored) view via setMarView;
        // the listener reads them from there at click-time, so they
        // automatically pick up the new dispatchers.
        break;
      }
      // navigationStack / uiSection patch in place: the surrounding
      // chrome (nav bar, section header/footer) gets re-rendered, but
      // the body wrapper stays the same DOM node so any focused input
      // inside keeps focus + cursor + selection.
      case 'navigationStack': {
        // Slots 0+1: pinned toolbar row + large-title header. Cheap
        // to recreate (no interactive focus state on title/buttons
        // that matters for typing). Two slot-specific fixups: kill
        // the old row's title observer so it can't fight the new
        // one, and carry the scrolled state over so the inline
        // title doesn't blink off for a frame on every patch while
        // the page is scrolled (the new observer re-confirms it
        // asynchronously).
        const oldRow = node.children[0];
        const [newRow, newBar] = buildNavigationChrome(newView);
        if (oldRow && oldRow._marNavObserver) oldRow._marNavObserver.disconnect();
        if (oldRow && oldRow.classList.contains('mar-nav-scrolled')) {
          newRow.classList.add('mar-nav-scrolled');
        }
        node.replaceChild(newRow, node.children[0]);
        node.replaceChild(newBar, node.children[1]);
        // Slot 2: body wrapper. Diff its children — that's where
        // form/list/etc live, possibly with a focused input below.
        const body = node.querySelector(':scope > .mar-nav-body');
        if (body) {
          patchChildrenPositional(body, oldView.children, newView.children, 'navigationStack');
        }
        break;
      }
      case 'uiSection':
      case 'uiKeyedList': {
        const headerText = getAttr(newView, 'header');
        const footerText = getAttr(newView, 'footer');
        const h = node.querySelector(':scope > .mar-section-header');
        const body = node.querySelector(':scope > .mar-section-body');
        const f = node.querySelector(':scope > .mar-section-footer');
        if (h) {
          if (h.textContent !== headerText) h.textContent = headerText;
          h.style.display = headerText ? '' : 'none';
        }
        if (f) {
          if (f.textContent !== footerText) f.textContent = footerText;
          f.style.display = footerText ? '' : 'none';
        }
        if (body) {
          // When the editing flag of `onMove` OR `onDelete` flips,
          // the row structure changes (drag handles + delete
          // buttons appear / disappear; listeners get rebound).
          // Rebuild the section body wholesale — toggling edit
          // mode is rare (one tap) and a fresh subtree is simpler
          // than diffing every affordance in/out.
          //
          // We only watch the `.editing` flag on onDelete, not the
          // presence of the attr itself: the delete button is gated
          // entirely on editing=true (see attachRowDeleteAffordance
          // — browse mode shows no destructive control), so the
          // attr can come and go between renders without visual
          // change as long as editing stays false.
          const oldOnMoveS = getAttrRaw(oldView, 'onMove');
          const newOnMoveS = getAttrRaw(newView, 'onMove');
          const oldEdS = !!(oldOnMoveS && oldOnMoveS.editing);
          const newEdS = !!(newOnMoveS && newOnMoveS.editing);
          const oldOnDelS = getAttrRaw(oldView, 'onDelete');
          const newOnDelS = getAttrRaw(newView, 'onDelete');
          const oldDelEdS = !!(oldOnDelS && oldOnDelS.editing);
          const newDelEdS = !!(newOnDelS && newOnDelS.editing);
          if (oldEdS !== newEdS || oldDelEdS !== newDelEdS) {
            const fresh = createDOM(newView);
            node.parentNode.replaceChild(fresh, node);
            break;
          }
          // Stable editing state — keyed reconciliation handles
          // reorders without losing handle wiring (drag listeners
          // are bound to the section-body element, not the rows).
          patchChildrenPositional(body, oldView.children, newView.children, 'uiSection');
          // Defensive sweep: ensure every current row has its
          // edit-mode affordances. patchChildrenKeyed can create
          // new rows via createDOM(rowView) when a new key appears
          // (Add Task, restored row via Undo, tag swap toggle ↔
          // hstack); all of those bypass the section's own
          // createDOM loop where these affordances are normally
          // appended. Without this sweep, those rows would render
          // WITHOUT a delete button or drag handle while siblings
          // have one — the visible "only first row has no delete"
          // symptom users hit after an Undo.
          //
          // Cheap: O(N) querySelector calls, with the inner work
          // (re-appending) skipped via idempotent guards in the
          // helpers themselves. Done unconditionally per row so
          // index-based per-row state (aria-posinset, dataset.idx)
          // stays in sync after reorders / inserts / removes.
          const rows = body.querySelectorAll(':scope > *:not(.mar-drop-indicator):not(.mar-live-region)');
          if (newEdS) {
            for (let i = 0; i < rows.length; i++) {
              ensureRowEditAffordances(rows[i], i, rows.length);
            }
          }
          if (newOnDelS && newOnDelS.handler) {
            for (let i = 0; i < rows.length; i++) {
              attachRowDeleteAffordance(rows[i], i, newOnDelS.handler, newDelEdS);
            }
          }
        }
        break;
      }
      case 'uiList':
        // No reorder semantics on list itself; just diff children.
        patchChildrenPositional(node, oldView.children, newView.children, 'uiList');
        break;
      case 'form':
      case 'hstack':
      case 'vstack':
      default:
        patchChildrenPositional(node, oldView.children, newView.children, newView.tag);
    }
    return node;
  }

  // patchChildrenPositional walks DOM children and VView children in
  // lockstep. Same-tag pairs are patched in place; new entries are
  // appended; removed entries are detached.
  //
  // Dispatches to patchChildrenKeyed when any child carries a `key`
  // attr — preserves identity (focus, animation, scroll, custom
  // state on the DOM node) across reorders. Positional matching is
  // a fast path for the common case (homogeneous static-order
  // lists like form sections); keyed is the right thing for
  // anything that can reorder.
  function patchChildrenPositional(parentNode, oldChildren, newChildren, _parentTag) {
    if (hasAnyKey(oldChildren) || hasAnyKey(newChildren)) {
      patchChildrenKeyed(parentNode, oldChildren, newChildren);
      return;
    }
    const domChildren = parentNode.childNodes;
    const max = Math.max(oldChildren.length, newChildren.length);
    for (let i = 0; i < max; i++) {
      const newChild = newChildren[i];
      const domChild = domChildren[i];
      if (!newChild) {
        // Excess DOM nodes — remove the rest.
        while (parentNode.childNodes.length > newChildren.length) {
          parentNode.removeChild(parentNode.lastChild);
        }
        break;
      }
      if (!domChild) {
        // New child — append.
        parentNode.appendChild(createDOM(newChild));
        continue;
      }
      // Existing — patch in place.
      patchDOM(domChild, newChild);
    }
  }

  // hasAnyKey returns true if any view in the list declares a
  // `key` attr. Used by patchChildrenPositional to decide whether
  // to switch to keyed mode.
  function hasAnyKey(children) {
    for (const c of children) if (viewKey(c)) return true;
    return false;
  }

  // viewKey extracts the `key` attr value (or null) from a view.
  // Lives next to hasAnyKey so call sites pull identity in one
  // place — if we later move keys off the attrs list onto a
  // dedicated field of VView, only this one helper changes.
  function viewKey(view) {
    if (!view || !view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name === 'key' && a.value && a.value.k === 'S') {
        return a.value.s;
      }
    }
    return null;
  }

  // patchChildrenKeyed reorders DOM children to match newChildren by
  // matching `key` attrs across renders. Steps:
  //
  //   1. Build a map from old key → existing DOM node.
  //   2. Walk newChildren in order. For each:
  //      - If the key exists in the map: reuse that DOM node,
  //        moving it via insertBefore to its new position, then
  //        patch its content in place. Mark it as "consumed".
  //      - If no key match (new entry): create a fresh DOM node.
  //      - If a new child has no key, fall back to creating fresh.
  //        (Mixing keyed and unkeyed siblings is a smell; we don't
  //        attempt clever matching — the unkeyed ones are treated
  //        as always-new.)
  //   3. After the walk, remove any old DOM nodes whose key wasn't
  //      consumed by the new list (i.e., the item was removed).
  //
  // The result: rows that moved keep their DOM node (and with it
  // focus, scroll, animation state), rows that vanished are removed,
  // rows that appeared are created.
  function patchChildrenKeyed(parentNode, oldChildren, newChildren) {
    const domChildren = Array.from(parentNode.childNodes);

    // Step 1: index old DOM nodes by key.
    const oldByKey = new Map();
    for (let i = 0; i < oldChildren.length; i++) {
      const k = viewKey(oldChildren[i]);
      if (k != null && domChildren[i]) {
        oldByKey.set(k, { node: domChildren[i], view: oldChildren[i] });
      }
    }

    // Step 2: walk newChildren in order, reordering DOM as we go.
    // `cursor` tracks the position where the next reused/created
    // node should land. We use insertBefore(node, anchor) where
    // anchor is the node currently at `cursor` — that's the only
    // primitive that handles "move to position N" correctly even
    // when the node is already somewhere else in the parent.
    const consumed = new Set();
    for (let i = 0; i < newChildren.length; i++) {
      const newChild = newChildren[i];
      const newK = viewKey(newChild);
      const anchor = parentNode.childNodes[i] || null;
      if (newK != null && oldByKey.has(newK)) {
        // Reuse: move the existing node to position i.
        const { node, view: oldView } = oldByKey.get(newK);
        consumed.add(newK);
        if (node !== anchor) {
          parentNode.insertBefore(node, anchor);
        }
        // Patch content in place (handles attr / text / children
        // changes; preserves the DOM identity we just moved).
        patchDOM(node, newChild);
      } else {
        // New entry — create fresh and insert at position i.
        const node = createDOM(newChild);
        parentNode.insertBefore(node, anchor);
      }
    }

    // Step 3: remove any old keyed nodes that weren't consumed.
    // Also remove any extra positional (unkeyed) leftovers past the
    // new length.
    for (const [k, entry] of oldByKey) {
      if (!consumed.has(k) && entry.node.parentNode === parentNode) {
        parentNode.removeChild(entry.node);
      }
    }
    while (parentNode.childNodes.length > newChildren.length) {
      parentNode.removeChild(parentNode.lastChild);
    }
  }

  // ---------- MVU loop ----------

  function unwrapModelTuple(v) {
    if (v && v.k === 'T' && v.xs.length === 2) {
      return { model: v.xs[0], effect: v.xs[1] };
    }
    return { model: v, effect: VEffect(() => VUnit(), 'none') };
  }

  function runEffect(eff) {
    if (eff && eff.k === 'E' && typeof eff.run === 'function') {
      try { eff.run(); } catch (e) { console.error('effect failed:', e); }
    }
  }

  // Listener bookkeeping that survives across mountPages calls.
  //
  // `mar dev`\'s hot reload re-invokes mountPages on every save
  // (see marReload → marRun). Without explicitly removing the
  // previous listeners, each call would stack ANOTHER popstate
  // listener on `window` and ANOTHER click delegator on
  // `#mar-root`. After N reloads, every `history.back()` would
  // fire N+1 render() closures in parallel, each tied to a stale
  // mountPages scope, racing to startViewTransition (only one is
  // allowed in flight — the rest reject silently). The visible
  // symptom is the back button needing several presses to
  // actually advance the URL.
  //
  // We track the most recent listener references at IIFE scope
  // and remove them on each new mountPages, so there\'s always
  // exactly one active listener of each kind.
  let prevPopstateHandler = null;
  let prevLinkClickHandler = null;
  let prevLinkClickRoot = null;

  // mountPages mounts a list of pages with URL-based routing. A
  // single-page app is just a list of one page (path "/"). Each page
  // has its own model — selected by window.location.pathname.
  // Falls back to the first page when the URL doesn't match any path.
  // iOS Safari needs a no-op touch listener on `document` to enter
  // its "delegated event delivery" mode. WITHOUT this, after a
  // long-touch gesture (like our drag reorder), Safari leaves the
  // page in a state where the next tap on any button is consumed
  // by gesture-state cleanup instead of firing the button's click
  // handler — operator must tap twice. The fix is documented voodoo
  // (the same trick FastClick used in the late 2010s, the same
  // reason jQuery Mobile shipped an empty body click handler):
  // adding a passive listener to `document` flips Safari onto a
  // code path that delivers events immediately after touchend
  // instead of waiting for the gesture state machine.
  //
  // Discovered empirically: when the operator pasted a console
  // snippet that attached debug listeners to `document`, the
  // double-tap bug vanished immediately. Reload (without the
  // snippet) brought the bug back. That's the signature of this
  // exact iOS Safari quirk.
  //
  // Installed once per page load (the iife wrapping the runtime
  // runs once per page). Hot-reload doesn't need to re-install
  // because the previous listener stays attached; even if a
  // second one were added, both being no-ops doesn't matter.
  if (typeof document !== 'undefined' && !document.__marTouchPrimer) {
    document.addEventListener('touchstart', function () {}, { passive: true });
    document.__marTouchPrimer = true;
  }

  function mountPages(pageList) {
    // A fresh mount (cold load or hot reload) owns the live timers; clear any
    // the previous mountPages left running so they do not double-fire.
    teardownAllSubs();
    const pages = {};
    let firstPath = null;

    // Cached Auth.me result for protected pages. null = not fetched
    // yet; VCtor('Just', [user]) | VCtor('Nothing', []) once resolved.
    // We refetch on demand (see ensureUser) and invalidate on logout.
    let authUser = null;
    let authPending = null;

    function fetchAuthMe() {
      if (authPending) return authPending;
      authPending = fetch('/_auth/whoami', { credentials: 'same-origin' })
        .then(r => r.ok ? r.text() : Promise.reject('HTTP ' + r.status))
        .then(t => {
          const raw = t ? JSON.parse(t) : null;
          authUser = raw == null
            ? VCtor('Nothing', [])
            : VCtor('Just', [jsToMar(raw)]);
          return authUser;
        })
        .catch(() => {
          authUser = VCtor('Nothing', []);
          return authUser;
        })
        .finally(() => { authPending = null; });
      return authPending;
    }

    // Path-pattern helpers (parsePathPattern, matchPathPattern,
    // buildPathURL) live at the IIFE level so they're reachable from
    // both makeBuiltinEnv (linkTo / Nav.pushTo) and mountPages.

    function buildPageEntry(path, initFn, updateFn, viewFn, title, isProtected, isDynamic, subsFn) {
      // For protected/dynamic pages init/update/view need extra args
      // threaded in (User, Params, or both). We can't init until those
      // are known, so we defer.
      const models = {};         // preservation key → model
      const initEffects = {};    // preservation key → first-render effect
      const initializedKeys = {}; // preservation key → bool

      // Apply User and Params in the right order (User first, then
      // Params — matches the type signatures in env.go). Either may
      // be null/undefined when irrelevant.
      const applyExtras = (fn, user, params) => {
        let f = fn;
        if (isProtected) f = apply(f, user);
        if (isDynamic)   f = apply(f, params);
        return f;
      };

      const initWith = (user, params, key) => {
        if (initializedKeys[key]) return;
        initializedKeys[key] = true;
        const initFnApplied = applyExtras(initFn, user, params);
        const prior = preservedScreenModels[key];
        if (prior !== undefined) {
          const viewFnApplied = applyExtras(viewFn, user, params);
          try {
            apply(viewFnApplied, prior);
            models[key] = prior;
            return;
          } catch (_) {
            if (typeof console !== 'undefined') {
              console.info('[mar reload] page ' + path + ' model shape changed, fresh init');
            }
          }
        }
        // initFnApplied IS the (model, effect) tuple by now: plain
        // pages declare init as the tuple value itself, and the
        // protected/dynamic flavors became it once applyExtras fed
        // them User/Params. No vestigial unit argument.
        const initial = unwrapModelTuple(initFnApplied);
        models[key] = initial.model;
        initEffects[key] = initial.effect;
        preservedScreenModels[key] = initial.model;
      };

      const entry = {
        path,
        title,
        isProtected,
        isDynamic,
        // Per-key state. activeKey is set by currentPage() before
        // render touches model / params — keeping these as getters
        // means the rest of the runtime stays oblivious to the
        // single-vs-many model split.
        activeKey: path,
        params: null,
        get model()       { return models[entry.activeKey]; },
        set model(v) {
          models[entry.activeKey] = v;
          preservedScreenModels[entry.activeKey] = v;
        },
        get initEffect()  { return initEffects[entry.activeKey] || null; },
        set initEffect(v) { initEffects[entry.activeKey] = v; },
        get initialized() { return !!initializedKeys[entry.activeKey]; },
        clearInitialized: (key) => { initializedKeys[key] = false; },
        initWith: (user, params) => initWith(user, params, entry.activeKey),
        init: initFn,
        update: updateFn,
        view: viewFn,
        subscriptions: subsFn,
      };

      if (!isProtected && !isDynamic) {
        // Static public pages can init eagerly — no auth dependency,
        // no URL-derived params.
        initWith(null, null, path);
      }

      return entry;
    }

    // Static pages live in the `pages` map (literal path → entry).
    // Dynamic pages live in `dynamicPages` as an ordered list of
    // {pattern, page} pairs. Resolution order: static first (so a
    // literal '/notes/new' beats the pattern '/notes/:id'), then
    // dynamic in declaration order.
    const dynamicPages = [];

    for (const p of pageList) {
      if (p.k !== 'C') continue;
      if (p.tag !== '__Page' && p.tag !== '__ProtectedPage' &&
          p.tag !== '__DynamicPage' && p.tag !== '__DynamicProtectedPage') continue;
      // These ctors share the same arg shape: [path, init, update, view,
      // title]. __ProtectedPage / __DynamicProtectedPage trigger the auth
      // gate (redirect resolved from globalThis.__marAuthSignInPath, set by
      // the Auth.config builtin). __DynamicPage / __DynamicProtectedPage also
      // enable URL-pattern matching with `:param` segments. (Page.adminProtected
      // pre-applies its AdminSession and emits a plain __Page — see its builtin.)
      const [pathV, initFn, updateFn, viewFn, titleV, subsFn] = p.args;
      const path = pathV.s;
      const title = (titleV && titleV.k === 'S') ? titleV.s : '';
      const isProtected = (p.tag === '__ProtectedPage' || p.tag === '__DynamicProtectedPage');
      const isDynamic   = (p.tag === '__DynamicPage'   || p.tag === '__DynamicProtectedPage');
      if (firstPath === null) firstPath = path;
      const entry = buildPageEntry(path, initFn, updateFn, viewFn, title, isProtected, isDynamic, subsFn);
      if (isDynamic) {
        dynamicPages.push({ pattern: parsePathPattern(path), page: entry });
      } else {
        pages[path] = entry;
      }
    }

    let initEffectsRun = {};

    // Resolve the URL to a page entry. Side effect: stamps
    // entry.activeKey + entry.params so the rest of the runtime sees
    // a per-URL model slot (dynamic pages) and the right Params record.
    function currentPage() {
      const urlPath = window.location.pathname;
      // 1) literal match
      if (pages[urlPath]) {
        const pg = pages[urlPath];
        pg.activeKey = pg.path;
        pg.params = null;
        return pg;
      }
      // 2) dynamic-pattern match, in declaration order
      for (const dp of dynamicPages) {
        const params = matchPathPattern(urlPath, dp.pattern);
        if (params !== null) {
          // Dynamic pages key model state by URL, not by path: navigating
          // /notes/abc → /notes/xyz is a fresh model slot, not a re-render.
          // (Static pages key by path — see the literal match above.)
          dp.page.activeKey = urlPath;
          dp.page.params = params;
          return dp.page;
        }
      }
      // 3) fallback to the first declared page (single-page apps with
      //    a stale URL, dev hot-reload from a deep link, etc.).
      const fallback = pages[firstPath]
        || (dynamicPages[0] && dynamicPages[0].page)
        || null;
      if (fallback) {
        fallback.activeKey = fallback.path;
        fallback.params = null;
      }
      return fallback;
    }

    let mounted = null;
    let mountedPath = null;
    let routerInstalled = false;
    // Tracks the marDepth at the last DOM-swap render. The next swap
    // compares newDepth vs this to pick the view-transition direction
    // (forward / back / none).
    //
    // `firstRender` short-circuits the direction computation on the
    // very first render of this mountPages call: there is no previous
    // depth to compare against. Without this, a cold load on a deep
    // sub-page (e.g. /projects/42, marDepth=2, restored from BFCache
    // or browser session) would compute newDepth(2) > lastSeen(0) →
    // 'forward' and play a slide-in-from-right on top of the initial
    // mount. Same trap on hot-reload (the IIFE re-runs with a fresh
    // lastSeenNavDepth while history.state preserves the real depth).
    let lastSeenNavDepth = 0;
    let firstRender = true;

    // Returns the User value for a protected page, or null if we
    // either don't have an answer yet or the user isn't logged in.
    // `authUser` (mountPages-scoped) is VCtor('Just', [user]) | VCtor('Nothing', []) | null.
    function getUser() {
      if (!authUser || authUser.k !== 'C') return null;
      if (authUser.tag === 'Just' && authUser.args.length > 0) return authUser.args[0];
      return null;
    }

    // For protected/dynamic pages init/update/view take extras as the
    // leading args (User first, then Params, mirroring the type sigs
    // in env.go). These helpers thread them in transparently so the
    // rest of the runtime (render, dispatch, time-travel) can stay
    // agnostic to the four page flavors.
    function applyExtras(pg, fn) {
      let f = fn;
      if (pg.isProtected) f = apply(f, getUser());
      if (pg.isDynamic)   f = apply(f, pg.params);
      return f;
    }
    function viewWithUser(pg, model) {
      return apply(applyExtras(pg, pg.view), model);
    }
    function updateWithUser(pg, msg, model) {
      return apply(apply(applyExtras(pg, pg.update), msg), model);
    }

    // When the user agent already animated this traversal itself
    // (Safari macOS two-finger swipe-back is the canonical case),
    // we skip our own startViewTransition for the next render so
    // animations don't stack. The flag is set by the popstate
    // handler below — it reads PopStateEvent.hasUAVisualTransition,
    // a standard property browsers expose during traversal-driven
    // visual transitions. Reset to false after a single render.
    let skipNextViewTransition = false;

    // Tracks WHICH nav verb triggered the upcoming render so we can
    // pick a direction-correct animation. Depth alone isn't enough:
    //
    //   - Nav.replace    → same depth, but it's a context change
    //     (logout, switching modes); use 'fade' (no direction).
    //   - Nav.replaceFresh → depth resets to 0; semantically it's
    //     ALSO a context change (sign-in completed, redirected
    //     after auth-expired), not a "go back" — use 'fade'.
    //   - Nav.push       → depth grows; use 'forward' slide.
    //   - browser back   → depth shrinks; use 'back' slide.
    //
    // The depth heuristic alone would render replaceFresh as 'back'
    // (because newDepth < lastSeen), which implies "going back into
    // history" — visually wrong since the previous flow is gone.
    // Set just before render() by the nav primitives below; read
    // and cleared inside render().
    let pendingNavKind = null;

    function render() {
      const pg = currentPage();
      if (!pg) return;

      // Page.protected gating: ensure we know who the user is before
      // mounting. If unauthed, navigate to the sign-in path declared
      // in Auth.config.signInPage. If we don't have an answer yet,
      // fire Auth.me and bail — the .then handler will re-call
      // render() once authUser is populated.
      if (pg.isProtected) {
        if (authUser == null) {
          fetchAuthMe().then(() => render());
          return;
        }
        const user = getUser();
        if (user == null) {
          const signInPath = globalThis.__marAuthSignInPath;
          if (!signInPath) {
            console.error('[mar] Page.protected used but Auth.config has no `signInPage`. ' +
              'Add `signInPage = Frontend.SignIn.page` to Auth.config so unauthed users have somewhere to go.');
            return;
          }
          // Replace history (no back-button to a page the user can't
          // see) and route to the sign-in page. Marked as 'replace'
          // so the cross-fade animation fires — the protected page
          // never visibly mounted, so a slide would be misleading.
          pendingNavKind = 'replace';
          replaceNav(signInPath);
          render();
          return;
        }
        if (!pg.initialized) {
          pg.initWith(user, pg.params);
        }
      } else if (pg.isDynamic) {
        // Public dynamic pages still need a deferred init: each URL is
        // its own model slot so the first time we see /notes/:id with a
        // new id, we have to run init with that Params record.
        if (!pg.initialized) {
          pg.initWith(null, pg.params);
        }
      }

      if (!initEffectsRun[pg.activeKey] && pg.initEffect !== null) {
        initEffectsRun[pg.activeKey] = true;
        runEffect(pg.initEffect);
      }
      // Per-page browser-tab title. Empty title (the default when the
      // user omits `title` from Page.create) leaves whatever the host
      // HTML had alone — useful in cases where a non-mar wrapper sets
      // a richer title.
      if (pg.title && document.title !== pg.title) {
        document.title = pg.title;
      }
      // Reconcile subscriptions against the current model: this runs on every
      // render (post-update, post-init, and on navigation), so leaving a page
      // stops its timers and the new page's subscriptions start.
      reconcileSubs(pg.subscriptions ? apply(applyExtras(pg, pg.subscriptions), pg.model) : null);
      const viewVal = viewWithUser(pg, pg.model);
      const root = document.getElementById('mar-root');
      // On page change, throw away the old DOM — diffing across a
      // navigation gives no useful work. Use activeKey (URL-based for
      // dynamic pages) so /notes/abc → /notes/xyz also resets the DOM.
      if (mounted == null || mountedPath !== pg.activeKey) {
        // Compute the slide direction. Three signals feed into it,
        // in priority order:
        //
        //   1. firstRender — cold load / reload / direct URL: skip
        //      animation entirely. There's no "previous" to leave.
        //
        //   2. pendingNavKind — explicit verb just called by the
        //      app code. `replace` and `replaceFresh` map to 'fade'
        //      (cross-fade with subtle scale): the destination is a
        //      context change, not a stack movement, so a slide
        //      would imply a back/forward semantic that isn't there.
        //      `push` falls through to the depth heuristic so the
        //      direction still matches the depth delta.
        //
        //   3. depth delta — popstate (browser back/forward) and
        //      any other untagged path. Forward (push) → slide in
        //      from the right + old parallax left. Back (pop) →
        //      reversed. Same depth → no animation.
        //
        // The cross-fade is iOS-modern: similar to what Photos,
        // Apple Music, and Sign in with Apple use when transitioning
        // between non-hierarchical contexts. Reads as "you stepped
        // into a new place" without implying a stack direction.
        const newDepth = currentNavDepth();
        let dir;
        if (firstRender) {
          dir = 'none';
        } else if (pendingNavKind === 'replace' || pendingNavKind === 'replaceFresh') {
          dir = 'fade';
        } else if (newDepth > lastSeenNavDepth) {
          dir = 'forward';
        } else if (newDepth < lastSeenNavDepth) {
          dir = 'back';
        } else {
          dir = 'none';
        }
        firstRender = false;
        lastSeenNavDepth = newDepth;
        pendingNavKind = null;
        // Scroll-to-top policy. Native iOS NavigationStack pushes
        // always land the new screen at the top; modern web SPAs
        // mirror that. The three cases:
        //
        //   - 'forward' / 'fade': fresh destination, user wants
        //     to start reading from the top. Reset scroll.
        //   - 'back': the user is RETURNING to a page they were
        //     reading. Browser's history.scrollRestoration =
        //     'auto' (the default) restores the scroll position
        //     from when they left, so we DON'T touch it.
        //   - 'none': first render / same depth refresh. Already
        //     at the top or intentionally not navigating, so no
        //     reset.
        const shouldScrollTop = (dir === 'forward' || dir === 'fade');
        const swap = () => {
          while (root.firstChild) root.removeChild(root.firstChild);
          // Recompute from the CURRENT model, not the `viewVal` snapshot
          // captured at render() entry. startViewTransition runs this
          // callback ASYNCHRONOUSLY (next frame), so a fast init-effect
          // fetch can resolve and advance pg.model BEFORE the swap fires.
          // Mounting the stale snapshot would leave the old "Loading…"
          // view on screen even though the data already arrived — the
          // bug where a freshly-navigated page (e.g. a protected page's
          // dashboard right after sign-in) stuck on "Loading…" until
          // time-travel forced a fresh mount.
          mounted = createDOM(viewWithUser(pg, pg.model));
          root.appendChild(mounted);
          mountedPath = pg.activeKey;
          if (shouldScrollTop) {
            // `auto` (not 'smooth') because the page itself is
            // animating in via View Transitions / cross-fade —
            // adding a second animated scroll on top reads as
            // jittery. Instant reset is correct here.
            window.scrollTo({ top: 0, left: 0, behavior: 'auto' });
          }
        };
        if (dir === 'none'
            || !document.startViewTransition
            || prefersReducedMotion()
            || skipNextViewTransition) {
          // Reset the flag — only the immediately-following render
          // should skip; subsequent button-back clicks need their
          // own transition.
          skipNextViewTransition = false;
          swap();
        } else {
          // The data-attribute drives the CSS that picks the right
          // keyframes (forward vs back). We clear it on the
          // transition's `finished` so a subsequent same-depth
          // render doesn't accidentally inherit the previous run's
          // direction.
          document.documentElement.dataset.marNavDir = dir;
          const t = document.startViewTransition(swap);
          t.finished
            .catch(() => { /* user-cancelled / skipped — ignore */ })
            .finally(() => {
              delete document.documentElement.dataset.marNavDir;
            });
        }
      } else {
        // Same page, just an MVU re-render (model changed; URL didn't).
        // Patch in place — no view-transition, no animation. Clear
        // pendingNavKind even though we didn't consume it for direction
        // selection above (the swap branch did), so a stale flag from
        // an early-exit render (e.g. authUser fetch race) doesn't leak
        // into the NEXT swap.
        mounted = patchDOM(mounted, viewVal);
        pendingNavKind = null;
      }

      // Intercept link clicks pointing at known page paths so navigation
      // stays in-process (no full page reload). One delegated listener on
      // the root catches clicks from any descendant anchor.
      //
      // Hot reload re-runs mountPages which re-runs this block. We
      // remove the previous mount\'s listener before installing a
      // new one (see prevLinkClickHandler comment above) so the
      // root doesn\'t accumulate stale closures that reference
      // dead `pages` / `pushNav` / `render` from old mounts.
      if (!routerInstalled) {
        routerInstalled = true;
        if (prevLinkClickHandler && prevLinkClickRoot) {
          prevLinkClickRoot.removeEventListener('click', prevLinkClickHandler);
        }
        const onLinkClick = (ev) => {
          const a = ev.target.closest && ev.target.closest('a[href^="/"]');
          if (!a) return;
          // Disabled navigationLink: swallow the click so the
          // browser doesn't follow the href (full reload) and
          // skip the SPA push. Mirrors how disabled <button>
          // suppresses dispatch.
          const lv = a.__marView;
          if (lv && isDisabled(lv)) {
            ev.preventDefault();
            return;
          }
          const href = a.getAttribute('href');
          if (matchesAnyPage(href)) {
            ev.preventDefault();
            pushNav(href);
            render();
          }
        };
        root.addEventListener('click', onLinkClick);
        prevLinkClickHandler = onLinkClick;
        prevLinkClickRoot = root;
      }
    }

    // True when `href` corresponds to a known static page or matches a
    // dynamic pattern. Used by the link interceptor so /notes/abc gets
    // SPA-routed instead of full-reloading.
    function matchesAnyPage(href) {
      if (pages[href]) return true;
      for (const dp of dynamicPages) {
        if (matchPathPattern(href, dp.pattern) !== null) return true;
      }
      return false;
    }

    // pushNav / replaceNav wrap history.pushState / replaceState
    // with the bookkeeping the nav chrome reads back later:
    //
    //   marDepth   — depth counter for the auto back button
    //                (buildNavigationChrome reads currentNavDepth())
    //                and the view-transition direction detector
    //                (render() compares old vs new depth).
    //   prevTitle  — title of the page we\'re LEAVING, captured
    //                from document.title at push time. The back
    //                button on the next page reads this to render
    //                "‹ <where back goes>" instead of a generic
    //                "‹ Back". Mirrors iOS Settings, where the
    //                back chevron is labeled with the previous
    //                screen\'s name.
    //
    // pushNav bumps depth and stamps the leaving page\'s title.
    // replaceNav preserves both (the user didn\'t actually
    // navigate, just changed the URL of the current entry, so
    // the back target stays the same).
    function pushNav(path) {
      const leavingTitle = document.title || '';
      history.pushState(
        { marDepth: currentNavDepth() + 1, prevTitle: leavingTitle },
        '',
        path,
      );
    }
    function replaceNav(path) {
      const prev = (history.state && history.state.prevTitle) || '';
      history.replaceState(
        { marDepth: currentNavDepth(), prevTitle: prev },
        '',
        path,
      );
    }

    // Expose programmatic navigation so Nav.push / Nav.replace can
    // reach into this closure. Last call wins — successive mountPages
    // (e.g. hot-reload) replace the table.
    globalThis.__marNav = {
      push: (path) => {
        pendingNavKind = 'push';
        pushNav(path);
        render();
      },
      replace: (path) => {
        pendingNavKind = 'replace';
        replaceNav(path);
        // Auth state may have changed (login/logout drives most replace
        // calls). Invalidate the cache so the next protected page
        // re-fetches /_auth/whoami.
        authUser = null;
        render();
      },
      // replaceFresh: like replace, but also resets marDepth to 0 so
      // the destination has no back-button into the (now-completed)
      // flow that brought us here. Used by Auth.completeSignIn and by the
      // auth-expired redirect — both are entry-point transitions
      // where "going back" makes no sense (you'd land on a page you
      // either can't access yet, or that you just finished with).
      replaceFresh: (path) => {
        pendingNavKind = 'replaceFresh';
        history.replaceState({ marDepth: 0, prevTitle: '' }, '', path);
        authUser = null;
        render();
      },
    };

    // ---------- Time-travel ----------
    //
    // Whole section is gated behind __MAR_DEV__ so esbuild drops it from
    // production. The frame capture in `currentDispatch` (below) and the
    // dock panel registration are gated separately.
    //
    // Hoisted outside the `if (__MAR_DEV__)` block so currentDispatch
    // (defined further down, also inside an `if (__MAR_DEV__)` guard)
    // can see it. In production preservedTimeTravel is null and
    // pushTimeTravelState stays a no-op; the dev guards downstream
    // short-circuit before touching anything.
    const timeTravel = preservedTimeTravel;
    let pushTimeTravelState = () => {};

    if (__MAR_DEV__) {
    // Each dispatch captures a frame { msg, prevModel, nextModel, hadEffect,
    // pagePath, time } so the time-travel panel can scrub through history.
    // The panel reads `timeTravel.frames` and `timeTravel.cursor`; jumping
    // sets the page's model directly and re-renders. Live dispatch resets
    // travel mode and (if we're not at the end of history) branches —
    // discarding frames after the cursor so the user's new action becomes
    // the new "present".
    //
    // After a reload we validate every frame's nextModel against the
    // (possibly updated) view function for its page; any frame that the
    // new view rejects gets dropped and the cursor is clamped to
    // whatever's left. If nothing survives, we start fresh.
    // Frame paths can be either literal page paths (static pages) or
    // URLs that match a dynamic pattern. This helper unifies the lookup
    // so time-travel works for both flavors.
    function lookupPageForFrame(framePath) {
      if (pages[framePath]) return pages[framePath];
      for (const dp of dynamicPages) {
        if (matchPathPattern(framePath, dp.pattern) !== null) {
          return dp.page;
        }
      }
      return null;
    }

    {
      const validFrames = [];
      for (const frame of timeTravel.frames) {
        const pg = lookupPageForFrame(frame.pagePath);
        if (!pg) continue;
        // Protected pages can't be validated until we have a User —
        // skip them on cold start, they'll be re-validated naturally
        // on the next dispatch after Auth.me resolves.
        if (pg.isProtected && getUser() == null) {
          validFrames.push(frame);
          continue;
        }
        // Dynamic pages thread Params through the view, but the
        // params record only exists for the URL we're currently on —
        // a stored frame from another URL would crash view validation
        // with a wrong-shape Params. Pass the frame through unchanged;
        // when the user navigates to that URL, render() rebuilds it
        // naturally from preservedScreenModels.
        if (pg.isDynamic) {
          validFrames.push(frame);
          continue;
        }
        try {
          viewWithUser(pg, frame.nextModel);
          // The prevModel must also still be renderable, otherwise
          // jumping back through this frame would crash render().
          viewWithUser(pg, frame.prevModel);
          validFrames.push(frame);
        } catch (_) {
          // Shape mismatch — drop this and any later frames; history
          // truncates at the first incompatibility so the timeline
          // stays a contiguous chain.
          break;
        }
      }
      timeTravel.frames = validFrames;
      if (timeTravel.cursor >= validFrames.length) {
        timeTravel.cursor = validFrames.length - 1;
      }
      // Re-derive traveling: true iff cursor isn't at the latest frame.
      // If the user was time-traveling at reload time, they stay there —
      // preservedScreenModels has the model they had jumped to, so
      // render() shows it consistently with the cursor.
      timeTravel.traveling = timeTravel.cursor !== timeTravel.frames.length - 1;
    }

    function jumpToFrame(targetIdx, opts) {
      if (targetIdx < -1 || targetIdx >= timeTravel.frames.length) return;
      timeTravel.cursor = targetIdx;
      timeTravel.traveling = targetIdx !== timeTravel.frames.length - 1;
      // Pick the model corresponding to "after the frame at targetIdx".
      // For -1 (before any frame), use the prevModel of frame 0 (the
      // initial model). For 0..N-1, use frame[i].nextModel.
      let model;
      let pathToUse;
      if (timeTravel.frames.length === 0) return;
      if (targetIdx === -1) {
        model = timeTravel.frames[0].prevModel;
        pathToUse = timeTravel.frames[0].pagePath;
      } else {
        model = timeTravel.frames[targetIdx].nextModel;
        pathToUse = timeTravel.frames[targetIdx].pagePath;
      }
      const pg = lookupPageForFrame(pathToUse);
      if (!pg) return;
      // Stamp activeKey so the entry's model setter writes to the
      // right slot — for dynamic pages, that's the frame's URL, not
      // the pattern path.
      pg.activeKey = pathToUse;
      pg.model = model;
      // Stay on whatever page we're currently viewing — don't auto-
      // navigate. If the frame was on a different page, current view
      // simply doesn't reflect that frame's change visibly.
      render();
      // skipPanelRefresh keeps the dock panel intact during slider drags.
      // Re-rendering the panel rebuilds the <input>, which kills the
      // mouse drag mid-stream — without this guard the user can only
      // step one frame per click instead of scrubbing freely.
      if (!opts || !opts.skipPanelRefresh) {
        pushTimeTravelState();
      }
    }

    pushTimeTravelState = function () {
      const dock = getDevDock();
      dock.updatePanel('time-travel', {
        frames: timeTravel.frames,
        cursor: timeTravel.cursor,
        traveling: timeTravel.traveling,
      });
    };

    // resetTimeTravel re-runs the current page's init function (getting
    // a fresh (model, effect) pair) and wipes the frame history. The
    // resulting init effect is fired so any startup work — HTTP fetch,
    // initial timer, etc. — re-triggers as if the page was just loaded.
    // Multi-page apps: only the current page is reset; siblings keep
    // whatever model they last had.
    function resetTimeTravel() {
      const pg = currentPage();
      if (!pg || !pg.init) return;
      // For protected pages, init takes the User as first arg. Skip
      // reset when we don't have one (the page itself wouldn't render
      // anyway — render() routes to redirect).
      if (pg.isProtected && getUser() == null) return;
      // Apply User then Params (if present), matching the init type
      // signature. applyExtras handles the four flavors uniformly.
      const initFn = applyExtras(pg, pg.init);
      const fresh = unwrapModelTuple(apply(initFn, VUnit()));
      pg.model = fresh.model;
      // Allow the init effect to fire again on the next render path —
      // mountPages gates it via initEffectsRun (keyed on activeKey so
      // dynamic pages reset cleanly per-URL), which we toggle off here.
      initEffectsRun[pg.activeKey] = false;
      pg.initEffect = fresh.effect;
      timeTravel.frames = [];
      timeTravel.cursor = -1;
      timeTravel.traveling = false;
      render(); // render() consumes initEffect (runs it) on the gated branch
      pushTimeTravelState();
    }

    // Dev-only: time-travel panel + frame capture in dispatch. In
    // production (devMode = false on the program JSON) the dock isn't
    // even instantiated; mountPages just runs.
    if (__MAR_DEV__ && global.__marDevMode) {
      const dock = getDevDock();
      dock.registerPanel({
        id: 'time-travel',
        badge: {
          icon: '⏱',
          color: '#93c5fd',
          title: 'Time travel',
          label: (s) => (s.frames || []).length + ' action' + ((s.frames || []).length === 1 ? '' : 's'),
          visible: (s) => (s.frames || []).length > 0,
          pulse: (s) => !!s.traveling,
        },
        render: renderTimeTravelPanel,
        // Seed with whatever survived hot-reload — registerPanel replaces
        // the panel definition (and state), so without this we'd zero the
        // history visually even though `timeTravel` (the closure variable)
        // still holds the preserved frames.
        initialState: {
          frames: timeTravel.frames,
          cursor: timeTravel.cursor,
          traveling: timeTravel.traveling,
        },
      });
    }

    // After a hot-reload that preserved the model, restore the page's
    // model from the latest frame too. mountPages above already restored
    // the model from preservedScreenModels (which is updated on every
    // dispatch), so this would normally be a no-op — but if the user
    // was time-traveling at reload time, the live model is the frame
    // they had jumped to, and we want the panel state to reflect that.
    if (timeTravel.frames.length > 0) {
      pushTimeTravelState();
    }

    function renderTimeTravelPanel(container, state) {
      const total = state.frames.length;
      if (total === 0) {
        container.textContent = 'No actions yet. Interact with the app to capture a frame.';
        return;
      }
      const cursor = state.cursor;

      // Controls row.
      const controls = document.createElement('div');
      controls.style.display = 'flex';
      controls.style.gap = '6px';
      controls.style.alignItems = 'center';
      controls.style.marginBottom = '8px';

      const mkBtn = (label, title, onclick, disabled) => {
        const b = document.createElement('button');
        b.textContent = label;
        b.title = title;
        b.disabled = !!disabled;
        b.style.background = disabled ? '#374151' : '#374151';
        b.style.color = disabled ? '#6b7280' : '#f3f4f6';
        b.style.border = '1px solid #4b5563';
        b.style.borderRadius = '4px';
        b.style.padding = '2px 8px';
        b.style.cursor = disabled ? 'not-allowed' : 'pointer';
        b.style.fontFamily = 'inherit';
        b.style.fontSize = '12px';
        if (!disabled) b.onclick = onclick;
        return b;
      };

      controls.appendChild(mkBtn('⏮', 'Initial state', () => jumpToFrame(-1), cursor === -1));
      controls.appendChild(mkBtn('◀', 'Previous frame (← / ↓)', () => jumpToFrame(cursor - 1), cursor <= -1));
      controls.appendChild(mkBtn('▶', 'Next frame (→ / ↑)', () => jumpToFrame(cursor + 1), cursor >= total - 1));
      controls.appendChild(mkBtn('⏭', 'Latest', () => jumpToFrame(total - 1), cursor === total - 1));

      // Visual gap before the destructive reset button so users don't
      // confuse it with a navigation control.
      const sep = document.createElement('span');
      sep.style.width = '8px';
      controls.appendChild(sep);

      const resetBtn = mkBtn('↺', 'Reset: restore initial model and clear all frames',
        () => {
          if (confirm('Reset to initial state and clear all ' + total + ' frame' + (total === 1 ? '' : 's') + '?')) {
            resetTimeTravel();
          }
        }, false);
      resetBtn.style.color = '#fca5a5';
      resetBtn.style.borderColor = '#7f1d1d';
      controls.appendChild(resetBtn);

      // Slider. `oninput` fires continuously during drag; we update the
      // app + status text live but skip the full panel re-render so the
      // <input> survives the drag (otherwise the dock would rebuild it
      // mid-mousedown and the user could only step one frame per click).
      // `onchange` fires on release — that's when we sync the panel
      // (including the row-list highlight, traveling banner, etc.).
      const slider = document.createElement('input');
      slider.type = 'range';
      slider.min = '-1';
      slider.max = String(total - 1);
      slider.value = String(cursor);
      slider.className = 'mar-tt-slider';
      slider.style.flex = '1';
      slider.style.minWidth = '120px';
      // Paint the blue "filled" portion of the track up to the cursor
      // (webkit reads --tt-fill in the track gradient). value runs
      // -1..total-1, so (value + 1) / total maps to 0..100%.
      const setFill = (val) => {
        slider.style.setProperty('--tt-fill', ((parseInt(val, 10) + 1) / total * 100) + '%');
      };
      setFill(slider.value);

      const status = document.createElement('span');
      status.textContent = (cursor + 1) + ' / ' + total;
      status.style.color = '#9ca3af';
      status.style.minWidth = '50px';
      status.style.textAlign = 'right';

      slider.oninput = () => {
        const idx = parseInt(slider.value, 10);
        status.textContent = (idx + 1) + ' / ' + total;
        setFill(slider.value);
        jumpToFrame(idx, { skipPanelRefresh: true });
      };
      slider.onchange = () => {
        jumpToFrame(parseInt(slider.value, 10));
      };

      controls.appendChild(slider);
      controls.appendChild(status);

      container.appendChild(controls);

      if (state.traveling) {
        const note = document.createElement('div');
        note.textContent = 'Time travel active — new actions will branch from the current frame.';
        note.style.background = '#1e3a8a';
        note.style.color = '#dbeafe';
        note.style.padding = '4px 8px';
        note.style.borderRadius = '4px';
        note.style.marginBottom = '6px';
        note.style.fontSize = '11px';
        container.appendChild(note);
      }

      // Frame list (newest first). Fills the remaining vertical space
      // inside the panel and is the only scrollable region — controls /
      // banners stay sticky above it. Marked with data-scroll-key so
      // the dock preserves scroll position across re-renders.
      const list = document.createElement('div');
      list.setAttribute('data-scroll-key', 'frames');
      list.style.borderTop = '1px solid #374151';
      list.style.flex = '1';
      list.style.minHeight = '0';
      list.style.overflow = 'auto';

      const renderRow = (idx, label, isCursor, hadEffect) => {
        const row = document.createElement('div');
        if (isCursor) row.setAttribute('data-cursor-row', 'true');
        row.style.padding = '4px 6px';
        row.style.cursor = 'pointer';
        row.style.borderBottom = '1px solid #374151';
        row.style.display = 'flex';
        row.style.gap = '8px';
        row.style.alignItems = 'baseline';
        row.style.background = isCursor ? '#1e40af' : 'transparent';
        if (!isCursor) row.onmouseenter = () => (row.style.background = '#374151');
        if (!isCursor) row.onmouseleave = () => (row.style.background = 'transparent');
        const num = document.createElement('span');
        num.textContent = String(idx + 1).padStart(3, ' ');
        num.style.color = '#9ca3af';
        num.style.fontVariantNumeric = 'tabular-nums';
        const text = document.createElement('span');
        text.textContent = label;
        text.title = label;            // hover shows full label
        text.style.flex = '1';
        text.style.minWidth = '0';     // required for ellipsis inside a flex row
        text.style.color = '#e5e7eb';
        text.style.whiteSpace = 'nowrap';
        text.style.overflow = 'hidden';
        text.style.textOverflow = 'ellipsis';
        row.appendChild(num);
        row.appendChild(text);
        if (hadEffect) {
          const eff = document.createElement('span');
          eff.textContent = '⚠';
          eff.title = 'This frame triggered effects that won\'t be undone by time-travel.';
          eff.style.color = '#fbbf24';
          row.appendChild(eff);
        }
        row.onclick = () => jumpToFrame(idx);
        return row;
      };

      for (let i = state.frames.length - 1; i >= 0; i--) {
        const f = state.frames[i];
        list.appendChild(renderRow(i, displayValue(f.msg), i === cursor, f.hadEffect));
      }
      // "Initial state" row at the bottom.
      list.appendChild(renderRow(-1, '(initial state)', cursor === -1, false));
      container.appendChild(list);

      // Scroll the cursor row into view if it's outside the list's
      // visible area. `block: 'nearest'` is the right setting — it only
      // scrolls when needed (no jump if already visible) and picks the
      // shortest path (top vs bottom edge). Deferred via queueMicrotask
      // so it runs after the dock's scroll-position restore step,
      // overriding the saved position only when truly necessary.
      const queue = (typeof queueMicrotask === 'function')
        ? queueMicrotask
        : (fn) => setTimeout(fn, 0);
      queue(() => {
        const cursorRow = list.querySelector('[data-cursor-row="true"]');
        if (cursorRow && typeof cursorRow.scrollIntoView === 'function') {
          cursorRow.scrollIntoView({ block: 'nearest' });
        }
      });
    }

    // Wire the module-level keyboard handler (registered once per page
    // load up at the top of the IIFE) into THIS mountPages's jumpToFrame
    // closure. Each hot-reload re-runs mountPages and re-points to the
    // fresh closure; the listener itself is never re-registered, so
    // arrow keys don't multiply.
    currentJumpToFrame = jumpToFrame;
    } // end if (__MAR_DEV__) — time-travel block

    currentDispatch = (msg) => {
      const pg = currentPage();
      if (!pg) return;

      const prevModel = pg.model;
      const out = unwrapModelTuple(updateWithUser(pg, msg, prevModel));
      pg.model = out.model;
      // model setter already writes to preservedScreenModels[activeKey];
      // no separate write needed here.

      // Frame capture is dev-only — no need to keep the history around in
      // production (and it would just leak memory).
      if (__MAR_DEV__ && global.__marDevMode) {
        if (timeTravel.cursor < timeTravel.frames.length - 1) {
          timeTravel.frames = timeTravel.frames.slice(0, timeTravel.cursor + 1);
        }
        const hadEffect = !!(out.effect && out.effect.k === 'E' && out.effect.tag !== 'none');
        timeTravel.frames.push({
          msg, prevModel, nextModel: out.model, hadEffect,
          // pagePath records activeKey (URL for dynamic pages, pattern
          // path otherwise) so jumping back routes to the right model
          // slot.
          pagePath: pg.activeKey, time: Date.now(),
        });
        timeTravel.cursor = timeTravel.frames.length - 1;
        timeTravel.traveling = false;
        pushTimeTravelState();
      }

      render();
      runEffect(out.effect);
    };

    // Stamp marDepth: 0 onto the initial history entry so every
    // subsequent push/replace can read + bump it. Without this, the
    // first navigation sees `history.state == null`, treats depth as
    // 0, pushes with depth 1 — which is correct but only by accident.
    // Doing it explicitly here keeps `currentNavDepth()` honest from
    // the very first render onward.
    if (history.state == null || typeof history.state.marDepth !== 'number') {
      replaceNav(window.location.pathname + window.location.search + window.location.hash);
    }

    // Replace the previous popstate listener (from the prior
    // mountPages, if any) with this mount\'s render. See the
    // `prevPopstateHandler` declaration above for why this matters
    // on hot reload.
    //
    // We wrap render() in a shim that inspects the PopStateEvent's
    // `hasUAVisualTransition` flag — set to true by Safari (and
    // other UAs) when a back/forward traversal triggered by a
    // platform gesture (two-finger swipe-back on macOS Safari) is
    // ALREADY being visually transitioned by the browser. Layering
    // our own startViewTransition on top of that produces a
    // visible double-animation. The shim suppresses our transition
    // for exactly that one render.
    const handlePopState = (ev) => {
      if (ev && ev.hasUAVisualTransition) {
        skipNextViewTransition = true;
      }
      render();
    };
    if (prevPopstateHandler) {
      window.removeEventListener('popstate', prevPopstateHandler);
    }
    prevPopstateHandler = handlePopState;
    window.addEventListener('popstate', handlePopState);
    render();
    return VUnit();
  }

  // ---------- Module loader ----------

  function loadModule(env, mod, modulesByName) {
    const modName = (mod.name || []).join('.');
    // Per-module env scoping. The synthetic `__entry` module
    // (apphost.PickFrontMods appends it with `name: nil`) is special-
    // cased: its `__entry` value has to live in the shared env so
    // marRun's `envLookup(env, program.entry)` finds it. Real modules
    // get their own frame so two modules that both declare a bare
    // `page` / `renderBody` / `projectsSection` (and bigapp has all
    // three) don't clobber each other in the shared bindings — the
    // last-write-wins on a shared env makes the result depend on the
    // non-deterministic topo iteration order, which is why this bug
    // was intermittent. Closures captured in modEnv chain to the
    // shared env via parent so cross-module qualified lookups still
    // succeed.
    const modEnv = modName ? envNew(env) : env;

    // Pass 0: process `import M exposing (...)` so bare names bind to
    // already-known qualified values. Mirrors the typechecker; without
    // this, code that typechecks (e.g. `column [...]` after
    // `import View exposing (column)`) explodes at runtime with
    // "unbound name: column".
    if (mod.imports) {
      for (const imp of mod.imports) {
        if ((!imp.exposing || imp.exposing.length === 0) && !imp.all) continue;
        const impName = (imp.module || []).join('.');
        // `exposing (..)`: bind every export of the module bare —
        // values and ctors registered as `impName.x` in the env chain
        // (for builtin modules like UI, the whole vocabulary). Mirrors
        // the typechecker's wildcard handling.
        if (imp.all) {
          const exports = envExportsOf(modEnv, impName);
          for (const name in exports) envDefine(modEnv, name, exports[name]);
        }
        if (!imp.exposing) continue;
        for (const item of imp.exposing) {
          const qualified = impName + '.' + item.name;
          const v = envLookup(modEnv, qualified);
          if (v !== undefined) {
            envDefine(modEnv, item.name, v);
          }
          // `Type(..)` — pull every constructor of the imported type
          // into the bare namespace too. The imported module's AST is
          // the source of truth for the ctor list.
          if (item.open && modulesByName) {
            const impMod = modulesByName[impName];
            if (impMod && impMod.decls) {
              for (const d of impMod.decls) {
                if (d.kind !== 'CustomTypeDecl' || d.name !== item.name) continue;
                for (const c of d.constructors) {
                  const cv = envLookup(modEnv, impName + '.' + c.name);
                  if (cv !== undefined) envDefine(modEnv, c.name, cv);
                }
              }
            }
          }
        }
      }
    }
    // Pass 1: register custom-type constructors. Bare goes into
    // modEnv (module-local); qualified `Module.Ctor` goes into the
    // shared env so `import M exposing (T(..))` from other modules
    // (Pass 0 above) can find it.
    for (const d of mod.decls) {
      if (d.kind === 'CustomTypeDecl') {
        const ctorNames = [];
        let allZeroArg = true;
        for (const c of d.constructors) {
          const arity = c.argCount;
          const ctor = arity === 0
            ? VCtor(c.name)
            : native(arity, args => VCtor(c.name, args));
          envDefine(modEnv, c.name, ctor);
          if (modName) envDefine(env, modName + '.' + c.name, ctor);
          ctorNames.push(c.name);
          if (arity > 0) allZeroArg = false;
        }
        // Path-pattern enum registry: a `{role:Role}` segment in
        // some Page.dynamic looks up `Role` here to know the URL ↔
        // ctor mapping. Mirrors the Go runtime's RegisterEnumType.
        // Only zero-arg-ctor types qualify; types with payload are
        // rejected by the typechecker for path use, but the filter
        // here is defensive.
        if (allZeroArg) {
          enumTypes[d.name] = ctorNames;
          if (modName) enumTypes[modName + '.' + d.name] = ctorNames;
        }
      }
    }
    // Pass 2: pre-bind value names with placeholders. Module-local —
    // only this module's body needs the self-reference; other modules
    // reach the value via its qualified alias once Pass 3 runs.
    for (const d of mod.decls) {
      if (d.kind === 'ValueDecl') {
        envDefine(modEnv, d.name, VUnit());
      }
    }
    // Pass 3: evaluate. Bodies resolve bare names against modEnv
    // (shadowing but chaining to env); closures captured here keep
    // modEnv as their lexical env, so calls from other modules still
    // see this module's own bare bindings.
    for (const d of mod.decls) {
      if (d.kind !== 'ValueDecl') continue;
      let body = d.body;
      if (d.params && d.params.length > 0) {
        body = { kind: 'ELambda', params: d.params, body };
      }
      let val = evalExpr(body, modEnv);
      envDefine(modEnv, d.name, val);
      if (modName) envDefine(env, modName + '.' + d.name, val);
    }
  }

  // ---------- Public entry ----------

  global.marRun = function (program) {
    // devMode is sticky on the global so mountPages, setupDevChannel,
    // and the time-travel capture path all check the same source. The
    // server stamps it into program.json (see makeProgramJSON in
    // internal/jsserve/livereload.go and internal/scaffold/build.go).
    global.__marDevMode = !!program.devMode;
    // Auth metadata baked in by the server. Main.mar isn't in the
    // browser bundle (only page-reachable modules are), so the
    // server hands resolved auth config — currently signInPath for
    // Page.protected — through this side channel.
    if (program.auth && typeof program.auth.signInPath === 'string') {
      globalThis.__marAuthSignInPath = program.auth.signInPath;
    }
    const env = makeBuiltinEnv();
    // Modern wire format: a list of modules, loaded in dependency
    // order (the server topo-sorted them). Each module's decls get
    // bound bare (for intra-module references) AND under a qualified
    // `Module.name` alias (so EQualified lookups across modules
    // resolve). Backward-compat: older bundles sent a single `module`.
    const modules = Array.isArray(program.modules)
      ? program.modules
      : (program.module ? [program.module] : []);
    // Index modules by dotted name so loadModule's Pass 0 can resolve
    // `import M exposing (Type(..))` against M's AST (we need M's
    // constructor list to expose them all as bare).
    const modulesByName = Object.create(null);
    for (const m of modules) {
      const name = (m.name || []).join('.');
      if (name) modulesByName[name] = m;
    }
    for (const mod of modules) {
      loadModule(env, mod, modulesByName);
    }
    const main = envLookup(env, program.entry || 'main');
    if (main === undefined) {
      throw new Error('entry not found: ' + (program.entry || 'main'));
    }
    if (main.k !== 'E') {
      throw new Error('entry value is not an Effect (got ' + main.k + ')');
    }
    main.run();
  };

  // marReload tears down the currently running app and re-mounts a fresh
  // one from the latest program.json. Called by the SSE reload listener.
  // Tearing down means: stop dispatching (so any listeners still attached
  // to old DOM nodes become no-ops) and clear the mount root. The old
  // mountApp / mountScreens closures become unreachable garbage.
  global.marReload = function () {
    currentDispatch = null;
    const root = document.getElementById('mar-root');
    if (root) while (root.firstChild) root.removeChild(root.firstChild);
    return fetch('/_mar/program.json', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (p) { global.marRun(p); })
      .catch(function (err) {
        console.error('marReload failed:', err);
        if (root) {
          const pre = document.createElement('pre');
          pre.style.color = '#b00';
          pre.textContent = String(err && err.message || err);
          root.appendChild(pre);
        }
      });
  };

  // marBootstrap is the entry point invoked from the host HTML page.
  // It fetches the program, runs it, and (in dev) opens the SSE
  // connection for hot reload + compile-error events.
  //
  // The HTML head includes <link rel="preload" href="/_mar/program
  // .json" as="fetch">, so this fetch is typically a cache hit on the
  // browser's preload buffer — the actual download started in parallel
  // with runtime.js as soon as the head was parsed. Total wait is
  // max(T_runtime_load, T_program_download), not their sum.
  //
  // No `cache: 'no-store'` here even though the response is meant to
  // be fresh on every cold start. The server already pins
  // `Cache-Control: no-store` on the response (see
  // /_mar/program.json handler in server.go), so the browser won't
  // persist it between page loads. Setting `cache: 'no-store'` on
  // the fetch *additionally* would make Chrome refuse to use the
  // preloaded entry (the cache modes have to match), which manifests
  // as the warning "preloaded using link preload but not used within
  // a few seconds" — and the second network round-trip defeats the
  // whole point of the preload.
  global.marBootstrap = function () {
    // Reuse the eager fetch the HTML shell started (see server.go's
    // inline <script> in the <head>) so we don't issue a second
    // network round-trip. Fall back to a fresh fetch when the shell
    // didn't provide one (e.g. when the runtime is mounted by a
    // non-mar host page like the static `dist/` build).
    var pending = global.__marProgramPromise
      || fetch('/_mar/program.json').then(function (r) { return r.json(); });
    pending
      .then(function (p) {
        try { global.marRun(p); }
        catch (e) {
          console.error(e);
          const root = document.getElementById('mar-root');
          if (root) {
            // Wipe the boot placeholder (and anything else) so the
            // error message stands alone — otherwise "Loading…" stays
            // stacked above the red error which reads like a fresh
            // load is still in progress.
            while (root.firstChild) root.removeChild(root.firstChild);
            const pre = document.createElement('pre');
            pre.style.color = '#b00';
            pre.textContent = String(e && e.message || e);
            root.appendChild(pre);
          }
        }
        if (__MAR_DEV__ && global.__marDevMode) setupDevChannel();
      });
  };

  // setupDevChannel opens the SSE connection used by `mar dev` for both
  // hot reload and dev-time UI feedback (compile errors, server-down
  // detection). Skipped when EventSource isn't available — the app
  // still runs, just without dev affordances.
  //
  // Both the compile-error and connection state are reported to the dev
  // dock as panels; same dock that mountPages uses for the time-travel
  // panel. One bottom-right widget hosts every dev affordance.
  let setupDevChannel = null;
  let getDevDock = null;
  let displayValue = null;
  if (__MAR_DEV__) {
  setupDevChannel = function () {
    if (typeof EventSource === 'undefined') return;

    const dock = getDevDock();

    dock.registerPanel({
      id: 'compile-error',
      badge: {
        icon: '⨯',
        color: '#fca5a5',
        title: 'Compile error',
        label: () => 'compile error',
        visible: (s) => !!s.message,
        pulse: (s) => !!s.message,
      },
      render: (container, state) => {
        const pre = document.createElement('pre');
        pre.style.margin = '0';
        pre.style.whiteSpace = 'pre-wrap';
        pre.style.color = '#fecaca';
        pre.textContent = state.message || '';
        container.appendChild(pre);
      },
      initialState: { message: '' },
    });

    dock.registerPanel({
      id: 'connection',
      badge: {
        icon: '⚠',
        color: '#fcd34d',
        title: 'Connection',
        label: () => 'offline',
        visible: (s) => s.disconnected,
        pulse: (s) => s.disconnected,
      },
      render: (container, state) => {
        container.textContent = state.disconnected
          ? 'Server offline. Reconnecting…'
          : 'Connected.';
      },
      initialState: { disconnected: false },
    });

    let disconnectTimer = null;
    const clearDisconnectTimer = () => {
      if (disconnectTimer) { clearTimeout(disconnectTimer); disconnectTimer = null; }
    };

    const es = new EventSource('/_mar/reload');

    // Tracks whether we've ever had a live connection. The first onopen
    // is the initial connect; any *later* onopen is a reconnect, which
    // on localhost almost always means `mar dev` was restarted.
    let everConnected = false;

    es.onopen = function () {
      clearDisconnectTimer();
      if (everConnected) {
        // The SSE stream dropped and came back — a server restart. Since
        // runtime.js (and the CSS it injects via ensureUIStyles) is
        // embedded in the `mar` binary, a rebuilt binary serves a new
        // runtime.js, but the soft hot-reload path (marReload) only
        // refetches program.json and would keep the STALE bundle + its
        // injected styles running in this tab. That's exactly how a
        // freshly-fixed CSS rule appears not to take effect until a
        // manual refresh. A full reload refetches runtime.js so the new
        // build actually lands. In-process .mar hot-reloads never drop
        // the connection (the same process recompiles and broadcasts a
        // `reload` event), so this only fires on a real restart.
        location.reload();
        return;
      }
      everConnected = true;
      dock.updatePanel('connection', { disconnected: false });
    };

    es.onmessage = function (ev) {
      let payload;
      try { payload = JSON.parse(ev.data); } catch (_) { return; }
      if (payload.type === 'reload') {
        dock.updatePanel('compile-error', { message: '' });
        global.marReload();
      } else if (payload.type === 'ok') {
        dock.updatePanel('compile-error', { message: '' });
      } else if (payload.type === 'error') {
        dock.updatePanel('compile-error', { message: payload.message });
      }
    };

    es.onerror = function () {
      // EventSource fires onerror on every drop. Wait ~1s before
      // surfacing — short blips (server restart inside hot-reload,
      // network hiccup) shouldn't flash a badge.
      clearDisconnectTimer();
      disconnectTimer = setTimeout(function () {
        if (es.readyState !== 1 /* OPEN */) {
          dock.updatePanel('connection', { disconnected: true });
        }
      }, 1000);
    };
  }

  // ---------- Dev dock ----------
  //
  // A single bottom-right widget that hosts pluggable panels: compile
  // errors, connection state, time travel, and (in the future) state
  // inspectors, network logs, etc. Each panel registers a badge that
  // appears in the bar; clicking the badge expands the panel's content
  // above the bar. Only one panel is expanded at a time.
  //
  // The dock is a singleton on window so that hot-reload (which
  // re-creates `currentDispatch` and the page closures) doesn't
  // accidentally instantiate a duplicate.

  getDevDock = function () {
    if (global.__marDevDock) return global.__marDevDock;
    return (global.__marDevDock = createDevDock());
  };

  function createDevDock() {
    // Inject pulse animation once.
    if (!document.getElementById('mar-dock-style')) {
      const style = document.createElement('style');
      style.id = 'mar-dock-style';
      style.textContent =
        '@keyframes mar-dock-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.55; } }' +
        '.mar-dock-badge { padding: 4px 8px; border-radius: 4px; cursor: pointer; user-select: none; }' +
        '.mar-dock-badge:hover { background: #374151; }' +
        '.mar-dock-badge.active { background: #374151; }' +
        '.mar-dock-badge.pulse { animation: mar-dock-pulse 1.2s ease-in-out infinite; }' +
        // Time-travel scrubber — custom range so the thumb reaches both
        // ends (the native thumb insets by half its width, leaving a gap)
        // and matches the dark dock. The track fills blue up to the
        // cursor via the --tt-fill CSS var (set in renderTimeTravelPanel);
        // Firefox gets the same fill from ::-moz-range-progress.
        '.mar-tt-slider { -webkit-appearance: none; appearance: none; height: 16px; margin: 0; padding: 0; background: transparent; cursor: pointer; vertical-align: middle; }' +
        '.mar-tt-slider::-webkit-slider-runnable-track { height: 4px; border-radius: 2px; background: linear-gradient(to right, #3b82f6 var(--tt-fill, 100%), #4b5563 var(--tt-fill, 100%)); }' +
        '.mar-tt-slider::-webkit-slider-thumb { -webkit-appearance: none; appearance: none; width: 16px; height: 10px; margin-top: -3px; border-radius: 5px; background: #f3f4f6; border: none; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4); }' +
        '.mar-tt-slider::-moz-range-track { height: 4px; border-radius: 2px; background: #4b5563; }' +
        '.mar-tt-slider::-moz-range-progress { height: 4px; border-radius: 2px; background: #3b82f6; }' +
        '.mar-tt-slider::-moz-range-thumb { width: 16px; height: 10px; border: none; border-radius: 5px; background: #f3f4f6; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4); }' +
        '.mar-tt-slider:focus { outline: none; }' +
        '.mar-tt-slider:focus-visible::-webkit-slider-thumb { box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.5); }';
      document.head.appendChild(style);
    }

    const root = document.createElement('div');
    root.id = 'mar-dev-dock';
    root.style.position = 'fixed';
    root.style.bottom = '0';
    root.style.right = '8px';
    root.style.zIndex = '9999';
    root.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, monospace';
    root.style.fontSize = '12px';
    root.style.display = 'none';
    // Pin the dock during page view-transitions. The default
    // `::view-transition-old(root)` / `::view-transition-new(root)`
    // snapshots include the whole document — dock and all — so the
    // dock slides with the page despite being at `position: fixed`.
    // Giving it its own `view-transition-name` opts it out of the
    // root group: the browser snapshots the dock as a separate,
    // continuous element across old→new. Combined with the CSS
    // rule disabling animation for that name (see ensureUIStyles),
    // the dock stays visually anchored to the bottom-right while
    // the page slides underneath.
    root.style.viewTransitionName = 'mar-dev-dock';

    const panelEl = document.createElement('div');
    panelEl.style.background = '#1f2937';
    panelEl.style.color = '#f3f4f6';
    panelEl.style.borderRadius = '6px 6px 0 0';
    panelEl.style.boxShadow = '0 -4px 16px rgba(0,0,0,0.3)';
    // Fixed dimensions so the panel doesn't reflow as content grows
    // (e.g. a DraftChanged frame label getting longer with each
    // keystroke, or new frames appearing). Stable size avoids visual
    // jitter while the user is reading or interacting. Capped at 90vw /
    // 90vh for narrow viewports.
    panelEl.style.width = '480px';
    panelEl.style.maxWidth = '90vw';
    panelEl.style.height = '420px';
    panelEl.style.maxHeight = '90vh';
    panelEl.style.display = 'none';
    panelEl.style.flexDirection = 'column';
    panelEl.style.overflow = 'hidden';

    const panelHeader = document.createElement('div');
    panelHeader.style.padding = '6px 10px';
    panelHeader.style.background = '#111827';
    panelHeader.style.display = 'flex';
    panelHeader.style.justifyContent = 'space-between';
    panelHeader.style.alignItems = 'center';
    panelHeader.style.fontWeight = '600';
    const panelTitle = document.createElement('span');
    panelHeader.appendChild(panelTitle);
    const closeBtn = document.createElement('button');
    closeBtn.textContent = '×';
    closeBtn.title = 'Close (Esc)';
    closeBtn.style.background = 'transparent';
    closeBtn.style.border = 'none';
    closeBtn.style.color = '#9ca3af';
    closeBtn.style.cursor = 'pointer';
    closeBtn.style.fontSize = '16px';
    closeBtn.style.lineHeight = '1';
    closeBtn.style.padding = '0 4px';
    closeBtn.onclick = () => collapse();
    panelHeader.appendChild(closeBtn);

    const panelBody = document.createElement('div');
    panelBody.style.padding = '8px 10px';
    panelBody.style.flex = '1';
    panelBody.style.overflow = 'hidden';
    panelBody.style.display = 'flex';
    panelBody.style.flexDirection = 'column';
    panelBody.style.minHeight = '0'; // allow flex children to shrink properly

    panelEl.appendChild(panelHeader);
    panelEl.appendChild(panelBody);

    const bar = document.createElement('div');
    bar.style.background = '#1f2937';
    bar.style.color = '#f3f4f6';
    bar.style.padding = '4px 6px';
    bar.style.borderTopLeftRadius = '6px';
    bar.style.borderTopRightRadius = '6px';
    bar.style.display = 'flex';
    bar.style.gap = '4px';
    bar.style.alignItems = 'center';
    bar.style.boxShadow = '0 -2px 8px rgba(0,0,0,0.2)';

    root.appendChild(panelEl);
    root.appendChild(bar);
    document.body.appendChild(root);

    const panels = []; // [{ id, badge, render, state }]
    let expandedId = null;

    function rerenderBar() {
      bar.innerHTML = '';
      let visibleCount = 0;
      for (const p of panels) {
        const visible = p.badge.visible ? p.badge.visible(p.state) : true;
        if (!visible) continue;
        visibleCount++;
        const el = document.createElement('span');
        el.className = 'mar-dock-badge'
          + (expandedId === p.id ? ' active' : '')
          + (p.badge.pulse && p.badge.pulse(p.state) ? ' pulse' : '');
        const icon = p.badge.icon || '•';
        const color = p.badge.color || '#f3f4f6';
        const label = p.badge.label ? p.badge.label(p.state) : p.id;
        el.innerHTML =
          '<span style="color: ' + color + '">' + icon + '</span>'
          + ' <span style="color: #e5e7eb">' + label + '</span>';
        el.onclick = () => toggle(p.id);
        bar.appendChild(el);
      }
      root.style.display = visibleCount === 0 ? 'none' : 'block';
      if (expandedId) {
        const cur = panels.find(p => p.id === expandedId);
        if (cur && cur.badge.visible && !cur.badge.visible(cur.state)) collapse();
      }
    }

    function expand(id) {
      const p = panels.find(p => p.id === id);
      if (!p) return;
      expandedId = id;
      panelTitle.textContent = p.badge.title || p.id;
      panelBody.innerHTML = '';
      p.render(panelBody, p.state, makeHelpers(p));
      panelEl.style.display = 'flex';
      rerenderBar();
      persistExpanded();
    }

    function collapse() {
      expandedId = null;
      panelEl.style.display = 'none';
      rerenderBar();
      persistExpanded();
    }

    function toggle(id) {
      if (expandedId === id) collapse(); else expand(id);
    }

    function rerenderExpanded() {
      if (!expandedId) return;
      const p = panels.find(p => p.id === expandedId);
      if (!p) return;
      // Preserve scroll positions across re-renders so clicking an item
      // in a long list doesn't reset the user's scroll. Panels mark
      // scrollable containers with `data-scroll-key="<id>"`; we snapshot
      // before re-render and restore after.
      const scrolls = {};
      panelBody.querySelectorAll('[data-scroll-key]').forEach(function (el) {
        scrolls[el.getAttribute('data-scroll-key')] = el.scrollTop;
      });
      panelBody.innerHTML = '';
      p.render(panelBody, p.state, makeHelpers(p));
      panelBody.querySelectorAll('[data-scroll-key]').forEach(function (el) {
        const key = el.getAttribute('data-scroll-key');
        if (scrolls[key] !== undefined) el.scrollTop = scrolls[key];
      });
    }

    function makeHelpers(p) {
      return { update: (s) => api.updatePanel(p.id, s) };
    }

    function persistExpanded() {
      try { localStorage.setItem('mar-dev-dock-expanded', expandedId || ''); } catch (_) {}
    }

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && expandedId) collapse();
    });

    const api = {
      registerPanel(spec) {
        const idx = panels.findIndex(p => p.id === spec.id);
        const panel = {
          id: spec.id,
          badge: spec.badge || {},
          render: spec.render || (() => {}),
          state: spec.initialState || {},
        };
        if (idx >= 0) panels[idx] = panel;
        else panels.push(panel);
        rerenderBar();
        // Lazy-restore previously expanded panel after registration.
        try {
          const want = localStorage.getItem('mar-dev-dock-expanded');
          if (want === spec.id && expandedId !== want) {
            const visible = panel.badge.visible ? panel.badge.visible(panel.state) : true;
            if (visible) expand(spec.id);
          }
        } catch (_) {}
      },
      updatePanel(id, newState) {
        const p = panels.find(p => p.id === id);
        if (!p) return;
        p.state = typeof newState === 'function'
          ? newState(p.state)
          : Object.assign({}, p.state, newState);
        rerenderBar();
        if (expandedId === id) rerenderExpanded();
      },
      getPanelState(id) {
        const p = panels.find(p => p.id === id);
        return p ? p.state : null;
      },
      expand,
      collapse,
    };
    return api;
  }

  // displayValue formats a runtime value as a short, readable string for
  // dev-tool UIs (e.g. listing recent Msgs in the time-travel panel). Not
  // round-trippable; truncates deeply-nested values. Only callers are
  // inside __MAR_DEV__ blocks, so the assignment is dropped from prod.
  displayValue = function (v, depth) {
    if (depth === undefined) depth = 0;
    if (depth > 3) return '…';
    if (v === null || v === undefined) return String(v);
    switch (v.k) {
      case 'I': return String(v.n);
      case 'F': return String(v.n);
      case 'S': return JSON.stringify(v.s);
      case 'B': return v.b ? 'True' : 'False';
      case 'U': return '()';
      case 'L': return '[' + v.xs.map(function (x) { return displayValue(x, depth + 1); }).join(', ') + ']';
      case 'T': return '(' + v.xs.map(function (x) { return displayValue(x, depth + 1); }).join(', ') + ')';
      case 'R': {
        const fields = (v.order || Object.keys(v.fields || {})).map(function (k) {
          return k + ' = ' + displayValue(v.fields[k], depth + 1);
        });
        return '{ ' + fields.join(', ') + ' }';
      }
      case 'C':
        if (!v.args || v.args.length === 0) return v.tag;
        return v.tag + ' ' + v.args.map(function (x) {
          var inner = displayValue(x, depth + 1);
          return /\s/.test(inner) ? '(' + inner + ')' : inner;
        }).join(' ');
      case 'E': return '<effect>';
      case 'SUB': return '<sub>';
      case 'Fn': return '<fn>';
      case 'V': return '<view>';
      default: return '<?>';
    }
  };
  } // end if (__MAR_DEV__)
})(typeof window !== 'undefined' ? window : globalThis);
