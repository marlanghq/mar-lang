package jsserve

// The boot failure screen: what the browser shows when the app could not
// start, instead of "Loading…" forever.
//
// It covers a program.json that answers 500, comes back cut short, or never
// arrives, and a runtime.js that fails to download or does not parse. The
// phone's counterpart is FailedView in ContentView.swift: the same message,
// and a Retry that runs the load again.
//
// It cannot live in runtime.js. The failure it most has to cover is runtime.js
// never running, and nothing in runtime.js can draw a screen about that. So it
// is a small block of CSS and ES5 that every shell inlines in its <head>,
// BEFORE anything that can fail: the `mar dev` and `mar-runtime` shell
// (pageHTML), the admin panel shell (the same template), and the static build
// (productionPageHTML in internal/scaffold). One source, so the two web shells
// cannot drift apart.
//
// What it defines:
//
//	window.__marBootFailed(err)  draws the screen into #mar-root. The first
//	                             failure wins; a later one while it is still
//	                             up is only logged. Drawn on real failure only,
//	                             never on a timer, so a slow network keeps the
//	                             loading placeholder and never flashes this.
//	window.__marWhenRuntime(name, src, run)
//	                             runs `run` once `name` (marBootstrap or marRun)
//	                             exists. If runtime.js did not define it, one
//	                             fresh fetch of `src` with a cache-busting query
//	                             before giving up, in both shells.
//
// Lifecycle, matched to the phone: a failure becomes a screen that says so and
// offers to try again. The chrome is the platform's: Retry re-runs loadAll in
// place on iOS; here Reload reloads the page, which is the only retry that also
// covers a runtime.js that never ran. It matters most for an app installed to
// the home screen, which has no browser reload button at all.
//
// The copy follows the user-facing rules: one calm sentence, no jargon, no
// em-dashes. The technical cause goes to the console always, and on screen only
// in `mar dev`, where the person looking is the one who can fix it.

// BootFailureHead returns the <style> and <script> that define the boot
// failure screen. dev adds the technical detail under the message.
func BootFailureHead(dev bool) string {
	flag := "false"
	if dev {
		flag = "true"
	}
	return bootFailureCSS + "<script>\n(function (dev) {\n" + bootFailureJS + "})(" + flag + ");\n</script>\n"
}

// The screen's own tokens, so it reads the same in both shells (the static
// one has no --fg-muted) and in both color schemes without runtime.js, which
// is what normally supplies the dark theme. WCAG contrast against the shells'
// page backgrounds (#fafafa light, #161618 dark): title 16.7:1 / 16.6:1, the
// second line 7.1:1 / 7.0:1, button label 17.4:1 / 15.6:1, button border
// 3.3:1 / 3.6:1 (the 3:1 a control's boundary needs).
//
// The button rules carry two classes on purpose: the shells style bare
// `button:hover` with a light grey, which in dark mode would put light text on
// a light background under the pointer.
const bootFailureCSS = `<style>
  .mar-boot-failed {
    --mbf-fg: #1a1a1a; --mbf-muted: #555; --mbf-surface: #fff;
    --mbf-border: #8a8a8e; --mbf-hover: #f0f0f2; --mbf-focus: #2563eb;
    flex: 1; display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    gap: 0.4rem; padding: 2rem 1rem; text-align: center;
    color: var(--mbf-fg);
  }
  @media (prefers-color-scheme: dark) {
    .mar-boot-failed {
      --mbf-fg: #f5f5f7; --mbf-muted: #a1a1a6; --mbf-surface: #1c1c1e;
      --mbf-border: #6e6e73; --mbf-hover: #2c2c2e; --mbf-focus: #0a84ff;
    }
  }
  .mar-boot-failed p { margin: 0; max-width: 24rem; }
  .mar-boot-failed .mar-boot-failed-title { font-size: 17px; font-weight: 600; }
  .mar-boot-failed .mar-boot-failed-line { font-size: 15px; color: var(--mbf-muted); }
  .mar-boot-failed .mar-boot-failed-reload {
    appearance: none; margin-top: 0.9rem;
    min-height: 44px; padding: 0.5rem 1.4rem;
    border: 1px solid var(--mbf-border); border-radius: 10px;
    background: var(--mbf-surface); color: var(--mbf-fg);
    font: inherit; font-size: 15px; font-weight: 500; cursor: pointer;
  }
  @media (hover: hover) {
    .mar-boot-failed .mar-boot-failed-reload:hover { background: var(--mbf-hover); }
  }
  .mar-boot-failed .mar-boot-failed-reload:active { background: var(--mbf-hover); }
  .mar-boot-failed .mar-boot-failed-reload:focus-visible { outline: 2px solid var(--mbf-focus); outline-offset: 2px; }
  .mar-boot-failed .mar-boot-failed-detail {
    margin: 1.25rem 0 0; max-width: min(40rem, 100%); text-align: left;
    white-space: pre-wrap; overflow-wrap: anywhere;
    font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace;
    color: var(--mbf-muted);
  }
</style>
`

