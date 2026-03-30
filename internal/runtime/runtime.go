package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/sqlitecli"
)

// Runtime hosts the compiled Mar app state and serves its HTTP API on top of SQLite.
type Runtime struct {
	App             *model.App
	DB              *sqlitecli.DB
	entitiesByRes   map[string]*model.Entity
	entitiesByName  map[string]*model.Entity
	aliasesByName   map[string]*model.TypeAlias
	actionsByName   map[string]*model.Action
	rules           map[string][]compiledRule
	authorizers     map[string]map[string]expr.Expr
	authUser        *model.Entity
	metrics         *metricsCollector
	requestLogs     *requestLogStore
	dbQueries       *dbQueryCollector
	requestAuthMu   sync.Mutex
	requestAuthByID map[string]authSession
	authRateLimit   *authRateLimiter
	authLogOnce     sync.Once
	requestTraceID  uint64
	startupValid    bool
	startupMu       sync.RWMutex
	startupEnforced bool
	startupReady    bool
	startupErr      error
	publicFS        fs.FS
	appUIFS         fs.FS
	versionInfo     VersionInfo
}

type compiledRule struct {
	Message    string
	Expression string
	Expr       expr.Expr
}

type authSession struct {
	Authenticated bool
	Token         string
	Email         string
	UserID        any
	Role          any
	User          map[string]any
}

type apiError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

const defaultHTTPMaxRequestBodyMB = 1

const (
	defaultSecurityFramePolicy    = "sameorigin"
	defaultSecurityReferrerPolicy = "strict-origin-when-cross-origin"
	defaultSecurityContentNoSniff = true
	securityFramePolicyDeny       = "deny"
	securityFramePolicySameOrigin = "sameorigin"
)

// Error implements error for API-layer errors that carry HTTP metadata.
func (e *apiError) Error() string {
	return e.Message
}

func newAPIError(status int, code, message string) *apiError {
	return &apiError{
		Status:  status,
		Code:    strings.TrimSpace(code),
		Message: message,
	}
}

func defaultAPIErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusUnprocessableEntity:
		return "validation_failed"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusInternalServerError:
		return "internal_error"
	default:
		return "request_failed"
	}
}

// New builds a runtime from an app model, compiles expressions, and applies migrations.
func New(app *model.App) (*Runtime, error) {
	if app == nil {
		return nil, errors.New("app is nil")
	}
	db := sqlitecli.OpenWithConfig(app.Database, SQLiteConfigForApp(app))

	r := &Runtime{
		App:             app,
		DB:              db,
		entitiesByRes:   map[string]*model.Entity{},
		entitiesByName:  map[string]*model.Entity{},
		aliasesByName:   map[string]*model.TypeAlias{},
		actionsByName:   map[string]*model.Action{},
		rules:           map[string][]compiledRule{},
		authorizers:     map[string]map[string]expr.Expr{},
		metrics:         newMetricsCollector(),
		requestLogs:     newRequestLogStore(requestLogsBufferSize(app)),
		dbQueries:       newDBQueryCollector(requestLogsBufferSize(app)),
		requestAuthByID: map[string]authSession{},
		authRateLimit:   newAuthRateLimiter(authRequestCodeRateLimitPerMinute(app), authLoginRateLimitPerMinute(app)),
	}
	db.SetQueryHook(func(event sqlitecli.QueryEvent) {
		r.dbQueries.record(event)
	})

	for i := range app.Entities {
		ent := &app.Entities[i]
		r.entitiesByRes[ent.Resource] = ent
		r.entitiesByName[ent.Name] = ent
	}
	for i := range app.InputAliases {
		alias := &app.InputAliases[i]
		r.aliasesByName[alias.Name] = alias
	}
	for i := range app.Actions {
		action := &app.Actions[i]
		r.actionsByName[action.Name] = action
	}
	r.authUser = r.entitiesByName["User"]

	if err := r.compileExpressions(); err != nil {
		return nil, err
	}
	if err := r.runMigrations(); err != nil {
		return nil, err
	}
	return r, nil
}

