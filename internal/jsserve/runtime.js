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
    if (v === null || v === undefined) return VCtor('Nothing');
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
        // Maybe / Result get convenience encodings (transparent /
        // shorthand error). All other constructors round-trip with
        // the marker convention shared by the Go encoders.
        if (v.tag === 'Just') return marToJs(v.args[0]);
        if (v.tag === 'Nothing') return null;
        if (v.tag === 'Ok') return marToJs(v.args[0]);
        if (v.tag === 'Err') return { error: marToJs(v.args[0]) };
        if (!v.args || v.args.length === 0) return { __ctor: v.tag };
        return { __ctor: v.tag, __args: v.args.map(marToJs) };
      case 'D': return v.seconds;
      case 'TM': return { __time: new Date(v.millis).toISOString() };
      default: return null;
    }
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

  function matchInto(pat, v, bindings) {
    switch (pat.kind) {
      case 'PWildcard': return true;
      case 'PVar': bindings[pat.name] = v; return true;
      case 'PInt': return v.k === 'I' && v.n === pat.value;
      case 'PString': return v.k === 'S' && v.s === pat.value;
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
    }
    return false;
  }

  // ---------- Eval ----------

  function evalExpr(e, env) {
    switch (e.kind) {
      case 'EInt':    return VInt(e.value);
      case 'EFloat':  return VFloat(e.value);
      case 'EString': return VString(e.value);
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
        throw new Error('no case branch matched');
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
    }
    return false;
  }

  function cmpValues(a, b) {
    if (a.k === 'I' || a.k === 'F') return a.n - b.n;
    if (a.k === 'S') return a.s < b.s ? -1 : a.s > b.s ? 1 : 0;
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

  function makeBuiltinEnv() {
    const env = envNew(null);
    const def = (n, v) => envDefine(env, n, v);

    // Booleans / Maybe / Result constructors
    def('True',  VBool(true));
    def('False', VBool(false));
    def('Nothing', VCtor('Nothing'));
    def('Just', native(1, args => VCtor('Just', [args[0]])));
    def('Ok',  native(1, args => VCtor('Ok',  [args[0]])));
    def('Err', native(1, args => VCtor('Err', [args[0]])));

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
    def('list',  uiContentOnly('uiList')); def('UI.list',    uiContentOnly('uiList'));
    def('uiSection', uiContainer('uiSection')); def('UI.section', uiContainer('uiSection'));
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

    // text — UI version takes no attrs.
    const uiTextCtor = native(1, ([s]) => VView('text', [], [], s.s));
    def('uiText', uiTextCtor); def('UI.text', uiTextCtor);

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

    // title / subtitle — reuse the existing "title" / "subtitle"
    // tags so createDOM's existing handling applies; UI styles
    // (font-size / weight / color) live in ensureUIStyles().
    const uiTitleCtor = native(1, ([s]) => VView('title', [], [], s.s));
    def('uiTitle', uiTitleCtor); def('UI.title', uiTitleCtor);
    const uiSubtitleCtor = native(1, ([s]) => VView('subtitle', [], [], s.s));
    def('uiSubtitle', uiSubtitleCtor); def('UI.subtitle', uiSubtitleCtor);

    // link : Path r -> r -> String -> View msg
    // Renders as <a href="...">label</a> with a stamped href built
    // from the typed Path + record (same machinery as linkTo).
    def('uiLink', native(3, ([pathV, params, label]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('UI.link: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      const attrs = [{ name: 'href', value: VString(url) }];
      return VView('link', attrs, [], label.s);
    }));
    def('UI.link', envLookup(env, 'uiLink'));

    // empty — no-op placeholder; same VView the existing renderer
    // already knows to handle (display: none).
    def('uiEmpty', VView('empty', [], [], ''));
    def('UI.empty', VView('empty', [], [], ''));

    // centered : View msg -> View msg
    // Wraps child in a "centered" view tag. The renderer (createDOM
    // case 'centered' below) renders a flex container that fills
    // available space and centers its child.
    const uiCenteredCtor = native(1, ([child]) =>
      VView('centered', [], [child], ''));
    def('uiCentered', uiCenteredCtor); def('UI.centered', uiCenteredCtor);

    // Modifier attrs.
    const navTitleCtor   = native(1, ([s]) => makeAttr('navigationTitle', s));
    def('navigationTitle', navTitleCtor); def('UI.navigationTitle', navTitleCtor);
    const trailingCtor   = native(1, ([v]) => makeAttr('trailing', v));
    def('trailing', trailingCtor); def('UI.trailing', trailingCtor);
    const leadingCtor    = native(1, ([v]) => makeAttr('leading', v));
    def('leading', leadingCtor); def('UI.leading', leadingCtor);
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
      return VCtor('__Page', [f.path, f.init, f.update, f.view, title]);
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
      return VCtor('__ProtectedPage', [f.path, f.init, f.update, f.view, title]);
    });
    def('pageProtected', pageProtectedImpl);
    def('Page.protected', pageProtectedImpl);

    // Page.dynamic — pattern path with `:param` segments. The runtime
    // matches the URL against the pattern at navigation time, threading
    // a Params record through init/update/view as the leading argument.
    // The wire-format ctor is __DynamicPage; mountPages parses the path
    // pattern lazily so the framework only walks segments once per page.
    const pageDynamicImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__DynamicPage', [f.path, f.init, f.update, f.view, title]);
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
      return VCtor('__DynamicProtectedPage', [f.path, f.init, f.update, f.view, title]);
    });
    def('pageDynamicProtected', pageDynamicProtectedImpl);
    def('Page.dynamicProtected', pageDynamicProtectedImpl);

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

    // Nav.afterSignIn : Effect e ()
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
    //       ( model, Nav.afterSignIn )
    //
    // Replaces the older `Nav.replace "/"` pattern. Pages that don't
    // care about return-to-origin can keep using Nav.replace directly.
    def('navAfterSignIn', VEffect(() => {
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
      // nav.replace because the post-replace render may itself fire
      // a Service.call that would race against a stale flag.
      globalThis.__marRedirectingToSignIn = false;
      nav.replace(target);
      return VUnit();
    }, 'navAfterSignIn'));
    def('Nav.afterSignIn', envLookup(env, 'navAfterSignIn'));

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
    def('Effect.succeed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('effectMap', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('Effect.map', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('effectAndThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('Effect.andThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));

    // Effect.fail : e -> Effect e a — throws when run. Uncaught
    // failures surface as a JS exception in the dispatcher; user
    // code that wants typed recovery should use Result.* instead.
    const effectFailImpl = native(1, ([err]) => VEffect(() => {
      const msg = err && err.k === 'S' ? err.s : ('(' + (err && err.k) + ')');
      throw new Error('Effect.fail: ' + msg);
    }, 'fail'));
    def('effectFail', effectFailImpl);
    def('Effect.fail', effectFailImpl);

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
    def('Effect.forEach', effectForEachImpl);

    // Effect.sequence : List (Effect e a) -> Effect e (List a)
    const effectSequenceImpl = native(1, ([list]) => VEffect(() => {
      const out = new Array(list.xs.length);
      for (let i = 0; i < list.xs.length; i++) {
        out[i] = list.xs[i].run();
      }
      return VList(out);
    }, 'sequence'));
    def('effectSequence', effectSequenceImpl);
    def('Effect.sequence', effectSequenceImpl);

    def('effectNone', VEffect(() => VUnit(), 'none'));
    def('Effect.none', VEffect(() => VUnit(), 'none'));

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
          .then(r => r.text().then(t => ({ ok: r.ok, body: t })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(r.body || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
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

    // Endpoint.* — typed contract shared between backend and frontend.
    // The runtime stores method+path; Endpoint.call uses fetch under the hood.
    def('endpointGet',    native(1, ([p]) => VCtor('__Ep', [VString('GET'), p])));
    def('Endpoint.get',    native(1, ([p]) => VCtor('__Ep', [VString('GET'), p])));
    def('endpointPost',   native(1, ([p]) => VCtor('__Ep', [VString('POST'), p])));
    def('Endpoint.post',   native(1, ([p]) => VCtor('__Ep', [VString('POST'), p])));
    def('endpointPatch',  native(1, ([p]) => VCtor('__Ep', [VString('PATCH'), p])));
    def('Endpoint.patch',  native(1, ([p]) => VCtor('__Ep', [VString('PATCH'), p])));
    def('endpointDelete', native(1, ([p]) => VCtor('__Ep', [VString('DELETE'), p])));
    def('Endpoint.delete', native(1, ([p]) => VCtor('__Ep', [VString('DELETE'), p])));
    // Endpoint.call : String -> Endpoint -> String -> (Result String String -> msg) -> Effect e msg
    //   base, endpoint, body, toMsg
    def('endpointCall', native(4, ([base, ep, body, toMsg]) => {
      const method = ep.args[0].s;
      const path = ep.args[1].s;
      const url = base.s + path;
      return VEffect(() => {
        const opts = { method };
        if (method !== 'GET' && method !== 'DELETE') opts.body = body.s;
        fetch(url, opts)
          .then(r => r.text().then(t => ({ ok: r.ok, body: t })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(r.body || ('HTTP ' + r.status))]);
            const msg = apply(toMsg, result);
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'endpointCall');
    }));
    def('Endpoint.call', envLookup(env, 'endpointCall'));

    // Service (RPC over HTTP).
    //
    // Service ctor: server-side wraps a handler. Browser-side, the
    // handler is never invoked — the constructor just returns an opaque
    // "__Service" value so it survives loadModule. The binding name +
    // module that the user wrote is stamped onto this value at load
    // time (see loadModule below) so Service.call can route to the
    // correct URL.
    // Service.declare: a typed RPC contract with no handler. Each
    // top-level binding gets its own instance via the loader's
    // origin-stamping pass; the browser only needs the resulting
    // value's identity to be passable through Service.call.
    def('serviceDeclare', VCtor('__Service', []));
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

    // Service.call : Service req resp -> req -> (Result String resp -> msg) -> Effect e msg
    //   - Encodes `req` as JSON, fetches /services/<module>.<name>, parses
    //     the JSON response into a mar value, dispatches msg(Ok|Err).
    //   - The path is derived from provenance stamped at load time
    //     (svc.originModule + svc.originName).
    def('serviceCall', native(3, ([svc, req, toMsg]) => {
      return VEffect(() => {
        const name = svc.originName || '(anonymous)';
        const mod  = svc.originModule ? svc.originModule + '.' : '';
        const path = '/services/' + mod + name;
        const body = JSON.stringify(marToJs(req));
        fetch(path, {
          method: 'POST',
          body,
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
        })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            // Auth-expiry interceptor: a 401 on a Service.call means
            // the session is gone (or never existed). Send the user to
            // the configured signInPath, capturing where they were so
            // Nav.afterSignIn can return them after a successful login.
            // The Err is NOT dispatched — user code never sees this
            // case. This keeps "session expired" out of every update
            // function across the app.
            if (r.status === 401 && handleAuthExpired()) return;
            if (!r.ok) {
              const msg = apply(toMsg, VCtor('Err', [VString(r.body || ('HTTP ' + r.status))]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            let parsed;
            try {
              parsed = jsToMar(JSON.parse(r.body));
            } catch (e) {
              const msg = apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))]));
              if (currentDispatch) currentDispatch(msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'serviceCall');
    }));
    def('Service.call', envLookup(env, 'serviceCall'));

    // handleAuthExpired centralizes the "401 from Service.call" reaction:
    //
    //   1. If signInPath isn't configured (Auth.config absent), can't
    //      do anything useful — return false so the caller surfaces the
    //      Err to user code instead. Without this guard we'd swallow
    //      the error silently in apps that don't even use Auth.
    //   2. If we already navigated to sign-in for an earlier 401 in
    //      this batch (parallel Service.calls all expiring together),
    //      drop subsequent 401s on the floor — one redirect is enough.
    //      The flag is reset by Nav.afterSignIn after a successful
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

      nav.replace(target);
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
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'authPost');
    }

    function decodeAuthError(body) {
      try {
        const j = JSON.parse(body);
        return (j && j.error) || '';
      } catch (_) {
        return body || '';
      }
    }

    def('authRequestCode', native(2, ([req, toMsg]) => {
      return authPost('/_auth/request-code', marToJs(req), toMsg, () => VUnit());
    }));
    def('Auth.requestCode', envLookup(env, 'authRequestCode'));

    def('authVerifyCode', native(2, ([req, toMsg]) => {
      return authPost('/_auth/verify-code', marToJs(req), toMsg, (text) => jsToMar(JSON.parse(text)));
    }));
    def('Auth.verifyCode', envLookup(env, 'authVerifyCode'));

    def('authLogout', native(1, ([toMsg]) => {
      return authPost('/_auth/logout', null, toMsg, () => VUnit());
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
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
            if (currentDispatch) currentDispatch(msg);
          });
        return VUnit();
      }, 'authMe');
    }));
    def('Auth.me', envLookup(env, 'authMe'));

    def('httpPost', native(3, ([url, body, toMsg]) => {
      return VEffect(() => {
        fetch(url.s, { method: 'POST', body: body.s })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(r.body || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            if (currentDispatch) currentDispatch(msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(String(err))]));
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

  // buildNavigationBar pulls navigationTitle / trailing / leading
  // attrs off a navigationStack view and returns a fresh <header>
  // element. The bar layouts as [leading] [title centered] [trailing].
  // Always returns an element (even when no attrs are set) so
  // patchDOM can keep the slot-0 DOM structure stable.
  function buildNavigationBar(view) {
    const header = document.createElement('header');
    header.className = 'mar-nav-bar';
    const titleAttr = view.attrs && view.attrs.find(a => a.name === 'navigationTitle');
    const trailingAttr = view.attrs && view.attrs.find(a => a.name === 'trailing');
    const leadingAttr  = view.attrs && view.attrs.find(a => a.name === 'leading');
    if (!titleAttr && !trailingAttr && !leadingAttr) {
      header.style.display = 'none';
      return header;
    }
    const left = document.createElement('div');
    left.className = 'mar-nav-side';
    if (leadingAttr) left.appendChild(createDOM(leadingAttr.value));
    header.appendChild(left);

    const center = document.createElement('div');
    center.className = 'mar-nav-title';
    if (titleAttr) center.textContent = titleAttr.value.s;
    header.appendChild(center);

    const right = document.createElement('div');
    right.className = 'mar-nav-side mar-nav-side-trailing';
    if (trailingAttr) right.appendChild(createDOM(trailingAttr.value));
    header.appendChild(right);

    return header;
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
      // Apple-on-the-web vocabulary: SF Pro Display, generous space,
      // pill-shaped buttons, near-black `#1d1d1f` text, Apple-blue
      // (#0071e3) accents, soft 0.5px dividers — same language used
      // on icloud.com / apple.com / music.apple.com. The page reads
      // as content (the notes), not chrome (the form).

      'html, body { margin: 0; padding: 0; height: 100%; }',
      'body {',
      // Apple\'s signature off-white for content surfaces (used on
      // icloud.com, apple.com support pages, etc.). White cards on
      // white page have zero contrast — the card edges disappear,
      // which is what was happening before.
      '  background: #f5f5f7;',
      '  color: #1d1d1f;',
      '  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Display",',
      '    "SF Pro Text", "Helvetica Neue", system-ui, sans-serif;',
      '  font-size: 17px;',
      '  line-height: 1.47;',
      '  -webkit-font-smoothing: antialiased;',
      '  text-rendering: optimizeLegibility;',
      '}',

      // Page container — Apple sites center content within ~980-1140px.
      // Big top padding so the title has breathing room (Apple's hero
      // pattern); the page is content-first, no nav chrome on top.
      '.mar-nav-stack {',
      '  display: block;',
      '  max-width: 1024px;',
      '  width: 100%;',
      '  margin: 0 auto;',
      '  padding: 24px 32px 48px 32px;',
      '  min-height: 100vh;',
      '  box-sizing: border-box;',
      '}',

      // Title bar — large but tight to the content below.
      '.mar-nav-bar {',
      '  display: flex;',
      '  align-items: baseline;',
      '  justify-content: space-between;',
      '  gap: 24px;',
      '  margin-bottom: 20px;',
      '}',
      '.mar-nav-title {',
      '  font-size: 32px;',
      '  font-weight: 700;',
      '  letter-spacing: -0.02em;',
      '  line-height: 1.15;',
      '  flex: 1;',
      '  white-space: nowrap;',
      '  overflow: hidden;',
      '  text-overflow: ellipsis;',
      '}',
      '.mar-nav-side { display: flex; align-items: center; gap: 12px; }',
      '.mar-nav-side-trailing { justify-content: flex-end; }',

      // Pill-shaped secondary buttons — Apple uses 980px radius in
      // production (any number ≥ height/2 produces a pill, but they
      // overshoot for safety). Subtle gray fill, never iOS-bright.
      '.mar-nav-side button {',
      '  background: rgba(0, 0, 0, 0.05);',
      '  border: none;',
      '  color: #1d1d1f;',
      '  font-family: inherit;',
      '  font-size: 14px;',
      '  font-weight: 500;',
      '  padding: 8px 18px;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms;',
      '}',
      '.mar-nav-side button:hover { background: rgba(0, 0, 0, 0.08); }',

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

      // Section: Apple-web eyebrow header + iOS-style content card.
      // Reset UA margin — some browsers give <section> ~1em vertical
      // margin by default, which shows up as a chunky black gap
      // between consecutive section cards on top of our form gap.
      '.mar-section { display: block; margin: 0; padding: 0; }',
      '.mar-section-header {',
      '  font-size: 12px; font-weight: 600;',
      '  text-transform: uppercase; letter-spacing: 0.8px;',
      '  color: #86868b;',
      '  padding: 0 4px; margin: 0 0 4px 0;',
      '}',
      '.mar-section-body {',
      '  background: #ffffff;',
      '  border-radius: 12px;',
      '  overflow: hidden;',
      '  box-shadow: 0 0 0 0.5px rgba(0, 0, 0, 0.06);',
      '}',
      '.mar-section-body > * {',
      '  display: block;',
      '  padding: 10px 16px;',
      '  border-bottom: 0.5px solid rgba(0, 0, 0, 0.08);',
      '  font-size: 16px;',
      '}',
      '.mar-section-body > *:last-child { border-bottom: none; }',
      '.mar-section-footer {',
      '  font-size: 13px; color: #86868b;',
      '  padding: 0 4px; margin: 6px 0 0 0;',
      '}',

      // Stacks
      '.mar-hstack {',
      '  display: flex; flex-direction: row; align-items: center;',
      '  gap: 12px;',
      '}',
      '.mar-hstack > * { flex: 1; min-width: 0; }',
      '.mar-hstack > button { flex: 0 0 auto; }',
      '.mar-vstack {',
      '  display: flex; flex-direction: column; align-items: stretch;',
      '  gap: 12px;',
      '}',

      // Inputs — soft fill, big, prominent. Focus shows the Apple
      // blue ring (matches the `Sign in` / `Verify` flow on iCloud).
      // Input — always has visible web-input chrome (border, fill,
      // rounded, Apple-blue focus ring). What changes with context is
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
      // Inside a section card directly — horizontal margin so the
      // input has breathing room from the card edges.
      '.mar-section-body > .mar-textfield {',
      '  margin: 8px 16px;',
      '  width: auto;',
      '}',
      // Inside an hstack, fill the available row, no margin.
      '.mar-hstack > .mar-textfield {',
      '  flex: 1; min-width: 0;',
      '  margin: 0;',
      '}',

      // Section-row buttons (full-width tappy actions): Apple-blue
      // text, no fill — matches Apple\'s "Continue with Apple" /
      // "Sign in" link styling.
      // Buttons — always Apple-blue pill, regardless of context.
      // What changes is sizing/margin: full-width inside a section
      // card (the primary "Verify" / "Send" CTA pattern), compact
      // inline when in an hstack (next to an input).
      '.mar-section-body > button, .mar-hstack > button {',
      '  background: #0071e3; border: none; color: white;',
      '  font-weight: 500;',
      '  border-radius: 980px;',
      '  cursor: pointer; font-family: inherit;',
      '  transition: background 200ms;',
      '}',
      '.mar-section-body > button:hover, .mar-hstack > button:hover {',
      '  background: #0077ed;',
      '}',
      // Section-card button — full row action (sign-in form CTA).
      '.mar-section-body > button {',
      '  display: block;',
      '  margin: 8px 16px;',
      '  padding: 10px 20px;',
      '  font-size: 15px;',
      '  width: auto;',
      '  text-align: center;',
      '}',
      // Inline pill (next to an input).
      '.mar-hstack > button {',
      '  flex: 0 0 auto;',
      '  padding: 6px 16px;',
      '  font-size: 14px;',
      '}',

      // hstack inside section card — already gets row padding from
      // the section-body > * rule. No extra needed.

      // Title / subtitle — heading text. Typography matches Apple-
      // web hierarchy. Scoped via class to avoid clobbering h1/h2
      // used elsewhere (e.g. .mar-section-header is also <h2>).
      '.mar-title {',
      '  font-size: 24px; font-weight: 700; letter-spacing: -0.015em;',
      '  margin: 0; line-height: 1.2;',
      '}',
      '.mar-subtitle {',
      '  font-size: 17px; font-weight: 400; color: #86868b;',
      '  margin: 0; line-height: 1.35;',
      '}',
      // Inline links — Apple-blue, no underline (Apple convention).
      'a.mar-link {',
      '  color: #0071e3; text-decoration: none; cursor: pointer;',
      '}',
      'a.mar-link:hover { text-decoration: underline; }',

      // Centered — fills viewport and centers child both axes.
      // Used for full-screen Loading / EmptyState / Error views.
      '.mar-centered {',
      '  display: flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  min-height: 60vh;',
      '  width: 100%;',
      '  text-align: center;',
      '}',

      // Dark mode — Apple-graphite (`#1d1d1f`), not pure black.
      // Tracks Apple\'s actual dark theme on apple.com / Music.
      '@media (prefers-color-scheme: dark) {',
      '  body { background: #1d1d1f; color: #f5f5f7; }',
      '  .mar-nav-bar { border-bottom: none; }',
      '  .mar-nav-side button {',
      '    background: rgba(255, 255, 255, 0.1); color: #f5f5f7;',
      '  }',
      '  .mar-nav-side button:hover { background: rgba(255, 255, 255, 0.15); }',
      '  .mar-section-body {',
      '    background: #2c2c2e;',
      '    box-shadow: 0 0 0 0.5px rgba(255, 255, 255, 0.06);',
      '  }',
      '  .mar-section-body > * {',
      '    border-bottom-color: rgba(255, 255, 255, 0.08);',
      '  }',
      '  .mar-section-body > *:last-child { border-bottom: none; }',
      '  .mar-section-header, .mar-section-footer { color: #86868b; }',
      '  .mar-textfield::placeholder { color: #86868b; }',
      '  .mar-textfield {',
      '    border-color: rgba(255, 255, 255, 0.12);',
      '    background: rgba(255, 255, 255, 0.04);',
      '  }',
      '  .mar-textfield:focus {',
      '    background: rgba(255, 255, 255, 0.06);',
      '    border-color: rgba(10, 132, 255, 0.6);',
      '    box-shadow: 0 0 0 4px rgba(10, 132, 255, 0.18);',
      '  }',
      // Buttons in dark mode keep the blue-pill look (white text on
      // blue), just slightly brighter blue to stay legible against
      // the darker background.
      '  .mar-section-body > button, .mar-hstack > button {',
      '    background: #0a84ff; color: white;',
      '  }',
      '  .mar-section-body > button:hover, .mar-hstack > button:hover {',
      '    background: #2997ff;',
      '  }',
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
      case 'button':          return 'button';
      case 'link':            return 'a';
      case 'navigationStack': return 'main';
      case 'form':            return 'form';
      case 'uiList':          return 'ul';
      case 'uiSection':       return 'section';
      case 'hstack':          return 'div';
      case 'vstack':          return 'div';
      case 'textField':       return 'input';
      default:                return 'div';
    }
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
    const tag = domTagFor(view.tag);
    const e = document.createElement(tag);
    setMarView(e, view);
    switch (view.tag) {
      case 'text':
        e.textContent = view.text;
        break;
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
      case 'button':
        e.textContent = view.text;
        applyDisabledAttr(e, view);
        attachClickDispatcher(e);
        break;
      case 'link':
        ensureUIStyles();
        e.className = 'mar-link';
        e.setAttribute('href', getAttr(view, 'href'));
        e.textContent = view.text;
        // Intercept clicks: if the href maps to a known mar page,
        // SPA-route via history.pushState instead of full reload.
        // (Existing delegated link interceptor in mountPages handles
        // this generically via `[href^="/"]` selector.)
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
        //   <header.mar-nav-bar>     ← always present; hidden if no attrs
        //   <div.mar-nav-body>       ← stable wrapper, children diffed
        // </main>
        ensureUIStyles();
        e.className = 'mar-nav-stack';
        e.appendChild(buildNavigationBar(view));
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
        // wrapping (semantic noise without payoff for sectioned lists).
        ensureUIStyles();
        e.className = 'mar-list';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'uiSection': {
        // <section>
        //   <h2.mar-section-header>  ← always present; hidden if empty
        //   <div.mar-section-body>   ← stable wrapper, children diffed
        //   <p.mar-section-footer>   ← always present; hidden if empty
        // </section>
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
        for (const c of view.children) body.appendChild(createDOM(c));
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
        const ph = getAttr(view, 'placeholder');
        if (ph) e.setAttribute('placeholder', ph);
        e.value = view.text;
        attachInputDispatcher(e);
        attachSubmitDispatcher(e);
        break;
      }
      case 'centered':
        // Flex container that fills available space + centers child.
        // Used for full-screen states (Loading, EmptyState, etc.).
        ensureUIStyles();
        e.className = 'mar-centered';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      default:
        for (const c of view.children) e.appendChild(createDOM(c));
    }
    return e;
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

    switch (newView.tag) {
      case 'text':
      case 'title':
      case 'subtitle':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      case 'button':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        applyDisabledAttr(node, newView);
        break;
      case 'link': {
        const newHref = getAttr(newView, 'href');
        if (node.getAttribute('href') !== newHref) node.setAttribute('href', newHref);
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      }
      case 'textField':
        // Only write if the value diverges — avoids resetting the cursor
        // mid-keystroke when the model just echoes what the user typed.
        if (node.value !== newView.text) node.value = newView.text;
        break;
      case 'empty':
        break;
      // navigationStack / uiSection patch in place: the surrounding
      // chrome (nav bar, section header/footer) gets re-rendered, but
      // the body wrapper stays the same DOM node so any focused input
      // inside keeps focus + cursor + selection.
      case 'navigationStack': {
        // Slot 0: nav bar. Cheap to recreate (no interactive focus
        // state on title/buttons that matters for typing).
        const oldBar = node.firstChild;
        const newBar = buildNavigationBar(newView);
        node.replaceChild(newBar, oldBar);
        // Slot 1: body wrapper. Diff its children — that's where
        // form/list/etc live, possibly with a focused input below.
        const body = node.querySelector(':scope > .mar-nav-body');
        if (body) {
          patchChildrenPositional(body, oldView.children, newView.children, 'navigationStack');
        }
        break;
      }
      case 'uiSection': {
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
          patchChildrenPositional(body, oldView.children, newView.children, 'uiSection');
        }
        break;
      }
      case 'uiList':
        // No wrapper structure — children are direct. Plain positional
        // diff handles it.
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
  function patchChildrenPositional(parentNode, oldChildren, newChildren, _parentTag) {
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

  // mountPages mounts a list of pages with URL-based routing. A
  // single-page app is just a list of one page (path "/"). Each page
  // has its own model — selected by window.location.pathname.
  // Falls back to the first page when the URL doesn't match any path.
  function mountPages(pageList) {
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

    // Dynamic-page model preservation key. Static pages share state
    // across renders by `path`; dynamic pages share by URL — switching
    // from /notes/abc to /notes/xyz is a navigation, not a re-render,
    // so each URL gets its own model slot.
    function preservationKey(pg, urlPath) {
      return pg.isDynamic ? urlPath : pg.path;
    }

    function buildPageEntry(path, initFn, updateFn, viewFn, title, isProtected, isDynamic) {
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
        const initial = unwrapModelTuple(apply(initFnApplied, VUnit()));
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
      // All four ctors share the same arg shape: [path, init, update,
      // view, title]. __ProtectedPage / __DynamicProtectedPage trigger
      // the auth gate (redirect resolved from globalThis.__marAuthSignInPath,
      // set by the Auth.config builtin). __DynamicPage / __DynamicProtectedPage
      // also enable URL-pattern matching with `:param` segments.
      const [pathV, initFn, updateFn, viewFn, titleV] = p.args;
      const path = pathV.s;
      const title = (titleV && titleV.k === 'S') ? titleV.s : '';
      const isProtected = (p.tag === '__ProtectedPage' || p.tag === '__DynamicProtectedPage');
      const isDynamic   = (p.tag === '__DynamicPage'   || p.tag === '__DynamicProtectedPage');
      if (firstPath === null) firstPath = path;
      const entry = buildPageEntry(path, initFn, updateFn, viewFn, title, isProtected, isDynamic);
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
          // see) and route to the sign-in page.
          history.replaceState({}, '', signInPath);
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
      const viewVal = viewWithUser(pg, pg.model);
      const root = document.getElementById('mar-root');
      // On page change, throw away the old DOM — diffing across a
      // navigation gives no useful work. Use activeKey (URL-based for
      // dynamic pages) so /notes/abc → /notes/xyz also resets the DOM.
      if (mounted == null || mountedPath !== pg.activeKey) {
        while (root.firstChild) root.removeChild(root.firstChild);
        mounted = createDOM(viewVal);
        root.appendChild(mounted);
        mountedPath = pg.activeKey;
      } else {
        mounted = patchDOM(mounted, viewVal);
      }

      // Intercept link clicks pointing at known page paths so navigation
      // stays in-process (no full page reload). One delegated listener on
      // the root catches clicks from any descendant anchor — installed
      // once, so we don't have to re-scan the DOM on every render or
      // tag each anchor with a flag.
      if (!routerInstalled) {
        routerInstalled = true;
        root.addEventListener('click', (ev) => {
          const a = ev.target.closest && ev.target.closest('a[href^="/"]');
          if (!a) return;
          const href = a.getAttribute('href');
          if (matchesAnyPage(href)) {
            ev.preventDefault();
            history.pushState({}, '', href);
            render();
          }
        });
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

    // Expose programmatic navigation so Nav.push / Nav.replace can
    // reach into this closure. Last call wins — successive mountPages
    // (e.g. hot-reload) replace the table.
    globalThis.__marNav = {
      push: (path) => {
        history.pushState({}, '', path);
        render();
      },
      replace: (path) => {
        history.replaceState({}, '', path);
        // Auth state may have changed (login/logout drives most replace
        // calls). Invalidate the cache so the next protected page
        // re-fetches /_auth/whoami.
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
      slider.style.flex = '1';
      slider.style.minWidth = '120px';

      const status = document.createElement('span');
      status.textContent = (cursor + 1) + ' / ' + total;
      status.style.color = '#9ca3af';
      status.style.minWidth = '50px';
      status.style.textAlign = 'right';

      slider.oninput = () => {
        const idx = parseInt(slider.value, 10);
        status.textContent = (idx + 1) + ' / ' + total;
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

    window.addEventListener('popstate', render);
    render();
    return VUnit();
  }

  // ---------- Module loader ----------

  function loadModule(env, mod) {
    // Pass 0: process `import M exposing (...)` so bare names bind to
    // already-known qualified values. Mirrors the typechecker; without
    // this, code that typechecks (e.g. `column [...]` after
    // `import View exposing (column)`) explodes at runtime with
    // "unbound name: column".
    if (mod.imports) {
      for (const imp of mod.imports) {
        if (!imp.exposing || imp.exposing.length === 0) continue;
        const modName = (imp.module || []).join('.');
        for (const item of imp.exposing) {
          const qualified = modName + '.' + item.name;
          const v = envLookup(env, qualified);
          if (v !== undefined) {
            envDefine(env, item.name, v);
          }
        }
      }
    }
    const modName = (mod.name || []).join('.');
    // Pass 1: register custom-type constructors. Bare for intra-
    // module use; qualified so other modules can reference them too
    // (matters when a module exports a custom type and another
    // module pattern-matches on its constructors via `import M
    // exposing (T(..))`).
    for (const d of mod.decls) {
      if (d.kind === 'CustomTypeDecl') {
        const ctorNames = [];
        let allZeroArg = true;
        for (const c of d.constructors) {
          const arity = c.argCount;
          const ctor = arity === 0
            ? VCtor(c.name)
            : native(arity, args => VCtor(c.name, args));
          envDefine(env, c.name, ctor);
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
    // Pass 2: pre-bind value names with placeholders.
    for (const d of mod.decls) {
      if (d.kind === 'ValueDecl') {
        envDefine(env, d.name, VUnit());
        if (modName) envDefine(env, modName + '.' + d.name, VUnit());
      }
    }
    // Pass 3: evaluate. Each value is registered both bare (for
    // intra-module references during evaluation; safe because mar
    // forbids name collisions within a module) and qualified
    // (`Module.name`) so EQualified lookups from other modules work.
    // Without the qualified alias, two modules that both define
    // `page` would silently overwrite each other in the bare slot.
    for (const d of mod.decls) {
      if (d.kind !== 'ValueDecl') continue;
      let body = d.body;
      if (d.params && d.params.length > 0) {
        body = { kind: 'ELambda', params: d.params, body };
      }
      let val = evalExpr(body, env);
      // Stamp Service values with their binding's name + module so
      // Service.call can derive the URL. Mirrors how the Go runtime
      // attaches OriginModule/OriginName via the project loader.
      if (val && val.k === 'C' && val.tag === '__Service' && !val.originName) {
        val = Object.assign({}, val, { originModule: modName, originName: d.name });
      }
      envDefine(env, d.name, val);
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
    for (const mod of modules) {
      loadModule(env, mod);
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
  global.marBootstrap = function () {
    fetch('/_mar/program.json', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
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

    es.onopen = function () {
      clearDisconnectTimer();
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
        '.mar-dock-badge.pulse { animation: mar-dock-pulse 1.2s ease-in-out infinite; }';
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
      case 'Fn': return '<fn>';
      case 'V': return '<view>';
      default: return '<?>';
    }
  };
  } // end if (__MAR_DEV__)
})(typeof window !== 'undefined' ? window : globalThis);