// ES5 on purpose: a browser too old to parse runtime.js is one of the ways
// runtime.js "does not run", and it still has to be able to draw this.
const bootFailureJS = `  // The words. English, like every other message the framework itself shows
  // (the runtime's failure screens, the stale-deploy bar); lang="en" below
  // keeps a screen reader from reading them in the app's own voice.
  var TITLE = "This app couldn't load.";
  var LINE = 'Check your connection and try again.';
  var BUTTON = 'Reload';

  var box = null;      // the screen, once drawn
  var waiting = false; // a failure arrived before #mar-root existed
  var leaving = false;

  // Leaving the page aborts its in-flight requests, and their rejections
  // still run in this document: measured in Chrome, a navigation away from a
  // page whose program.json was still pending delivered "Failed to fetch" to
  // both handlers during the pagehide dispatch, after this listener. Without
  // the flag they would draw the failure (and log it) on the way out.
  // pagehide, not beforeunload, so the back/forward cache keeps working.
  window.addEventListener('pagehide', function () { leaving = true; });
  window.addEventListener('pageshow', function () { leaving = false; });

  function showing() {
    var root = document.getElementById('mar-root');
    return !!(box && root && root.firstChild === box);
  }

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    n.className = cls;
    if (text) n.textContent = text;
    return n;
  }

  function render(detail) {
    var root = document.getElementById('mar-root');
    if (!root || showing()) return;
    // Takes the place of the loading placeholder (and of anything a
    // half-started app drew), so nothing on screen still says "Loading…".
    while (root.firstChild) root.removeChild(root.firstChild);
    box = el('div', 'mar-boot-failed');
    box.setAttribute('role', 'alert');
    box.setAttribute('lang', 'en');
    box.appendChild(el('p', 'mar-boot-failed-title', TITLE));
    box.appendChild(el('p', 'mar-boot-failed-line', LINE));
    var btn = el('button', 'mar-boot-failed-reload', BUTTON);
    btn.type = 'button';
    btn.addEventListener('click', function () { window.location.reload(); });
    box.appendChild(btn);
    if (dev && detail) box.appendChild(el('pre', 'mar-boot-failed-detail', detail));
    root.appendChild(box);
  }

  window.__marBootFailed = function (err) {
    if (leaving || waiting || showing()) return;
    var detail = String((err && err.message) || err);
    if (window.console && console.error) console.error('[mar] the app could not start: ' + detail, err);
    if (document.readyState === 'loading') {
      waiting = true;
      document.addEventListener('DOMContentLoaded', function () {
        waiting = false;
        render(detail);
      });
      return;
    }
    render(detail);
  };

  // runtime.js is a plain parser-blocking <script>, so by DOMContentLoaded it
  // has either run or failed: a 404 or 500, a dropped connection, a body cut
  // short, a syntax error, or (on a static host with SPA routing) the page's
  // own HTML served in its place. All of those leave the entry undefined, and
  // none of them is guaranteed to fire the tag's onerror, so the check is on
  // the outcome rather than the event.
  window.__marWhenRuntime = function (name, src, run) {
    if (showing() || waiting) return; // already failed: nothing to start
    if (typeof window[name] === 'function') { run(); return; }
    var s = document.createElement('script');
    s.src = src + (src.indexOf('?') < 0 ? '?' : '&') + 'retry=' + new Date().getTime();
    s.onload = function () {
      if (typeof window[name] === 'function') run();
      else window.__marBootFailed(new Error(src + ' downloaded but did not run: it is not valid JavaScript, or not the Mar runtime (twice, the second time fetched fresh)'));
    };
    s.onerror = function () {
      window.__marBootFailed(new Error('could not download ' + src + ' (twice, the second time fetched fresh)'));
    };
    document.head.appendChild(s);
  };
`