func SQLiteConfigForApp(app *model.App) sqlitecli.Config {
	cfg := sqlitecli.DefaultConfig()
	if app == nil || app.System == nil {
		return cfg
	}

	if app.System.SQLiteJournalMode != nil {
		cfg.JournalMode = *app.System.SQLiteJournalMode
	}
	if app.System.SQLiteSynchronous != nil {
		cfg.Synchronous = *app.System.SQLiteSynchronous
	}
	if app.System.SQLiteForeignKeys != nil {
		cfg.ForeignKeys = *app.System.SQLiteForeignKeys
	}
	if app.System.SQLiteBusyTimeoutMs != nil {
		cfg.BusyTimeoutMs = *app.System.SQLiteBusyTimeoutMs
	}
	if app.System.SQLiteWALAutoCheckpoint != nil {
		cfg.WALAutoCheckpoint = *app.System.SQLiteWALAutoCheckpoint
	}
	if app.System.SQLiteJournalSizeLimitMB != nil {
		if *app.System.SQLiteJournalSizeLimitMB < 0 {
			cfg.JournalSizeLimitB = -1
		} else {
			cfg.JournalSizeLimitB = int64(*app.System.SQLiteJournalSizeLimitMB) * 1024 * 1024
		}
	}
	if app.System.SQLiteMmapSizeMB != nil {
		cfg.MmapSizeB = int64(*app.System.SQLiteMmapSizeMB) * 1024 * 1024
	}
	if app.System.SQLiteCacheSizeKB != nil {
		cfg.CacheSizeKB = *app.System.SQLiteCacheSizeKB
	}

	return cfg
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	if r == nil || r.DB == nil {
		return nil
	}
	return r.DB.Close()
}

// Serve starts the HTTP server and blocks until shutdown or an unrecoverable server error.
func (r *Runtime) Serve(ctx context.Context) error {
	r.beginStartupReadiness()

	server := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", r.App.Port),
		Handler:      http.HandlerFunc(r.handleHTTP),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go r.runStartupReadinessChecks()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (r *Runtime) beginStartupReadiness() {
	r.startupMu.Lock()
	defer r.startupMu.Unlock()
	r.startupEnforced = true
	r.startupReady = false
	r.startupErr = nil
}

func (r *Runtime) runStartupReadinessChecks() {
	err := r.ValidateStartup()

	r.startupMu.Lock()
	r.startupErr = err
	r.startupReady = err == nil
	r.startupMu.Unlock()

	if err != nil {
		PrintStartupError(err, "")
		return
	}

	r.printStartupBanner()
}

func (r *Runtime) startupReadiness() (bool, bool, error) {
	r.startupMu.RLock()
	defer r.startupMu.RUnlock()
	return r.startupEnforced, r.startupReady, r.startupErr
}

// handleHTTP applies shared transport behavior before delegating to route.
func (r *Runtime) handleHTTP(w http.ResponseWriter, req *http.Request) {
	startedAt := time.Now()
	requestID := r.nextRequestTraceID()
	routeLabel := r.metricsRouteLabel(req)
	writer := &statusRecorder{ResponseWriter: w}
	if req.Body != nil {
		req.Body = http.MaxBytesReader(writer, req.Body, httpMaxRequestBodyBytes(r.App))
	}
	requestError := ""

	finishRequest := func() {
		status := writer.statusCode()
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(startedAt)
		r.metrics.recordRequest(req.Method, routeLabel, status, duration)
		r.captureRequestLog(req, requestID, routeLabel, status, duration, requestError, r.takeRequestAuth(requestID))
	}

	setCORSHeaders(writer)
	setSecurityHeaders(writer, r.App)
	if req.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		finishRequest()
		return
	}

	if err := r.route(writer, req, requestID); err != nil {
		requestError = err.Error()
		r.writeError(writer, err)
	}

	if writer.statusCode() == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	finishRequest()
}

func (r *Runtime) nextRequestTraceID() string {
	if r == nil {
		return ""
	}
	seq := atomic.AddUint64(&r.requestTraceID, 1)
	return fmt.Sprintf("req-trace-%d", seq)
}

