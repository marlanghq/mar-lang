// mar JS runtime. Interprets a mar AST in the browser and runs an MVU loop.
//
// Loaded into a page along with the program AST and the entry point. The
// runtime evaluates the program until it produces a special "mountApp"
// effect, then takes over and runs the init/update/view loop with real DOM.

(function (global) {
  'use strict';

  // ---------- Value constructors ----------

  const VInt    = (n)        => ({ k: 'I', n });
  const VFloat  = (n)        => ({ k: 'F', n });
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
        // qualified or bare constructor
        const key = e.module && e.module.length > 0
          ? e.module.join('.') + '.' + e.name
          : e.name;
        let v = envLookup(env, key);
        if (v === undefined) v = envLookup(env, e.name);
        if (v === undefined) throw new Error('unbound constructor: ' + key);
        return v;
      }
      case 'EQualified': {
        const key = e.module.join('.') + '.' + e.name;
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
        const paramNames = e.params.map(p => {
          if (p.kind === 'PVar') return p.name;
          if (p.kind === 'PWildcard') return '__wild';
          throw new Error('lambda params must be names or _');
        });
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
            for (const k of Object.keys(bindings)) frame.bindings[k] = bindings[k];
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
            for (const k of Object.keys(bindings)) frame.bindings[k] = bindings[k];
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

    // String stdlib
    def('stringFromInt', native(1, ([n]) => VString(String(n.n))));
    def('String.fromInt', native(1, ([n]) => VString(String(n.n))));
    def('stringLength', native(1, ([s]) => VInt(s.s.length)));
    def('String.length', native(1, ([s]) => VInt(s.s.length)));

    // List stdlib (subset for counter — extend as needed)
    def('listLength', native(1, ([l]) => VInt(l.xs.length)));
    def('List.length', native(1, ([l]) => VInt(l.xs.length)));
    def('listMap', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    def('List.map', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    def('listSum', native(1, ([l]) => VInt(l.xs.reduce((a, x) => a + x.n, 0))));
    def('List.sum', native(1, ([l]) => VInt(l.xs.reduce((a, x) => a + x.n, 0))));
    def('listFilter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));
    def('List.filter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));

    // Maybe
    def('maybeWithDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));
    def('Maybe.withDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));

    // View — elm-ui-style. Every constructor takes a List Attr first;
    // modifiers below produce Attr values consumed by that list. Each
    // attr is a VRecord { name, value } the constructors copy into
    // VView.attrs as { name, value } pairs.
    function collectAttrs(attrsList) {
      const out = [];
      for (const a of attrsList.xs) {
        out.push({ name: a.fields.name.s, value: a.fields.value });
      }
      return out;
    }
    function container(tag) {
      return native(2, ([attrsList, children]) =>
        VView(tag, collectAttrs(attrsList), children.xs, ''));
    }
    function leafText(tag) {
      return native(2, ([attrsList, s]) =>
        VView(tag, collectAttrs(attrsList), [], s.s));
    }
    def('viewSection',  container('section'));  def('View.section',  container('section'));
    def('viewRow',      container('row'));      def('View.row',      container('row'));
    def('viewColumn',   container('column'));   def('View.column',   container('column'));
    def('viewList',     container('list'));     def('View.list',     container('list'));
    def('viewText',     leafText('text'));      def('View.text',     leafText('text'));
    def('viewTitle',    leafText('title'));     def('View.title',    leafText('title'));
    def('viewSubtitle', leafText('subtitle'));  def('View.subtitle', leafText('subtitle'));

    // View.button : List Attr -> msg -> String -> View msg
    function makeButton([attrsList, msg, label]) {
      return VView('button', collectAttrs(attrsList), [], label.s, msg);
    }
    def('viewButton', native(3, makeButton));
    def('View.button', native(3, makeButton));

    // View.link : List Attr -> String -> String -> View msg   (href, label)
    function makeLink([attrsList, href, label]) {
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'href', value: href });
      return VView('link', attrs, [], label.s);
    }
    def('viewLink', native(3, makeLink));
    def('View.link', native(3, makeLink));

    // View.keyedList : List Attr -> List (String, View msg) -> View msg
    function makeKeyedList([attrsList, items]) {
      const children = items.xs.map((t) => {
        const key = t.xs[0].s;
        const view = t.xs[1];
        return Object.assign({}, view, { key });
      });
      return VView('keyedList', collectAttrs(attrsList), children, '');
    }
    def('viewKeyedList', native(2, makeKeyedList));
    def('View.keyedList', native(2, makeKeyedList));

    // View.input / textarea : List Attr -> String -> (String -> msg) -> View msg
    function inputCtor(tag) {
      return native(3, ([attrsList, value, onChange]) =>
        VView(tag, collectAttrs(attrsList), [], value.s, onChange));
    }
    def('viewInput',    inputCtor('input'));    def('View.input',    inputCtor('input'));
    def('viewTextarea', inputCtor('textarea')); def('View.textarea', inputCtor('textarea'));

    def('viewEmpty', VView('empty', [], [], ''));
    def('View.empty', VView('empty', [], [], ''));

    // Layout modifiers — produce Attr values (VRecord { name, value })
    // that the constructors above consume from their attrs list.
    function makeAttr(name, value) {
      return VRecord({ name: VString(name), value }, ['name', 'value']);
    }
    function intAttr(name) {
      return native(1, ([n]) => makeAttr(name, VInt(n.n)));
    }
    function flagAttr(name) {
      return makeAttr(name, VUnit());
    }
    def('viewPadding',       intAttr('padding'));       def('View.padding',       intAttr('padding'));
    def('viewPaddingTop',    intAttr('paddingTop'));    def('View.paddingTop',    intAttr('paddingTop'));
    def('viewPaddingRight',  intAttr('paddingRight'));  def('View.paddingRight',  intAttr('paddingRight'));
    def('viewPaddingBottom', intAttr('paddingBottom')); def('View.paddingBottom', intAttr('paddingBottom'));
    def('viewPaddingLeft',   intAttr('paddingLeft'));   def('View.paddingLeft',   intAttr('paddingLeft'));
    def('viewSpacing', intAttr('spacing'));   def('View.spacing', intAttr('spacing'));
    def('viewWidth',   intAttr('width'));     def('View.width',   intAttr('width'));
    def('viewHeight',  intAttr('height'));    def('View.height',  intAttr('height'));
    def('viewFillX',   flagAttr('fillX'));    def('View.fillX',   flagAttr('fillX'));
    def('viewFillY',   flagAttr('fillY'));    def('View.fillY',   flagAttr('fillY'));
    def('viewFill',    flagAttr('fill'));     def('View.fill',    flagAttr('fill'));
    def('viewCenterX', flagAttr('centerX'));  def('View.centerX', flagAttr('centerX'));
    def('viewCenterY', flagAttr('centerY'));  def('View.centerY', flagAttr('centerY'));
    def('viewCenter',  flagAttr('center'));   def('View.center',  flagAttr('center'));

    // App.create / App.serve — App.serve mounts the MVU loop.
    def('appCreate', native(3, ([init, update, view]) => VCtor('__App', [init, update, view])));
    def('App.create', native(3, ([init, update, view]) => VCtor('__App', [init, update, view])));
    // App.serve : App -> Effect String ()
    // Port is configured by the host server (mar.json), not by code.
    def('appServe', native(1, ([app]) => VEffect(() => mountApp(app), 'mountApp')));
    def('App.serve', native(1, ([app]) => VEffect(() => mountApp(app), 'mountApp')));

    // Screen.create + App.serveScreens — multi-screen apps with browser routing.
    def('screenCreate', native(4, ([path, init, update, view]) =>
      VCtor('__Screen', [path, init, update, view])
    ));
    def('Screen.create', native(4, ([path, init, update, view]) =>
      VCtor('__Screen', [path, init, update, view])
    ));
    // App.serveScreens : List Screen -> Effect String ()
    def('appServeScreens', native(1, ([list]) => VEffect(() => mountScreens(list.xs), 'mountScreens')));
    def('App.serveScreens', native(1, ([list]) => VEffect(() => mountScreens(list.xs), 'mountScreens')));

    // Effect — sync versions (effects are run-on-demand thunks).
    def('effectSucceed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('Effect.succeed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('effectMap', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('Effect.map', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('effectAndThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('Effect.andThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('effectNone', VEffect(() => VUnit(), 'none'));
    def('Effect.none', VEffect(() => VUnit(), 'none'));

    // JSON.decode : String -> Result String α (type-trusted; the compiler
    // accepts whatever shape the caller declared). Encodes plain JS values
    // into mar values: numbers -> VInt, strings -> VString, booleans -> VBool,
    // arrays -> VList, objects -> VRecord, null -> VCtor 'Nothing'.
    function jsToMar(v) {
      if (v === null || v === undefined) return VCtor('Nothing');
      if (typeof v === 'number') return VInt(v | 0);
      if (typeof v === 'string') return VString(v);
      if (typeof v === 'boolean') return VBool(v);
      if (Array.isArray(v)) return VList(v.map(jsToMar));
      if (typeof v === 'object') {
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
    def('jsonDecode', native(1, ([raw]) => {
      try {
        const parsed = JSON.parse(raw.s);
        return VCtor('Ok', [jsToMar(parsed)]);
      } catch (e) {
        return VCtor('Err', [VString(String(e && e.message || e))]);
      }
    }));
    def('JSON.decode', envLookup(env, 'jsonDecode'));

    // JSON.encode : α -> String (also type-trusted on the user side).
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
          if (v.tag === 'Just') return marToJs(v.args[0]);
          if (v.tag === 'Nothing') return null;
          if (v.tag === 'Ok') return marToJs(v.args[0]);
          if (v.tag === 'Err') return { error: marToJs(v.args[0]) };
          return { tag: v.tag, args: v.args.map(marToJs) };
        default: return null;
      }
    }
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
  // latest view after subsequent patches.
  function attachClickDispatcher(node) {
    node.addEventListener('click', (ev) => {
      ev.preventDefault();
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return;
      currentDispatch(v.msg);
    });
  }

  function attachInputDispatcher(node) {
    node.addEventListener('input', (ev) => {
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return;
      currentDispatch(apply(v.msg, VString(node.value)));
    });
  }

  function getAttr(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a ? a.value.s : '';
  }

  // domTagFor returns the HTML element name for a VView tag, or null when
  // the view has no single corresponding element (empty / unknown).
  function domTagFor(viewTag) {
    switch (viewTag) {
      case 'text':       return 'span';
      case 'title':      return 'h1';
      case 'subtitle':   return 'h2';
      case 'button':     return 'button';
      case 'link':       return 'a';
      case 'section':    return 'section';
      case 'row':        return 'div';
      case 'column':     return 'div';
      case 'list':       return 'ul';
      case 'keyedList':  return 'ul';
      case 'input':      return 'input';
      case 'textarea':   return 'textarea';
      default:           return 'div';
    }
  }

  // applyLayoutAttrs reads the layout modifiers attached to a view
  // (padding, spacing, fill, center, ...) and translates them to CSS on
  // the DOM node. Each modifier maps to a small set of style props that
  // work whether the parent is flex or block. Future native runtimes
  // read the same attrs and call .padding() / Modifier.padding() etc.
  function applyLayoutAttrs(node, view) {
    if (!view.attrs || view.attrs.length === 0) return;
    for (const a of view.attrs) {
      const v = a.value;
      switch (a.name) {
        case 'padding':
          node.style.padding = v.n + 'px';
          break;
        case 'paddingTop':
          node.style.paddingTop = v.n + 'px';
          break;
        case 'paddingRight':
          node.style.paddingRight = v.n + 'px';
          break;
        case 'paddingBottom':
          node.style.paddingBottom = v.n + 'px';
          break;
        case 'paddingLeft':
          node.style.paddingLeft = v.n + 'px';
          break;
        case 'spacing':
          node.style.gap = v.n + 'px';
          break;
        case 'width':
          node.style.width = v.n + 'px';
          break;
        case 'height':
          node.style.height = v.n + 'px';
          break;
        case 'fillX':
          // 'flex: 1' covers main-axis fill in a row; 'align-self: stretch'
          // and 'width: 100%' cover cross-axis fill in a column. Both are
          // harmless when the other applies.
          node.style.alignSelf = 'stretch';
          node.style.flexGrow = '1';
          node.style.width = '100%';
          break;
        case 'fillY':
          node.style.alignSelf = 'stretch';
          node.style.flexGrow = '1';
          node.style.height = '100%';
          break;
        case 'fill':
          node.style.alignSelf = 'stretch';
          node.style.flexGrow = '1';
          node.style.width = '100%';
          node.style.height = '100%';
          break;
        case 'centerX':
          // Margin-auto works in both block AND flex contexts to center
          // along the main / cross axis. align-self: center also covers
          // the flex-cross case.
          node.style.alignSelf = 'center';
          node.style.marginLeft = 'auto';
          node.style.marginRight = 'auto';
          break;
        case 'centerY':
          node.style.alignSelf = 'center';
          node.style.marginTop = 'auto';
          node.style.marginBottom = 'auto';
          break;
        case 'center':
          node.style.alignSelf = 'center';
          node.style.margin = 'auto';
          break;
        // attrs not in the layout vocabulary (href, name, __key) are
        // handled elsewhere — skip them here.
      }
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
      case 'title':
      case 'subtitle':
        e.textContent = view.text;
        break;
      case 'button':
        e.textContent = view.text;
        attachClickDispatcher(e);
        break;
      case 'link':
        e.setAttribute('href', getAttr(view, 'href'));
        e.textContent = view.text;
        break;
      case 'section':
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'row':
        // elm-ui-like default: children are content-sized (shrink), not
        // stretched. align-items: flex-start prevents the flex default
        // 'stretch' from forcing children to match the row's cross-axis
        // height.
        e.className = 'row';
        e.style.display = 'flex';
        e.style.alignItems = 'flex-start';
        e.style.gap = '0.5rem';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'column':
        // Default: children are content-sized. Stretching is opt-in via
        // the layout modifiers (View.fillX / View.fill).
        e.className = 'column';
        e.style.display = 'flex';
        e.style.flexDirection = 'column';
        e.style.alignItems = 'flex-start';
        e.style.gap = '0.5rem';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'list':
      case 'keyedList':
        for (const c of view.children) {
          const li = document.createElement('li');
          li.appendChild(createDOM(c));
          e.appendChild(li);
        }
        break;
      case 'input':
        e.type = 'text';
        e.value = view.text;
        attachInputDispatcher(e);
        break;
      case 'textarea':
        e.value = view.text;
        attachInputDispatcher(e);
        break;
      default:
        for (const c of view.children) e.appendChild(createDOM(c));
    }
    // Layout modifiers apply on top of the type-specific styling. Done
    // last so View.fill can override align-items: flex-start on a row,
    // View.padding can stack with intrinsic button padding, etc.
    applyLayoutAttrs(e, view);
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
      case 'button':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      case 'link': {
        const newHref = getAttr(newView, 'href');
        if (node.getAttribute('href') !== newHref) node.setAttribute('href', newHref);
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      }
      case 'input':
      case 'textarea':
        // Only write if the value diverges — avoids resetting the cursor
        // mid-keystroke when the model just echoes what the user typed.
        if (node.value !== newView.text) node.value = newView.text;
        break;
      case 'empty':
        break;
      case 'keyedList':
        patchChildrenKeyed(node, oldView.children, newView.children);
        break;
      case 'list':
      case 'section':
      case 'row':
      case 'column':
      default:
        patchChildrenPositional(node, oldView.children, newView.children, newView.tag);
    }
    // Re-apply layout attrs in case modifiers changed between renders
    // (e.g. View.fill toggled by state). Cheap — just sets a few CSS
    // properties.
    applyLayoutAttrs(node, newView);
    return node;
  }

  // patchChildrenPositional walks DOM children and VView children in
  // lockstep. Same-tag pairs are patched in place; new entries are
  // appended; removed entries are detached. Wrapping for `list` is the
  // <li> layer: each child sits inside its own <li>.
  function patchChildrenPositional(parentNode, oldChildren, newChildren, parentTag) {
    const wrapInLi = parentTag === 'list';
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
        if (wrapInLi) {
          const li = document.createElement('li');
          li.appendChild(createDOM(newChild));
          parentNode.appendChild(li);
        } else {
          parentNode.appendChild(createDOM(newChild));
        }
        continue;
      }
      // Existing — patch in place.
      const targetNode = wrapInLi ? domChild.firstChild : domChild;
      patchDOM(targetNode, newChild);
    }
  }

  // patchChildrenKeyed implements a two-pass keyed diff: build a key->oldDOM
  // map from existing children, then produce new children from the map
  // (patching) or fresh nodes for unseen keys, in the new order. Removed
  // keys are detached at the end.
  function patchChildrenKeyed(parentNode, oldChildren, newChildren) {
    // oldChildren is parallel to parentNode.childNodes (each wrapped in <li>).
    const oldByKey = new Map();
    for (let i = 0; i < oldChildren.length; i++) {
      const li = parentNode.childNodes[i];
      const oc = oldChildren[i];
      // Each keyed child stores its key on the inner view (set by
      // viewKeyedList builtin); fall back to position string if missing.
      const key = oc && oc.key != null ? oc.key : ('@' + i);
      oldByKey.set(key, li);
    }
    const usedLis = new Set();
    let prevSibling = null;
    for (let i = 0; i < newChildren.length; i++) {
      const nc = newChildren[i];
      const key = nc && nc.key != null ? nc.key : ('@' + i);
      let li = oldByKey.get(key);
      if (li) {
        // Patch the existing <li>'s child against the new view.
        patchDOM(li.firstChild, nc);
        usedLis.add(li);
        // Move into position (insertBefore is a no-op when already there).
        const desiredNext = prevSibling ? prevSibling.nextSibling : parentNode.firstChild;
        if (desiredNext !== li) parentNode.insertBefore(li, desiredNext);
      } else {
        // New key — fresh <li>.
        li = document.createElement('li');
        li.appendChild(createDOM(nc));
        const desiredNext = prevSibling ? prevSibling.nextSibling : parentNode.firstChild;
        parentNode.insertBefore(li, desiredNext);
      }
      prevSibling = li;
    }
    // Remove any old <li> not adopted by a new key.
    for (const [, li] of oldByKey) {
      if (!usedLis.has(li) && li.parentNode === parentNode) parentNode.removeChild(li);
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

  // mountScreens implements multi-screen client-side routing using
  // window.location.pathname + popstate to switch between screens. Each
  // screen has its own model.
  function mountScreens(screenList) {
    const screens = {};
    for (const s of screenList) {
      if (s.k !== 'C' || s.tag !== '__Screen') continue;
      const [pathV, initFn, updateFn, viewFn] = s.args;
      const path = pathV.s;

      // Per-screen hot-reload model preservation. preservedScreenModels
      // is keyed by screen path and survives marReload via the IIFE's
      // lexical scope. If the new view function rejects the old model
      // (shape mismatch), fall back to fresh init for that screen.
      let model = null;
      let initEffect = null;
      const prior = preservedScreenModels[path];
      if (prior !== undefined) {
        try {
          apply(viewFn, prior);
          model = prior;
        } catch (_) {
          if (typeof console !== 'undefined') {
            console.info('[mar reload] screen ' + path + ' model shape changed, fresh init');
          }
        }
      }
      if (model === null) {
        const initial = unwrapModelTuple(apply(initFn, VUnit()));
        model = initial.model;
        initEffect = initial.effect;
      }
      preservedScreenModels[path] = model;

      screens[path] = {
        path,
        model,
        initEffect, // null when we restored — already-run effects don't need re-firing
        update: updateFn,
        view: viewFn,
      };
    }

    let initEffectsRun = {};

    function currentScreen() {
      const p = window.location.pathname;
      return screens[p] || screens[Object.keys(screens)[0]];
    }

    let mounted = null;
    let mountedScreenPath = null;

    function render() {
      const sc = currentScreen();
      if (!sc) return;
      if (!initEffectsRun[sc.path] && sc.initEffect !== null) {
        initEffectsRun[sc.path] = true;
        runEffect(sc.initEffect);
      }
      const viewVal = apply(sc.view, sc.model);
      const root = document.getElementById('mar-root');
      // On screen change, throw away the old tree so patch starts fresh —
      // diffing across a navigation gives no useful work.
      if (mounted == null || mountedScreenPath !== sc.path) {
        while (root.firstChild) root.removeChild(root.firstChild);
        mounted = createDOM(viewVal);
        root.appendChild(mounted);
        mountedScreenPath = sc.path;
      } else {
        mounted = patchDOM(mounted, viewVal);
      }

      // Intercept link clicks for client-side navigation. Re-binding on
      // every render is OK — addEventListener with a fresh fn is cheap and
      // querySelectorAll only finds anchors that were just created or
      // patched.
      root.querySelectorAll('a[href^="/"]').forEach((a) => {
        if (a.__marRouted) return;
        a.__marRouted = true;
        a.addEventListener('click', (ev) => {
          const href = a.getAttribute('href');
          if (screens[href]) {
            ev.preventDefault();
            history.pushState({}, '', href);
            render();
          }
        });
      });
    }

    currentDispatch = (msg) => {
      const sc = currentScreen();
      if (!sc) return;
      const out = unwrapModelTuple(apply(apply(sc.update, msg), sc.model));
      sc.model = out.model;
      preservedScreenModels[sc.path] = out.model;
      render();
      runEffect(out.effect);
    };

    window.addEventListener('popstate', render);
    render();
    return VUnit();
  }

  function mountApp(app) {
    if (app.k !== 'C' || app.tag !== '__App') {
      throw new Error('App.serve: expected an App value');
    }
    const [initFn, updateFn, viewFn] = app.args;

    // Hot-reload model preservation: if a previous run of this app left a
    // model behind on the module-level `preservedModel`, try to keep
    // using it. Smoke-test by calling viewFn against it; if the new view
    // function blows up (e.g. user added a field to Model), discard it
    // and start fresh from init. The IIFE wrapping this whole file
    // persists across marRun re-invocations, so `preservedModel` survives
    // a marReload.
    let model = null;
    let initialEffect = null;
    if (preservedModel !== null) {
      try {
        apply(viewFn, preservedModel); // smoke test
        model = preservedModel;
      } catch (_) {
        // Shape changed — fall through to fresh init.
        if (typeof console !== 'undefined') {
          console.info('[mar reload] model shape changed, falling back to fresh init');
        }
      }
    }
    if (model === null) {
      const initial = unwrapModelTuple(apply(initFn, VUnit()));
      model = initial.model;
      initialEffect = initial.effect;
    }
    preservedModel = model;
    let mounted = null;

    function render() {
      const viewVal = apply(viewFn, model);
      const root = document.getElementById('mar-root');
      if (mounted == null) {
        while (root.firstChild) root.removeChild(root.firstChild);
        mounted = createDOM(viewVal);
        root.appendChild(mounted);
      } else {
        // Patch in place. patchDOM may swap out the node if the root tag
        // changes, so update our reference.
        mounted = patchDOM(mounted, viewVal);
      }
    }

    currentDispatch = (msg) => {
      const out = unwrapModelTuple(apply(apply(updateFn, msg), model));
      model = out.model;
      preservedModel = model;
      render();
      runEffect(out.effect);
    };
    render();
    if (initialEffect !== null) runEffect(initialEffect);
    return VUnit();
  }

  // ---------- Module loader ----------

  function loadModule(env, mod) {
    // Pass 1: register custom-type constructors.
    for (const d of mod.decls) {
      if (d.kind === 'CustomTypeDecl') {
        for (const c of d.constructors) {
          const arity = c.argCount;
          if (arity === 0) {
            envDefine(env, c.name, VCtor(c.name));
          } else {
            envDefine(env, c.name, native(arity, args => VCtor(c.name, args)));
          }
        }
      }
    }
    // Pass 2: pre-bind value names with placeholders.
    for (const d of mod.decls) {
      if (d.kind === 'ValueDecl') envDefine(env, d.name, VUnit());
    }
    // Pass 3: evaluate.
    for (const d of mod.decls) {
      if (d.kind !== 'ValueDecl') continue;
      let body = d.body;
      if (d.params && d.params.length > 0) {
        body = { kind: 'ELambda', params: d.params, body };
      }
      envDefine(env, d.name, evalExpr(body, env));
    }
  }

  // ---------- Public entry ----------

  global.marRun = function (program) {
    const env = makeBuiltinEnv();
    loadModule(env, program.module);
    const main = envLookup(env, program.entry || 'main');
    if (main === undefined) {
      throw new Error('entry not found: ' + (program.entry || 'main'));
    }
    if (main.k === 'E') {
      main.run();
    } else if (main.k === 'C' && main.tag === '__App') {
      // Bare App value (e.g. Frontend.page used by App.fullstack on the
      // backend) — mount it directly without an enclosing Effect.
      mountApp(main);
    } else {
      throw new Error('entry value is not an Effect or App (got ' + main.k + ')');
    }
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

  // marBootstrap is the entry point invoked from the host HTML page. It
  // fetches the initial program, runs it, and opens the SSE connection
  // that the dev server uses to push reload + compile-error events.
  global.marBootstrap = function () {
    fetch('/_mar/program.json', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (p) {
        try { global.marRun(p); }
        catch (e) {
          console.error(e);
          const root = document.getElementById('mar-root');
          if (root) {
            const pre = document.createElement('pre');
            pre.style.color = '#b00';
            pre.textContent = String(e && e.message || e);
            root.appendChild(pre);
          }
        }
        setupDevChannel();
      });
  };

  // setupDevChannel opens the SSE connection used by `mar dev` for both
  // hot reload and dev-time UI feedback (compile errors, server-down
  // detection). Skipped when EventSource isn't available — the app
  // still runs, just without dev affordances.
  function setupDevChannel() {
    if (typeof EventSource === 'undefined') return;

    const banner = createDevBanner();
    let disconnectTimer = null;

    const showDisconnect = () => {
      banner.show('disconnected', 'Server offline. Reconnecting…');
    };
    const clearDisconnectTimer = () => {
      if (disconnectTimer) { clearTimeout(disconnectTimer); disconnectTimer = null; }
    };

    const es = new EventSource('/_mar/reload');

    es.onopen = function () {
      clearDisconnectTimer();
      // The server resends current error state on subscribe (if any).
      // If there's no error to report, nothing arrives here — clear
      // any leftover "disconnect" banner.
      if (banner.kind === 'disconnected') banner.hide();
    };

    es.onmessage = function (ev) {
      let payload;
      try { payload = JSON.parse(ev.data); } catch (_) { return; }
      if (payload.type === 'reload') {
        banner.hide();
        global.marReload();
      } else if (payload.type === 'ok') {
        banner.hide();
      } else if (payload.type === 'error') {
        banner.show('error', payload.message);
      }
    };

    es.onerror = function () {
      // EventSource fires onerror on every drop. We only show the
      // banner if reconnection takes more than ~1s — short blips
      // (server restart inside hot-reload, network hiccup) shouldn't
      // flash a banner.
      clearDisconnectTimer();
      disconnectTimer = setTimeout(function () {
        if (es.readyState !== 1 /* OPEN */) showDisconnect();
      }, 1000);
    };
  }

  // createDevBanner injects a fixed top-of-viewport bar that the dev
  // channel uses to surface compile errors and server-down state. Pure
  // DOM, no framework dependencies.
  function createDevBanner() {
    let el = document.getElementById('mar-dev-banner');
    if (!el) {
      el = document.createElement('div');
      el.id = 'mar-dev-banner';
      el.style.position = 'fixed';
      el.style.top = '0';
      el.style.left = '0';
      el.style.right = '0';
      el.style.zIndex = '9999';
      el.style.padding = '0.75rem 1rem';
      el.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, monospace';
      el.style.fontSize = '13px';
      el.style.lineHeight = '1.4';
      el.style.whiteSpace = 'pre-wrap';
      el.style.maxHeight = '40vh';
      el.style.overflow = 'auto';
      el.style.boxShadow = '0 2px 4px rgba(0,0,0,0.15)';
      el.style.display = 'none';
      document.body.appendChild(el);
    }
    return {
      kind: null,
      show: function (kind, message) {
        this.kind = kind;
        if (kind === 'error') {
          el.style.background = '#7f1d1d';
          el.style.color = '#fee2e2';
          el.style.borderBottom = '2px solid #fca5a5';
        } else if (kind === 'disconnected') {
          el.style.background = '#78350f';
          el.style.color = '#fef3c7';
          el.style.borderBottom = '2px solid #fcd34d';
        }
        el.textContent = message;
        el.style.display = 'block';
      },
      hide: function () {
        this.kind = null;
        el.style.display = 'none';
      },
    };
  }
})(typeof window !== 'undefined' ? window : globalThis);
