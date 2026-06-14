// `mar cloudflare-pages deploy` — push a static App.frontend bundle
// to Cloudflare Pages via the Direct Upload API.
//
// Flow (all wrapped in progressStep so the operator sees what's
// happening):
//
//   1. Resolve mar.json + validate deploy.cloudflare-pages block.
//   2. Read CLOUDFLARE_API_TOKEN env var (fail with structured
//      hint if missing).
//   3. Run `mar build` to produce dist/ (clean output, frontend-only
//      target).
//   4. Refuse if topology isn't App.frontend — hint at `mar fly
//      deploy` for fullstack projects.
//   5. Walk dist/, hash every file with blake3, build the upload
//      manifest.
//   6. Get a JWT scoped to the project's asset store.
//   7. Ask which hashes are missing on the server.
//   8. Upload missing assets in batches.
//   9. Upsert hashes to commit them to the project's keyspace.
//  10. Create the deployment from the asset manifest.
//  11. Print the deployment URL.
//  12. Poll the per-deployment URL until it serves our exact bundle
//      (content-hash match), then open the browser.
//
// The whole thing is idempotent: re-deploying the same dist/ is a
// no-op for asset uploads (everything already in the store), so
// only steps 1, 2, 6, 7, 10 fire network calls. Most repeat
// deploys finish in under 5 seconds.

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mar/internal/clio"
	"mar/internal/project"
	"mar/internal/scaffold"
)