func httpMaxRequestBodyBytes(app *model.App) int64 {
	mb := defaultHTTPMaxRequestBodyMB
	if app != nil && app.System != nil && app.System.HTTPMaxRequestBodyMB != nil && *app.System.HTTPMaxRequestBodyMB > 0 {
		mb = *app.System.HTTPMaxRequestBodyMB
	}
	return int64(mb) * 1024 * 1024
}

// route resolves Mar endpoints for health, schema, auth, and entity CRUD operations.
func (r *Runtime) route(w http.ResponseWriter, req *http.Request, requestID string) error {
	path := strings.TrimSuffix(req.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	method := req.Method

	if method == http.MethodGet && path == "/" && (r.App == nil || r.App.Public == nil) {
		http.Redirect(w, req, "/_mar", http.StatusFound)
		return nil
	}

	if method == http.MethodGet && path == "/health" {
		enforced, ready, startupErr := r.startupReadiness()
		if enforced && !ready {
			payload := map[string]any{
				"ok":     false,
				"app":    r.App.AppName,
				"status": "starting",
			}
			if startupErr != nil {
				payload["status"] = "failed"
				payload["message"] = "Startup checks failed. Check the application logs."
			}
			r.writeJSON(w, http.StatusServiceUnavailable, payload)
			return nil
		}
		r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": r.App.AppName, "status": "ready"})
		return nil
	}
	if enforced, ready, startupErr := r.startupReadiness(); enforced && !ready {
		if startupErr != nil {
			return newAPIError(http.StatusServiceUnavailable, "startup_check_failed", "Startup checks failed. Check the application logs.")
		}
		return newAPIError(http.StatusServiceUnavailable, "app_starting", "App is still starting. Try again shortly.")
	}
	if method == http.MethodGet && path == "/_mar/schema" {
		r.writeJSON(w, http.StatusOK, r.schemaPayload(requestID))
		return nil
	}
	if method == http.MethodGet && path == "/_mar/version" {
		r.writeJSON(w, http.StatusOK, r.publicVersionPayload())
		return nil
	}
	if method == http.MethodPost && path == "/_mar/admin/bootstrap" {
		payload, err := readJSONBody(req)
		if err != nil {
			return err
		}
		return r.handleBootstrapAdmin(w, requestID, payload)
	}

	auth, err := r.resolveAuth(req, requestID)
	if err != nil {
		return err
	}
	r.rememberRequestAuth(requestID, auth)

	if method == http.MethodGet && path == "/_mar/admin/perf" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}
		r.writeJSON(w, http.StatusOK, r.perfPayload())
		return nil
	}
	if method == http.MethodGet && path == "/_mar/admin/version" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}
		r.writeJSON(w, http.StatusOK, r.protectedVersionPayload())
		return nil
	}

	if method == http.MethodGet && path == "/_mar/admin/request-logs" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}

		limit := parsePositiveInt(req.URL.Query().Get("limit"), 50)
		if limit < 1 {
			limit = 1
		}
		logs := sanitizeRequestLogs(r.requestLogs.list(limit))
		r.writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"buffer":        r.requestLogs.bufferSize(),
			"totalCaptured": r.requestLogs.totalCaptured(),
			"logs":          logs,
		})
		return nil
	}

	if method == http.MethodPost && path == "/_mar/admin/backups" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}
		result, err := CreateSQLiteBackup(r.App.Database, SQLiteConfigForApp(r.App), 20)
		if err != nil {
			return err
		}
		r.writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"path":      result.Path,
			"backupDir": result.BackupDir,
			"removed":   result.Removed,
			"keptLast":  result.KeptLast,
		})
		return nil
	}
	if method == http.MethodGet && path == "/_mar/admin/backups" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}
		backups, err := ListSQLiteBackups(r.App.Database, 100)
		if err != nil {
			return err
		}
		r.writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"backupDir": backupDirectory(r.App.Database),
			"backups":   backups,
		})
		return nil
	}
	if method == http.MethodGet && path == "/_mar/admin/backups/download" {
		if !r.authEnabled() {
			return newAPIError(http.StatusNotFound, "auth_not_enabled", "Authentication is not enabled")
		}
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		if !isAdminRole(auth.Role) {
			return newAPIError(http.StatusForbidden, "admin_role_required", "Admin role required")
		}

		name := strings.TrimSpace(req.URL.Query().Get("name"))
		if name == "" {
			return newAPIError(http.StatusBadRequest, "backup_name_required", "Backup name is required")
		}

		backup, ok, err := FindSQLiteBackupByName(r.App.Database, name)
		if err != nil {
			return err
		}
		if !ok {
			return newAPIError(http.StatusNotFound, "backup_not_found", "Backup not found")
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", backup.Name))
		http.ServeFile(w, req, backup.Path)
		return nil
	}

	if r.authEnabled() {
		switch {
		case method == http.MethodPost && path == "/auth/request-code":
			payload, err := readJSONBody(req)
			if err != nil {
				return err
			}
			email, err := parseAuthEmail(payload)
			if err != nil {
				return err
			}
			if !r.authRateLimit.allowRequestCode(req, email) {
				return newAPIError(http.StatusTooManyRequests, "rate_limit_request_code", "You requested too many codes. Please wait a minute and try again.")
			}
			return r.handleAuthRequestCode(w, requestID, payload)
		case method == http.MethodPost && path == "/auth/login":
			payload, err := readJSONBody(req)
			if err != nil {
				return err
			}
			email, err := parseAuthEmail(payload)
			if err != nil {
				return err
			}
			if !r.authRateLimit.allowLogin(req, email) {
				return newAPIError(http.StatusTooManyRequests, "rate_limit_login", "Too many sign-in attempts. Please wait a minute and try again.")
			}
			return r.handleAuthLogin(w, req, requestID, payload)
		case method == http.MethodPost && path == "/auth/logout":
			return r.handleAuthLogout(w, req, requestID, auth)
		case method == http.MethodGet && path == "/auth/me":
			if !auth.Authenticated {
				return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
			}
			r.writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"email":         auth.Email,
				"userId":        auth.UserID,
				"role":          auth.Role,
				"user":          auth.User,
			})
			return nil
		}
	}

	if strings.HasPrefix(path, "/actions/") {
		name := strings.TrimPrefix(path, "/actions/")
		if name == "" || strings.Contains(name, "/") {
			return newAPIError(http.StatusNotFound, "route_not_found", "Route not found")
		}
		if method != http.MethodPost {
			return newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
		}
		payload, err := readJSONBody(req)
		if err != nil {
			return err
		}
		return r.handleAction(w, requestID, name, auth, payload)
	}

	for i := range r.App.Entities {
		entity := &r.App.Entities[i]
		base := entity.Resource
		if path == base {
			switch method {
			case http.MethodGet:
				return r.handleList(w, requestID, entity, auth)
			case http.MethodPost:
				payload, err := readJSONBody(req)
				if err != nil {
					return err
				}
				return r.handleCreate(w, requestID, entity, auth, payload)
			}
		}

		prefix := base + "/"
		if strings.HasPrefix(path, prefix) {
			rawID := strings.TrimPrefix(path, prefix)
			if rawID == "" || strings.Contains(rawID, "/") {
				continue
			}
			id, ok := parsePrimaryValue(entity, rawID)
			if !ok {
				return newAPIError(http.StatusBadRequest, "invalid_primary_key", fmt.Sprintf("Invalid %s", entity.PrimaryKey))
			}

			switch method {
			case http.MethodGet:
				return r.handleGet(w, requestID, entity, auth, id)
			case http.MethodPut, http.MethodPatch:
				payload, err := readJSONBody(req)
				if err != nil {
					return err
				}
				return r.handleUpdate(w, requestID, entity, auth, id, payload)
			case http.MethodDelete:
				return r.handleDelete(w, requestID, entity, auth, id)
			}
		}
	}

	if served, err := r.serveAppUIAsset(w, req, path); served {
		return err
	}

	if served, err := r.servePublicAsset(w, req, path); served {
		return err
	}

	return newAPIError(http.StatusNotFound, "route_not_found", "Route not found")
}

