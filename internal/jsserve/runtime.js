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
  const VView   = (tag, attrs, children, text) =>
    ({ k: 'V', tag, attrs: attrs || [], children: children || [], text: text || '' });
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

    // View constructors
    def('viewSection', native(1, ([l]) => VView('section', [], l.xs, '')));
    def('View.section', native(1, ([l]) => VView('section', [], l.xs, '')));
    def('viewRow',     native(1, ([l]) => VView('row', [], l.xs, '')));
    def('View.row',     native(1, ([l]) => VView('row', [], l.xs, '')));
    def('viewColumn',  native(1, ([l]) => VView('column', [], l.xs, '')));
    def('View.column',  native(1, ([l]) => VView('column', [], l.xs, '')));
    def('viewText',    native(1, ([s]) => VView('text', [], [], s.s)));
    def('View.text',    native(1, ([s]) => VView('text', [], [], s.s)));
    def('viewTitle',   native(1, ([s]) => VView('title', [], [], s.s)));
    def('View.title',   native(1, ([s]) => VView('title', [], [], s.s)));
    def('viewSubtitle',native(1, ([s]) => VView('subtitle', [], [], s.s)));
    def('View.subtitle',native(1, ([s]) => VView('subtitle', [], [], s.s)));
    def('viewButton',  native(1, ([s]) => VView('button', [], [], s.s)));
    def('View.button',  native(1, ([s]) => VView('button', [], [], s.s)));
    def('viewLink',    native(2, ([href, label]) => VView('link', [{name:'href', value: href}], [], label.s)));
    def('View.link',    native(2, ([href, label]) => VView('link', [{name:'href', value: href}], [], label.s)));
    def('viewList',    native(1, ([l]) => VView('list', [], l.xs, '')));
    def('View.list',    native(1, ([l]) => VView('list', [], l.xs, '')));
    def('viewEmpty', VView('empty', [], [], ''));
    def('View.empty', VView('empty', [], [], ''));
    def('viewInput', native(2, ([n, v]) => VView('input', [{name:'name', value: n}], [], v.s)));
    def('View.input', native(2, ([n, v]) => VView('input', [{name:'name', value: n}], [], v.s)));
    def('viewTextarea', native(2, ([n, v]) => VView('textarea', [{name:'name', value: n}], [], v.s)));
    def('View.textarea', native(2, ([n, v]) => VView('textarea', [{name:'name', value: n}], [], v.s)));
    def('viewForm', native(2, ([msgName, children]) => VView('form', [], children.xs, msgName.s)));
    def('View.form', native(2, ([msgName, children]) => VView('form', [], children.xs, msgName.s)));

    // App.create / App.serve — App.serve mounts the MVU loop.
    def('appCreate', native(3, ([init, update, view]) => VCtor('__App', [init, update, view])));
    def('App.create', native(3, ([init, update, view]) => VCtor('__App', [init, update, view])));
    def('appServe', native(2, ([port, app]) => {
      return VEffect(() => mountApp(app), 'mountApp');
    }));
    def('App.serve', native(2, ([port, app]) => {
      return VEffect(() => mountApp(app), 'mountApp');
    }));

    // Screen.create + App.serveScreens — multi-screen apps with browser routing.
    def('screenCreate', native(4, ([path, init, update, view]) =>
      VCtor('__Screen', [path, init, update, view])
    ));
    def('Screen.create', native(4, ([path, init, update, view]) =>
      VCtor('__Screen', [path, init, update, view])
    ));
    def('appServeScreens', native(2, ([port, list]) => {
      return VEffect(() => mountScreens(list.xs), 'mountScreens');
    }));
    def('App.serveScreens', native(2, ([port, list]) => {
      return VEffect(() => mountScreens(list.xs), 'mountScreens');
    }));

    // Effect — sync versions (effects are run-on-demand thunks).
    def('effectSucceed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('Effect.succeed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('effectMap', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('Effect.map', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('effectAndThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('Effect.andThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('effectNone', VEffect(() => VUnit(), 'none'));
    def('Effect.none', VEffect(() => VUnit(), 'none'));

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

  function buildDOM(view) {
    if (!view || view.k !== 'V') {
      // Not a view (could be unit if user returned ()) — return empty span.
      const e = document.createElement('span');
      return e;
    }
    switch (view.tag) {
      case 'text': {
        const e = document.createElement('span');
        e.textContent = view.text;
        return e;
      }
      case 'title': {
        const e = document.createElement('h1');
        e.textContent = view.text;
        return e;
      }
      case 'subtitle': {
        const e = document.createElement('h2');
        e.textContent = view.text;
        return e;
      }
      case 'button': {
        const e = document.createElement('button');
        e.textContent = view.text;
        e.addEventListener('click', (ev) => {
          ev.preventDefault();
          if (currentDispatch) currentDispatch(VCtor(view.text, []));
        });
        return e;
      }
      case 'form': {
        // A View.form msgName children -> wraps inputs; on submit, builds
        // a record of name -> input-value and dispatches VCtor(msgName, [record]).
        const e = document.createElement('form');
        // Walk children, build DOM nodes, and remember inputs by name.
        const inputs = [];
        const collectInputs = (node) => {
          if (!node) return;
          if (node.k !== 'V') return;
          if (node.tag === 'input' || node.tag === 'textarea') {
            const name = (node.attrs.find(a => a.name === 'name') || {value: VString('')}).value.s;
            inputs.push({ name, view: node });
          }
          for (const c of node.children) collectInputs(c);
        };
        for (const c of view.children) collectInputs(c);
        for (const c of view.children) e.appendChild(buildDOM(c));
        const submit = document.createElement('button');
        submit.type = 'submit';
        submit.textContent = 'submit';
        e.appendChild(submit);
        e.addEventListener('submit', (ev) => {
          ev.preventDefault();
          if (!currentDispatch) return;
          // Read current values from the rendered DOM by name.
          const fields = {};
          const order = [];
          const list = e.querySelectorAll('input, textarea');
          for (const el of list) {
            if (el.name) {
              fields[el.name] = VString(el.value || '');
              order.push(el.name);
            }
          }
          const recordArg = VRecord(fields, order);
          currentDispatch(VCtor(view.text, [recordArg]));
        });
        return e;
      }
      case 'link': {
        const e = document.createElement('a');
        const href = (view.attrs.find(a => a.name === 'href') || {value: VString('')}).value.s;
        e.setAttribute('href', href);
        e.textContent = view.text;
        return e;
      }
      case 'section': {
        const e = document.createElement('section');
        for (const c of view.children) e.appendChild(buildDOM(c));
        return e;
      }
      case 'row': {
        const e = document.createElement('div');
        e.className = 'row';
        e.style.display = 'flex';
        e.style.gap = '0.5rem';
        for (const c of view.children) e.appendChild(buildDOM(c));
        return e;
      }
      case 'column': {
        const e = document.createElement('div');
        e.className = 'column';
        e.style.display = 'flex';
        e.style.flexDirection = 'column';
        e.style.gap = '0.5rem';
        for (const c of view.children) e.appendChild(buildDOM(c));
        return e;
      }
      case 'list': {
        const e = document.createElement('ul');
        for (const c of view.children) {
          const li = document.createElement('li');
          li.appendChild(buildDOM(c));
          e.appendChild(li);
        }
        return e;
      }
      case 'input': {
        const e = document.createElement('input');
        e.type = 'text';
        const name = (view.attrs.find(a => a.name === 'name') || {value: VString('')}).value.s;
        e.name = name;
        e.value = view.text;
        return e;
      }
      case 'textarea': {
        const e = document.createElement('textarea');
        const name = (view.attrs.find(a => a.name === 'name') || {value: VString('')}).value.s;
        e.name = name;
        e.value = view.text;
        return e;
      }
      case 'empty':
        return document.createDocumentFragment();
      default: {
        const e = document.createElement('div');
        for (const c of view.children) e.appendChild(buildDOM(c));
        return e;
      }
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
      const initial = unwrapModelTuple(apply(initFn, VUnit()));
      screens[path] = {
        path,
        model: initial.model,
        initEffect: initial.effect,
        update: updateFn,
        view: viewFn,
      };
    }

    let initEffectsRun = {};

    function currentScreen() {
      const p = window.location.pathname;
      return screens[p] || screens[Object.keys(screens)[0]];
    }

    function render() {
      const sc = currentScreen();
      if (!sc) return;
      if (!initEffectsRun[sc.path]) {
        initEffectsRun[sc.path] = true;
        runEffect(sc.initEffect);
      }
      const viewVal = apply(sc.view, sc.model);
      const root = document.getElementById('mar-root');
      while (root.firstChild) root.removeChild(root.firstChild);
      root.appendChild(buildDOM(viewVal));

      // Intercept link clicks for client-side navigation.
      root.querySelectorAll('a[href^="/"]').forEach((a) => {
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
    const initial = unwrapModelTuple(apply(initFn, VUnit()));
    let model = initial.model;

    function render() {
      const viewVal = apply(viewFn, model);
      const root = document.getElementById('mar-root');
      while (root.firstChild) root.removeChild(root.firstChild);
      root.appendChild(buildDOM(viewVal));
    }

    currentDispatch = (msg) => {
      const out = unwrapModelTuple(apply(apply(updateFn, msg), model));
      model = out.model;
      render();
      runEffect(out.effect);
    };
    render();
    runEffect(initial.effect);
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
    } else {
      throw new Error('entry value is not an Effect (got ' + main.k + ')');
    }
  };
})(typeof window !== 'undefined' ? window : globalThis);
