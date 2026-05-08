// Admin panel client — vanilla JS, no build step. Renders one of:
//
//   login           → email entry → code entry → cookie set
//   overview        → server / db / requests sections
//   entityBrowser   → paginated rows for a clicked entity
//
// State is held in a single object; the render function rebuilds
// the DOM from it. Same MVU pattern Mar pages use; we just hand-
// rolled it in JS so the framework binary stays a single file.

(function () {
  'use strict';

  const root = document.getElementById('root');

  // model — single source of truth, bumped via setState({...})
  let state = {
    view: 'loading',          // 'login' | 'overview' | 'entity' | 'loading'
    loginStage: 'email',      // 'email' | 'code' | 'sending' | 'verifying'
    loginEmail: '',
    loginError: '',
    loginInfo: '',
    session: null,            // { email } once authenticated
    server: null,
    db: null,
    requests: null,
    backups: null,            // { items: [{id, sizeBytes, createdAtMs}, ...] } | { error }
    browsing: null,           // { entity, rows, cursor, error }
    restoreState: null,       // null | 'pending' | 'staged' | 'failed'
    restoreMessage: '',
    error: '',
  };

  function setState(patch) {
    state = Object.assign({}, state, patch);
    render();
  }

  // -- HTTP helpers --

  async function postJSON(url, body) {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(body || {}),
    });
    let parsed = null;
    try { parsed = await resp.json(); } catch { /* non-JSON body */ }
    return { ok: resp.ok, status: resp.status, body: parsed };
  }

  async function getJSON(url) {
    const resp = await fetch(url, { credentials: 'include' });
    let parsed = null;
    try { parsed = await resp.json(); } catch { /* non-JSON body */ }
    return { ok: resp.ok, status: resp.status, body: parsed };
  }

  // -- Boot --

  async function boot() {
    // /whoami always 200s — body is the session record when signed
    // in, null otherwise. Same shape as /_auth/whoami on the user-
    // auth side.
    const r = await getJSON('/_mar/admin/api/whoami');
    if (r.ok && r.body && r.body.email) {
      setState({ view: 'overview', session: r.body });
      loadOverview();
    } else {
      setState({ view: 'login', loginStage: 'email' });
    }
  }

  // -- Login flow --

  async function submitEmail(email) {
    setState({ loginStage: 'sending', loginError: '' });
    const r = await postJSON('/_mar/admin/auth/request-code', { email });
    if (r.ok) {
      setState({
        loginStage: 'code',
        loginEmail: email,
        loginInfo: 'If your email is on the admin list, a code is on the way.',
      });
    } else if (r.status === 429) {
      setState({ loginStage: 'email', loginError: 'Too many attempts. Try again later.' });
    } else {
      setState({ loginStage: 'email', loginError: 'Something went wrong. Try again.' });
    }
  }

  async function submitCode(code) {
    setState({ loginStage: 'verifying', loginError: '' });
    const r = await postJSON('/_mar/admin/auth/verify-code', {
      email: state.loginEmail, code,
    });
    if (r.ok) {
      setState({ view: 'overview', session: r.body, loginEmail: '', loginInfo: '' });
      loadOverview();
    } else if (r.status === 401 && r.body && r.body.error === 'too_many_attempts') {
      setState({ loginStage: 'email', loginError: 'Too many wrong codes. Request a new one.' });
    } else {
      setState({ loginStage: 'code', loginError: 'Invalid code. Try again.' });
    }
  }

  async function logout() {
    await postJSON('/_mar/admin/auth/logout');
    setState({ view: 'login', loginStage: 'email', session: null });
  }

  // -- Overview load --

  async function loadOverview() {
    const [s, d, r, b] = await Promise.all([
      getJSON('/_mar/admin/api/server-info'),
      getJSON('/_mar/admin/api/db-stats'),
      getJSON('/_mar/admin/api/recent-requests'),
      getJSON('/_mar/admin/api/database-backups'),
    ]);
    setState({
      server:   s.ok ? s.body : { error: 'unavailable' },
      db:       d.ok ? d.body : { error: 'unavailable' },
      requests: r.ok ? r.body : { error: 'unavailable' },
      backups:  b.ok ? b.body : { error: 'unavailable' },
    });
  }

  async function restoreBackup(id) {
    const confirmed = window.confirm(
      'Restore this backup? The current database will be moved to a ' +
      '.bak file and the server will restart automatically.\n\n' +
      'Any data written between now and the restart will be lost.'
    );
    if (!confirmed) return;

    setState({ restoreState: 'pending', restoreMessage: 'Applying restore…' });
    const r = await fetch('/_mar/admin/api/database-backup/' + encodeURIComponent(id) + '/restore', {
      method: 'POST',
      credentials: 'include',
    });
    let body = null;
    try { body = await r.json(); } catch { /* */ }

    if (r.status === 409 && body && body.error === 'schema_mismatch') {
      setState({
        restoreState: 'failed',
        restoreMessage:
          'Schema mismatch — this backup was taken against a different schema ' +
          '(likely a migration ran since). Restore manually by deploying the ' +
          'matching binary first.',
      });
      return;
    }
    if (!r.ok) {
      setState({
        restoreState: 'failed',
        restoreMessage: 'Restore failed (' + r.status + '). See server logs.',
      });
      return;
    }

    setState({
      restoreState: 'staged',
      restoreMessage:
        'Restore staged. The server is restarting — this page will reload when it is back.',
    });
    pollUntilUp();
  }

  // pollUntilUp pings /whoami every 1.5s for up to 60s after a
  // restore. Once it gets a 200 (server is back), reloads the page
  // so the operator sees the restored state.
  async function pollUntilUp() {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1500));
      try {
        const r = await fetch('/_mar/admin/api/whoami', { credentials: 'include' });
        if (r.ok) {
          window.location.reload();
          return;
        }
      } catch (_) { /* still down */ }
    }
    setState({
      restoreState: 'failed',
      restoreMessage: 'Server did not come back within 60s. Check fly machine status.',
    });
  }

  async function downloadBackup(id) {
    // Direct browser download — let the browser handle the file save
    // dialog via Content-Disposition: attachment.
    window.location.href = '/_mar/admin/api/database-backup/' + encodeURIComponent(id);
  }

  async function browseEntity(name, cursor) {
    setState({ browsing: { entity: name, rows: null, cursor: cursor || null, error: '' } });
    const url = '/_mar/admin/api/entity-rows?entity=' + encodeURIComponent(name)
      + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');
    const r = await getJSON(url);
    if (r.ok) {
      setState({ browsing: { entity: name, rows: r.body, cursor: cursor || null, error: '' } });
    } else {
      setState({ browsing: { entity: name, rows: null, cursor: cursor || null, error: 'failed to load' } });
    }
  }

  // -- DOM helpers --

  function el(tag, props, ...children) {
    const e = document.createElement(tag);
    if (props) {
      for (const k in props) {
        const v = props[k];
        if (k === 'class') { e.className = v; }
        else if (k === 'onclick') { e.addEventListener('click', v); }
        else if (k === 'onsubmit') { e.addEventListener('submit', (ev) => { ev.preventDefault(); v(ev); }); }
        else if (k.startsWith('on') && typeof v === 'function') { e.addEventListener(k.slice(2), v); }
        else if (k === 'value') { e.value = v; }
        // Boolean attributes — assign as IDL property, NOT
        // setAttribute. setAttribute('disabled', false) still
        // disables the element because HTML treats presence-of-
        // attribute as truthy regardless of the value string.
        else if (k === 'disabled' || k === 'autofocus' || k === 'required') {
          e[k] = !!v;
        }
        else if (k === 'type') { e.type = v; }
        // For everything else: skip when v is false/null/undefined
        // (so explicitly passed `something: false` doesn't end up
        // as setAttribute('something', 'false')), otherwise stringify.
        else if (v === false || v == null) { /* skip */ }
        else { e.setAttribute(k, v); }
      }
    }
    for (const c of children) {
      if (c == null || c === false) continue;
      if (typeof c === 'string' || typeof c === 'number') {
        e.appendChild(document.createTextNode(String(c)));
      } else if (Array.isArray(c)) {
        for (const cc of c) {
          if (cc) e.appendChild(cc);
        }
      } else {
        e.appendChild(c);
      }
    }
    return e;
  }

  // -- Render --

  function render() {
    root.innerHTML = '';
    if (state.view === 'loading') {
      root.appendChild(el('div', { class: 'loading' }, 'Loading…'));
      return;
    }
    if (state.view === 'login') { root.appendChild(renderLogin()); return; }
    if (state.view === 'overview') { root.appendChild(renderOverview()); return; }
  }

  function renderLogin() {
    const stage = state.loginStage;
    let formInner;
    if (stage === 'email' || stage === 'sending') {
      formInner = [
        el('label', { for: 'email' }, 'Email'),
        el('input', {
          id: 'email', type: 'email', autofocus: true, required: true,
          placeholder: 'you@example.com',
          value: state.loginEmail || '',
        }),
        el('button', { type: 'submit', disabled: stage === 'sending' },
          stage === 'sending' ? 'Sending…' : 'Send code'),
      ];
    } else {
      formInner = [
        el('p', null, 'A 6-digit code was just sent to ' + state.loginEmail + '.'),
        el('label', { for: 'code' }, 'Code'),
        el('input', {
          id: 'code', type: 'text', autofocus: true, required: true,
          placeholder: '123456', autocomplete: 'one-time-code',
          inputmode: 'numeric', maxlength: '6',
        }),
        el('button', { type: 'submit', disabled: stage === 'verifying' },
          stage === 'verifying' ? 'Verifying…' : 'Sign in'),
        el('button', {
          type: 'button', class: 'secondary',
          onclick: () => setState({ loginStage: 'email', loginError: '' }),
        }, 'Use a different email'),
      ];
    }
    const form = el('form', {
      onsubmit: () => {
        if (stage === 'email') {
          const v = document.getElementById('email').value.trim();
          if (v) submitEmail(v);
        } else if (stage === 'code') {
          const v = document.getElementById('code').value.trim();
          if (v) submitCode(v);
        }
      },
    }, ...formInner);
    return el('div', { class: 'login' },
      el('div', { class: 'card' },
        el('h1', null, 'mar admin'),
        state.loginError ? el('div', { class: 'banner' }, state.loginError) : null,
        state.loginInfo  ? el('p', null, state.loginInfo) : null,
        form,
        el('div', { class: 'hint' }, 'Sign in with the email registered in mar.json[“admins”].'),
      ),
    );
  }

  function renderOverview() {
    const sections = [];
    sections.push(renderTopbar());

    if (state.browsing) {
      sections.push(renderEntityBrowser());
      return el('div', null, ...sections);
    }

    sections.push(renderServerSection());
    sections.push(renderDbSection());
    sections.push(renderBackupsSection());
    sections.push(renderRequestsSection());
    return el('div', null, ...sections);
  }

  function renderBackupsSection() {
    if (!state.backups) return el('div', { class: 'section' });
    if (state.backups.error) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Database backups'),
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' }, 'unavailable')));
    }
    const items = state.backups.items || [];
    const banner = state.restoreState ? renderRestoreBanner() : null;
    if (items.length === 0) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Database backups'),
        banner,
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' },
            'No backups yet. The first auto-backup runs after the configured interval.')));
    }
    const rows = items.map((b) => el('div', { class: 'row' },
      el('div', null,
        el('div', { class: 'label' }, b.id),
        el('div', { class: 'value', style: 'font-size: 11px;' },
          formatBytes(b.sizeBytes) + ' • ' + formatRelativeTime(b.createdAtMs)),
      ),
      el('div', null,
        el('button', {
          class: 'secondary',
          onclick: () => restoreBackup(b.id),
          disabled: state.restoreState === 'pending' || state.restoreState === 'staged',
        }, 'Restore'),
        el('button', {
          class: 'secondary',
          onclick: () => downloadBackup(b.id),
        }, 'Download'),
      ),
    ));
    return el('div', { class: 'section' },
      el('div', { class: 'section-header' }, 'Database backups (' + items.length + ')'),
      banner,
      el('div', { class: 'section-body' }, ...rows),
    );
  }

  function renderRestoreBanner() {
    if (!state.restoreState) return null;
    const cls = state.restoreState === 'failed' ? 'banner' : 'banner banner-info';
    return el('div', { class: cls, style: 'margin: 8px 12px;' },
      state.restoreMessage);
  }

  function renderTopbar() {
    return el('div', { class: 'topbar' },
      el('div', null,
        el('h1', null, 'Admin'),
        state.session ? el('div', { class: 'meta' }, state.session.email) : null,
      ),
      el('div', null,
        el('button', { class: 'secondary', onclick: loadOverview }, 'Refresh'),
        el('button', { class: 'secondary', onclick: logout }, 'Sign out'),
      ),
    );
  }

  function renderServerSection() {
    if (!state.server) return el('div', { class: 'section' });
    if (state.server.error) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Server'),
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' }, 'unavailable')));
    }
    const s = state.server;
    return el('div', { class: 'section' },
      el('div', { class: 'section-header' }, 'Server'),
      el('div', { class: 'section-body' },
        kvRow('mar version', s.marVersion || '—'),
        kvRow('go version', s.goVersion || '—'),
        kvRow('build target', s.buildTarget || '—'),
        kvRow('booted', formatTime(s.bootedAtMs)),
        kvRow('uptime', formatDuration(Date.now() - s.bootedAtMs)),
        kvRow('requests total', String(s.requestsTotal || 0)),
        kvRow('in flight', String(s.requestsInFlight || 0)),
      ),
    );
  }

  function renderDbSection() {
    if (!state.db) return el('div', { class: 'section' });
    if (state.db.error) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Database'),
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' }, 'unavailable')));
    }
    const d = state.db;

    // Three groupings, top to bottom:
    //   - Database         file sizes only (the on-disk footprint)
    //   - Tables           user-defined entities (the business model)
    //   - Framework tables _mar_-prefixed (auth + admin + migrations)
    //
    // Splitting "stats vs tables" lets the eye land on the file size
    // immediately without scanning past it to count rows; splitting
    // "user tables vs framework tables" keeps framework noise out of
    // the operator's own model.

    const sections = [];

    sections.push(
      el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Database'),
        el('div', { class: 'section-body' },
          kvRow('mar.db', formatBytes(d.dbSizeBytes)),
          kvRow('WAL', formatBytes(d.walSizeBytes)),
        ),
      )
    );

    const businessRows = (d.entities || []).map((e) =>
      clickableRow(e.name, e.rowCount + ' rows', () => browseEntity(e.name))
    );
    sections.push(
      el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Tables'),
        el('div', { class: 'section-body' },
          ...(businessRows.length > 0
            ? businessRows
            : [el('div', { class: 'empty' }, 'no tables')]),
        ),
      )
    );

    // Framework section only renders when there's something to show.
    // Row browser still works on these via the same listEntityRows
    // endpoint — useful for poking at admin sessions / migration
    // history during debugging.
    const frameworkRows = (d.frameworkTables || []).map((e) =>
      clickableRow(e.name, e.rowCount + ' rows', () => browseEntity(e.name))
    );
    if (frameworkRows.length > 0) {
      sections.push(
        el('div', { class: 'section' },
          el('div', { class: 'section-header' }, 'Framework tables'),
          el('div', { class: 'section-body' }, ...frameworkRows),
        )
      );
    }
    return el('div', null, ...sections);
  }

  function renderRequestsSection() {
    if (!state.requests) return el('div', { class: 'section' });
    if (state.requests.error) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Recent requests'),
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' }, 'unavailable')));
    }
    const reqs = state.requests.items || state.requests || [];
    if (reqs.length === 0) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, 'Recent requests'),
        el('div', { class: 'section-body' },
          el('div', { class: 'empty' }, 'no requests captured')));
    }
    const header = el('tr', null,
      el('th', null, 'Time'),
      el('th', null, 'Method'),
      el('th', null, 'Path'),
      el('th', null, 'Status'),
      el('th', null, 'ms'),
      el('th', null, 'User'),
    );
    const body = reqs.map((r) => el('tr', null,
      el('td', null, formatRelativeTime(r.atMs)),
      el('td', null, r.method),
      el('td', { class: 'cell-overflow' }, r.path),
      el('td', null, statusPill(r.status)),
      el('td', null, String(r.durationMs)),
      el('td', null, r.userEmail || ''),
    ));
    return el('div', { class: 'section' },
      el('div', { class: 'section-header' }, 'Recent requests (' + reqs.length + ')'),
      el('div', { class: 'section-body' },
        el('table', null, header, ...body)),
    );
  }

  function renderEntityBrowser() {
    const b = state.browsing;
    const back = el('button', {
      class: 'secondary',
      onclick: () => { setState({ browsing: null }); loadOverview(); },
    }, '← back');

    if (b.error) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, b.entity),
        back,
        el('div', { class: 'section-body' }, el('div', { class: 'empty' }, b.error)),
      );
    }
    if (!b.rows) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, b.entity),
        back,
        el('div', { class: 'section-body' }, el('div', { class: 'loading' }, 'Loading…')));
    }
    const items = b.rows.items || [];
    if (items.length === 0) {
      return el('div', { class: 'section' },
        el('div', { class: 'section-header' }, b.entity),
        back,
        el('div', { class: 'section-body' }, el('div', { class: 'empty' }, 'no rows')));
    }
    const columns = b.rows.columns || Object.keys(items[0]);
    const headerRow = el('tr', null, ...columns.map((c) => el('th', null, c)));
    const bodyRows = items.map((row) =>
      el('tr', null, ...columns.map((c) => {
        const v = row[c];
        const text = (v === null || v === undefined) ? '' : (typeof v === 'object' ? JSON.stringify(v) : String(v));
        return el('td', { class: 'cell-overflow' }, text);
      }))
    );
    return el('div', { class: 'section' },
      el('div', { class: 'section-header' }, b.entity + ' (' + items.length + ')'),
      back,
      el('div', { class: 'section-body' },
        el('table', null, headerRow, ...bodyRows),
        b.rows.nextCursor ? el('div', { class: 'row clickable', onclick: () => browseEntity(b.entity, b.rows.nextCursor) },
          el('span', { class: 'label' }, 'Load more'),
          el('span', { class: 'value' }, '→')) : null,
      ),
    );
  }

  // -- Tiny render helpers --

  function kvRow(label, value) {
    return el('div', { class: 'row' },
      el('span', { class: 'label' }, label),
      el('span', { class: 'value' }, value),
    );
  }
  function clickableRow(label, value, onclick) {
    return el('div', { class: 'row clickable', onclick },
      el('span', { class: 'label' }, label),
      el('span', { class: 'value' }, value + ' →'),
    );
  }
  function statusPill(status) {
    const klass = status >= 500 ? 's5xx' : status >= 400 ? 's4xx' : status >= 300 ? 's3xx' : 's2xx';
    return el('span', { class: 'status ' + klass }, String(status));
  }
  function formatBytes(n) {
    if (n == null) return '—';
    const units = ['B', 'KB', 'MB', 'GB'];
    let i = 0; let v = n;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(v < 10 && i > 0 ? 1 : 0) + ' ' + units[i];
  }
  function formatTime(ms) {
    if (!ms) return '—';
    return new Date(ms).toLocaleString();
  }
  function formatRelativeTime(ms) {
    if (!ms) return '—';
    const d = (Date.now() - ms) / 1000;
    if (d < 1) return 'now';
    if (d < 60) return Math.floor(d) + 's ago';
    if (d < 3600) return Math.floor(d / 60) + 'm ago';
    if (d < 86400) return Math.floor(d / 3600) + 'h ago';
    return new Date(ms).toLocaleDateString();
  }
  function formatDuration(ms) {
    if (!ms || ms < 0) return '—';
    const s = Math.floor(ms / 1000);
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    if (s < 86400) return Math.floor(s / 3600) + 'h ' + Math.floor((s % 3600) / 60) + 'm';
    return Math.floor(s / 86400) + 'd ' + Math.floor((s % 86400) / 3600) + 'h';
  }

  // Boot.
  boot();
})();