func (r *Runtime) writeJSON(w http.ResponseWriter, status int, payload any) {
	writeJSON(w, status, payload)
}

// writeError converts internal errors into consistent JSON API responses.
func (r *Runtime) writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := defaultAPIErrorCode(status)
	msg := err.Error()
	payload := map[string]any{"error": msg, "message": msg, "errorCode": code}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		if strings.TrimSpace(apiErr.Code) != "" {
			code = apiErr.Code
		} else {
			code = defaultAPIErrorCode(status)
		}
		payload["error"] = apiErr.Message
		payload["message"] = apiErr.Message
		payload["errorCode"] = code
		if len(apiErr.Details) > 0 {
			payload["details"] = apiErr.Details
		}
	}
	writeJSON(w, status, payload)
}

func (r *Runtime) authEnabled() bool {
	return true
}

func (r *Runtime) appAuthEnabled() bool {
	return r != nil && r.authUser != nil
}

var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// quoteIdentifier validates and quotes SQL identifiers to avoid unsafe interpolation.
func quoteIdentifier(name string) (string, error) {
	if !identifierRe.MatchString(name) {
		return "", fmt.Errorf("unsafe SQL identifier %q", name)
	}
	return `"` + name + `"`, nil
}

// setCORSHeaders sets permissive CORS defaults for local development and generated UIs.
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Mar-Admin-UI")
}