func runCloudflarePagesDeploy(path string, noOpen bool) int {
	// Resolve manifest first — surfaces missing mar.json /
	// missing-block errors before we spend time on build or
	// network calls. The resolver loads with env: resolution, so
	// target.APIToken is the ACTUAL token bytes (not "env:VAR").
	target, err := resolveCloudflarePagesProject(path)
	if err != nil {
		if _, ok := err.(*project.DeployCloudflarePagesError); ok {
			printDeployCloudflarePagesError(err)
		} else {
			printManifestError("mar cloudflare-pages deploy", err)
		}
		return 1
	}

	// Banner up-front so the operator sees the target before any
	// silent work. Matches the shape of the fly deploy banner.
	printCloudflarePagesBanner(target.App, target.ProjectDir)

	client := newCFClient(target.APIToken)

	// Topology check. CF Pages only serves static files; refusing
	// on fullstack is friendlier than producing a deployment that
	// can't talk to its own backend.
	if err := requireFrontendTopology(target.ProjectDir); err != nil {
		if tme, ok := err.(*topologyMismatchError); ok {
			printTopologyMismatch(tme)
		} else {
			printCloudflarePagesDeployError(target, err)
		}
		return 1
	}

	// Project existence check. If the project doesn't exist yet,
	// offer to create it (interactive) or fail with a clear hint
	// (non-interactive — typo-on-CI would silently create a stray
	// project, so we refuse to auto-create without a TTY).
	//
	// projectInfo carries the project's actual subdomain (which can
	// differ from the project name if the obvious <name>.pages.dev
	// was already taken on another account — CF appends a suffix
	// like -1in to make it unique). We use it for the final success
	// URL so the operator sees the address that actually serves
	// their site.
	projectInfo, err := ensureCloudflarePagesProjectExists(client, target.Account, target.App)
	if err != nil {
		if errors.Is(err, errCFProjectAborted) {
			// Plain-text abort message (not via fprint helpers,
			// so emit our own leading blank to separate from the
			// preceding prompt line).
			fmt.Println()
			fmt.Println("aborted; no project created.")
			return 0
		}
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	// Build the bundle. We always build into a fresh dist/ next
	// to the project — same path `mar build` produces — so a
	// follow-up `mar build` shows the same output.
	distDir := filepath.Join(target.ProjectDir, "dist")
	entry := filepath.Join(target.ProjectDir, "Main.mar")
	// scaffold.Build prints its own "[mar build] wrote N files to
	// dist" line to stdout, which collides with the spinner's
	// in-place redraw and leaves a half-written frame on screen.
	// Wrap it in withStdoutSilenced so the build runs without
	// visible output; the spinner's "✓ Building bundle" is the
	// only signal the operator needs at this point.
	if err := progressStepErr("Building bundle", func() error {
		return withStdoutSilenced(func() error {
			return scaffold.Build(entry, distDir, "")
		})
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	// Walk dist/ once: hash + classify + capture content for the
	// upload phase. files is keyed by the path-in-deployment
	// (relative to dist/, with a leading slash).
	var files map[string]*pagesAsset
	if err := progressStepErr("Hashing assets", func() error {
		f, e := collectDistAssets(distDir)
		files = f
		return e
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	if len(files) == 0 {
		fprintError("mar cloudflare-pages deploy: %s is empty after build.", colorMagenta(distDir))
		fprintHint("This usually means the build silently produced no output.\n" +
			"      Try running mar build directly to see what went wrong.")
		return 1
	}

	// Asset upload dance: JWT → check-missing → upload → upsert.
	var jwt string
	if err := progressStepErr("Authorizing asset upload", func() error {
		j, e := client.cfGetUploadJWT(target.Account, target.App)
		jwt = j
		return e
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	allHashes := make([]string, 0, len(files))
	for _, a := range files {
		allHashes = append(allHashes, a.Key)
	}
	sort.Strings(allHashes) // deterministic order for diffability

	var missing []string
	if err := progressStepErr("Checking asset cache", func() error {
		m, e := client.cfCheckMissingHashes(jwt, allHashes)
		missing = m
		return e
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	if len(missing) > 0 {
		if err := progressStepErr("Uploading assets", func() error {
			return uploadMissingAssets(client, jwt, files, missing)
		}); err != nil {
			printCloudflarePagesDeployError(target, err)
			return 1
		}
	}
	// upsert-hashes is called even if missing was empty: it's the
	// "commit to the project" step, and skipping it on a cache
	// hit risks leaving the assets unreachable from the manifest.
	if err := progressStepErr("Committing assets", func() error {
		return client.cfUpsertHashes(jwt, allHashes)
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	// Build the deployment manifest from the same files map.
	manifest := make(map[string]string, len(files))
	for path, a := range files {
		manifest[path] = a.Key
	}

	var deployment *cfDeploymentResult
	if err := progressStepErr("Creating deployment", func() error {
		d, e := client.cfCreateDeployment(target.Account, target.App, manifest)
		deployment = d
		return e
	}); err != nil {
		printCloudflarePagesDeployError(target, err)
		return 1
	}

	// Success summary. Two URLs: the canonical project URL
	// (stable across deploys) and the per-deployment URL (pins
	// this exact bundle for sharing / debugging).
	//
	// projectInfo.Subdomain comes back from CF as the FULL host
	// (e.g. "mar-website-1in.pages.dev"), not just the leading
	// label. So we use it verbatim instead of appending
	// ".pages.dev" ourselves (which would produce
	// "mar-website-1in.pages.dev.pages.dev"). The fallback path
	// — when CF didn't return a subdomain for some reason —
	// builds the host from target.App as best-effort.
	prodHost := target.App + ".pages.dev"
	if projectInfo != nil && projectInfo.Subdomain != "" {
		prodHost = projectInfo.Subdomain
	}
	prodURL := "https://" + prodHost
	fmt.Println()
	fmt.Printf("[mar cloudflare-pages deploy] %s Deployed.\n", colorGreen("✓"))
	fmt.Println()
	fmt.Printf("  %s  %s\n", colorBold("Production:"), colorCyan(prodURL))
	if deployment != nil && deployment.URL != "" {
		fmt.Printf("  %s  %s\n", colorBold("Deployment:"), colorCyan(deployment.URL))
	}
	fmt.Println()

	// Auto-open the per-DEPLOYMENT URL, not the production alias.
	// The deployment URL (`<hash>.<app>.pages.dev`) is pinned to the
	// bundle we just uploaded; the production alias only flips to this
	// deploy a few seconds later. Same --no-open / CI=true gate as
	// `mar fly deploy` and `mar dev`.
	//
	// Even the pinned deployment URL serves Cloudflare's "Nothing is
	// here yet" placeholder for the first few seconds after the
	// deployment is created, so we poll until it actually serves OUR
	// bundle before opening — otherwise the operator lands on the
	// placeholder and has to refresh by hand.
	if shouldOpenBrowser(noOpen) && deployment != nil && deployment.URL != "" {
		// The entry document's content hash is our version signal:
		// index.html inlines program.json (see scaffold.buildFrontendDist),
		// so any change to the app changes this hash. Frontend-only
		// sites have no backend /version endpoint — this IS the check.
		expectedIndexKey := ""
		if a, ok := files["/index.html"]; ok {
			expectedIndexKey = a.Key
		}
		live := true
		if expectedIndexKey != "" {
			err := progressStepErr("Waiting for the deployment to go live", func() error {
				if waitForCloudflarePagesLive(deployment.URL, expectedIndexKey) {
					return nil
				}
				return errCFDeployNotLive
			})
			live = err == nil
		}
		if !live {
			// Best-effort: the deploy itself succeeded (URLs printed
			// above). It's just still propagating, so open anyway and
			// tell the operator a refresh may be needed.
			fmt.Println()
			fmt.Println(colorDim("  Still propagating. Opening anyway. If you see a Cloudflare"))
			fmt.Println(colorDim("  placeholder, refresh in a few seconds."))
		}
		// Trailing blank so the shell prompt doesn't butt up against
		// the last line, matching the rest of the deploy output.
		fmt.Println()
		openURL(deployment.URL)
	}
	return 0
}

// errCFDeployNotLive signals that the freshly-created deployment did
// not start serving our exact bundle within the poll window. It is NOT
// a deploy failure (the upload already succeeded) — the caller treats
// it as "open anyway, the page may briefly show a placeholder".
var errCFDeployNotLive = errors.New("deployment not serving the new bundle yet")

const (
	// cfDeployReadyTimeout bounds how long we wait for a new deployment
	// to start serving our bundle before opening the browser.
	cfDeployReadyTimeout = 45 * time.Second
	// cfDeployReadyInterval is the gap between readiness polls.
	cfDeployReadyInterval = 1500 * time.Millisecond
	// cfMaxIndexBytes caps how much of the served document we read
	// before hashing — index.html inlines program.json, so it can be
	// large, but never close to this ceiling.
	cfMaxIndexBytes = 64 << 20 // 64 MiB
)

// waitForCloudflarePagesLive polls the per-deployment URL until it
// serves the EXACT bundle we just uploaded, then returns true. It does
// not merely distinguish our app from Cloudflare's placeholder: it
// fetches the entry document and compares its blake3 content hash with
// the index.html hash computed at upload time, so neither the
// propagation placeholder nor a stale earlier deploy ever counts as
// ready. Returns false if the deadline passes without a match.
func waitForCloudflarePagesLive(deployURL, expectedIndexKey string) bool {
	client := &http.Client{Timeout: 8 * time.Second}
	deadline := time.Now().Add(cfDeployReadyTimeout)
	for {
		if cfServesIndexHash(client, deployURL, expectedIndexKey) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(cfDeployReadyInterval)
	}
}

// cfServesIndexHash GETs the deployment root and reports whether the
// served body is byte-for-byte the index.html we uploaded (same blake3
// content key). Any transport error, non-200, or hash mismatch reads as
// "not ready yet".
func cfServesIndexHash(client *http.Client, deployURL, expectedIndexKey string) bool {
	req, err := http.NewRequest(http.MethodGet, deployURL, nil)
	if err != nil {
		return false
	}
	// Defeat any intermediary cache so we observe the deployment's
	// real current state, not a cached placeholder.
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cfMaxIndexBytes))
	if err != nil {
		return false
	}
	// "html" must match the ext collectDistAssets keys index.html under.
	return hashAssetKey(body, "html") == expectedIndexKey
}

// pagesAsset is the staging shape for one file: enough to build
// both the upload payload and the deployment manifest from one
// pass over the dist/ tree.
type pagesAsset struct {
	// Path is the URL path the file gets served from in the
	// deployed site, always starting with "/" (so dist/index.html
	// becomes "/index.html"). Cloudflare's manifest keys this way.
	Path string
	// Key is the blake3-derived content hash (32 hex chars).
	Key string
	// Content is the raw file bytes, kept around so the upload
	// phase can base64-encode without re-reading from disk.
	Content []byte
	// ContentType is the MIME type served back at request time.
	// Detected from extension; CF Pages doesn't infer it from
	// content.
	ContentType string
}

// collectDistAssets walks distDir, reads every file, hashes it,
// and returns the assembled asset map keyed by URL path.
//
// Skips dotfiles (.DS_Store, .gitignore that snuck in) and
// directories. Symlinks are dereferenced — if the operator
// symlinked content into dist/, follow it.
func collectDistAssets(distDir string) (map[string]*pagesAsset, error) {
	out := make(map[string]*pagesAsset)
	err := filepath.Walk(distDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Skip dotfiles. .DS_Store on macOS is the common one;
		// .gitignore won't get into dist/ but other dotfiles
		// might via custom build steps. Keeping the rule simple:
		// nothing whose basename starts with "." goes up.
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(distDir, path)
		if err != nil {
			return err
		}
		// Cloudflare expects forward-slash POSIX-style paths in
		// the manifest. filepath.Rel on Windows would produce
		// backslashes; normalize before keying.
		urlPath := "/" + filepath.ToSlash(rel)
		ext := strings.TrimPrefix(filepath.Ext(rel), ".")
		out[urlPath] = &pagesAsset{
			Path:        urlPath,
			Key:         hashAssetKey(content, ext),
			Content:     content,
			ContentType: contentTypeFor(ext),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// uploadMissingAssets chunks missing hashes into batches and
// uploads each. The chunking exists because CF's documented
// upload limit is per-request, not per-deploy; tiny Mar bundles
// always fit in one batch, but the chunk loop is here so future
// 5MB-per-frame video assets don't surprise us.
func uploadMissingAssets(client *cfClient, jwt string, files map[string]*pagesAsset, missing []string) error {
	// Index files by hash key for O(1) lookup during batching.
	byKey := make(map[string]*pagesAsset, len(files))
	for _, a := range files {
		byKey[a.Key] = a
	}
	for i := 0; i < len(missing); i += cfUploadBatchSize {
		end := i + cfUploadBatchSize
		if end > len(missing) {
			end = len(missing)
		}
		batch := make([]cfAsset, 0, end-i)
		for _, key := range missing[i:end] {
			a, ok := byKey[key]
			if !ok {
				// Server asked for a hash we don't have in the
				// current dist/. Shouldn't happen — check-missing
				// only returns hashes we sent it. Surface clearly
				// so an upstream bug doesn't get swallowed.
				return fmt.Errorf("internal: server requested hash %q not found in dist/", key)
			}
			batch = append(batch, cfAsset{
				Key:    key,
				Value:  base64Encode(a.Content),
				Base64: true,
				Metadata: cfAssetMetdata{
					ContentType: a.ContentType,
				},
			})
		}
		if err := client.cfUploadAssets(jwt, batch); err != nil {
			return err
		}
	}
	return nil
}

// errCFProjectAborted is the sentinel returned by
// ensureCloudflarePagesProjectExists when the operator declined
// the auto-create prompt. The caller catches it and exits 0 —
// declining is a clean abort, not a failure.
var errCFProjectAborted = fmt.Errorf("operator declined to create the project")

// ensureCloudflarePagesProjectExists makes sure the Pages project
// exists under the account. The flow:
//
//  1. GET the project. If it exists, return its info.
//  2. If errCFProjectNotFound: interactive operator gets a prompt,
//     CI gets an error pointing at the dashboard.
//  3. On prompt confirmation, POST to create the project and
//     return the newly-created info.
//  4. Other errors (auth, network) propagate unchanged.
//
// Returning the project info matters because Cloudflare Pages
// subdomains are GLOBALLY unique — if "mar-website" is taken on
// another account, CF assigns you something like "mar-website-1in".
// The caller needs info.Subdomain to print the right production
// URL; hard-coding "<app>.pages.dev" would point operators (and
// anyone they share the URL with) at someone else's site.
//
// Idempotent: re-running after creation hits step 1's "exists"
// branch and returns immediately.
func ensureCloudflarePagesProjectExists(client *cfClient, account, projectName string) (*cfProjectInfo, error) {
	var info *cfProjectInfo
	if err := progressStepErr("Checking project", func() error {
		got, err := client.cfGetProject(account, projectName)
		if errors.Is(err, errCFProjectNotFound) {
			return nil // not an error in this step — handled below
		}
		if err != nil {
			return err
		}
		info = got
		return nil
	}); err != nil {
		return nil, err
	}
	if info != nil {
		return info, nil
	}

	// Project doesn't exist. Decide whether to create.
	if !isInteractive() {
		// CI / piped stdin: refuse to silently create. Two
		// failure modes a typo could cause are equally bad:
		// silently creating a stray "mar-websitee" project, or
		// failing loud with a clear "create it once locally"
		// hint. The latter is easier to recover from.
		fprintError("mar cloudflare-pages deploy: project %s does not exist on Cloudflare\n"+
			"      and stdin is not a TTY (CI / piped).",
			colorCyan(projectName))
		fprintHint("Auto-create only runs interactively to prevent typos from\n"+
			"      generating stray projects. Either:\n"+
			"\n"+
			"        • Run %s locally once to create the project,\n"+
			"          then re-run from CI for subsequent deploys.\n"+
			"        • Or create it in the dashboard:\n"+
			"          %s",
			cmdSuggest("cloudflare-pages deploy"),
			colorCyan("https://dash.cloudflare.com/?to=/:account/pages/new"))
		return nil, errCFProjectAborted
	}

	// Interactive prompt. The Y/n line uses the same shape as
	// the Fly "Create it now in region X?" prompt so the muscle
	// memory carries across.
	fmt.Println()
	fmt.Printf("Project %s does not exist yet on Cloudflare.\n", colorCyan(projectName))
	fmt.Println()
	if !confirmPrompt(fmt.Sprintf("Create %s now?", colorCyan(projectName))) {
		return nil, errCFProjectAborted
	}
	fmt.Println()

	if err := progressStepErr("Creating project", func() error {
		created, err := client.cfCreateProject(account, projectName)
		if err != nil {
			return err
		}
		info = created
		return nil
	}); err != nil {
		return nil, err
	}
	return info, nil
}

// printCloudflarePagesBanner prints the target before any silent
// work starts. Mirrors how runFlyDeploy prints the Fly banner.
//
// MarkTrailingBlank coordinates with fprintError / fprintHint via
// internal/clio: when the next call is an error/hint helper, it
// sees the marked blank and skips its own leading blank, so the
// chain "banner → error" shows ONE blank between, not two.
//
// The Cloudflare account ID is deliberately NOT shown: it's not
// secret per se, but it's the kind of detail operators don't want
// flashing on screen during screencasts or pair-programming
// sessions. The deploy is already pinned to the right account via
// mar.json — repeating the ID on every run adds risk without
// adding information the operator needs at deploy time.
//
// `source` is displayed as the project folder's basename rather
// than the raw input path. Two reasons: (1) identifies the
// project unambiguously without flashing a full home-relative
// path like /Users/<name>/dev/... in screencasts; (2) handles
// the common `mar cloudflare-pages deploy .` case where the raw
// input would just read "." — not useful for confirming the
// target.
//
// The production URL is NOT shown in the banner. CF Pages
// subdomains are globally unique — if "<app>.pages.dev" is taken
// on another account, CF assigns a suffixed variant. We don't
// know the real subdomain until the project-check API call
// completes (a few seconds after the banner prints). Better to
// stay silent than to print a guess that might point at someone
// else's site. The success summary at the end shows the actual
// URL.
func printCloudflarePagesBanner(app, projectDir string) {
	fmt.Println()
	fmt.Println(colorBold("Cloudflare Pages deploy"))
	fmt.Printf("  app:        %s\n", colorCyan(app))
	fmt.Printf("  source:     %s\n", colorMagenta(sourceFolderName(projectDir)))
	fmt.Println()
	clio.MarkTrailingBlank()
}

// sourceFolderName renders projectDir for the banner. The rule:
//
//   - "." (or "./" or "") expands to "./<cwd basename>" so the
//     operator sees a meaningful folder identifier instead of
//     just a dot.
//   - Any other input is shown verbatim — the operator typed it
//     deliberately, and overriding their intent (e.g. collapsing
//     a long path to just its basename) would lose context.
//
// Falls back to the raw input when the cwd lookup fails — better
// to show something than to abort the banner over a stat error.
func sourceFolderName(projectDir string) string {
	if projectDir != "." && projectDir != "./" && projectDir != "" {
		return projectDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return projectDir
	}
	return "./" + filepath.Base(cwd)
}

// contentTypeFor maps an extension (without the dot) to a MIME
// type. Limited set covering what Mar's dist/ produces plus the
// common static-asset extensions; everything else falls back to
// application/octet-stream so browsers will offer a download
// rather than mis-render arbitrary content.
//
// Why a hand-table instead of mime.TypeByExtension: Go's table
// reads from /etc/mime.types on linux and OS-defaults elsewhere,
// which produces different MIME types depending on host. Pinning
// the set here makes deploys reproducible across machines.
func contentTypeFor(ext string) string {
	switch strings.ToLower(ext) {
	case "html", "htm":
		return "text/html; charset=utf-8"
	case "js", "mjs":
		return "application/javascript; charset=utf-8"
	case "json":
		return "application/json; charset=utf-8"
	case "css":
		return "text/css; charset=utf-8"
	case "svg":
		return "image/svg+xml"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "ico":
		return "image/vnd.microsoft.icon"
	case "woff":
		return "font/woff"
	case "woff2":
		return "font/woff2"
	case "ttf":
		return "font/ttf"
	case "txt":
		return "text/plain; charset=utf-8"
	case "wasm":
		return "application/wasm"
	case "map":
		return "application/json; charset=utf-8"
	case "xml":
		return "application/xml; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