func setSecurityHeaders(w http.ResponseWriter, app *model.App) {
	w.Header().Set("X-Frame-Options", securityFrameHeaderValue(framePolicyForApp(app)))
	w.Header().Set("Referrer-Policy", referrerPolicyForApp(app))
	if contentNoSniffForApp(app) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
}

func framePolicyForApp(app *model.App) string {
	if app == nil || app.Auth == nil || app.Auth.SecurityFramePolicy == nil {
		return defaultSecurityFramePolicy
	}
	value := strings.ToLower(strings.TrimSpace(*app.Auth.SecurityFramePolicy))
	if value == securityFramePolicyDeny || value == securityFramePolicySameOrigin {
		return value
	}
	return defaultSecurityFramePolicy
}

func securityFrameHeaderValue(policy string) string {
	if policy == securityFramePolicyDeny {
		return "DENY"
	}
	return "SAMEORIGIN"
}

func referrerPolicyForApp(app *model.App) string {
	if app == nil || app.Auth == nil || app.Auth.SecurityReferrerPolicy == nil {
		return defaultSecurityReferrerPolicy
	}
	value := strings.TrimSpace(*app.Auth.SecurityReferrerPolicy)
	if value == "strict-origin-when-cross-origin" || value == "no-referrer" {
		return value
	}
	return defaultSecurityReferrerPolicy
}

func contentNoSniffForApp(app *model.App) bool {
	if app == nil || app.Auth == nil || app.Auth.SecurityContentNoSniff == nil {
		return defaultSecurityContentNoSniff
	}
	return *app.Auth.SecurityContentNoSniff
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (r *Runtime) metricsRouteLabel(req *http.Request) string {
	path := strings.TrimSuffix(req.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	switch path {
	case "/health", "/_mar/schema", "/_mar/version", "/_mar/admin/version", "/_mar/admin/perf", "/_mar/admin/backups", "/_mar/admin/backups/download", "/_mar/admin/bootstrap":
		return path
	case "/_mar/admin/request-logs":
		return path
	}
	if path == "/_mar" || strings.HasPrefix(path, "/_mar/") {
		return "/_mar"
	}

	switch path {
	case "/auth/request-code", "/auth/login", "/auth/logout", "/auth/me":
		return path
	}

	if strings.HasPrefix(path, "/actions/") {
		name := strings.TrimPrefix(path, "/actions/")
		if name != "" && !strings.Contains(name, "/") {
			return "/actions/:name"
		}
		return "/not-found"
	}

	for i := range r.App.Entities {
		base := r.App.Entities[i].Resource
		if path == base {
			return base
		}
		prefix := base + "/"
		if strings.HasPrefix(path, prefix) {
			rawID := strings.TrimPrefix(path, prefix)
			if rawID != "" && !strings.Contains(rawID, "/") {
				return base + "/:id"
			}
		}
	}

	return "/not-found"
}

func isAdminRole(role any) bool {
	roleText, ok := role.(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(roleText), "admin")
}

func parsePositiveInt(raw string, fallback int) int {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fallback
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
