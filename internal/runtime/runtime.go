package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"belm/internal/expr"
	"belm/internal/model"
	"belm/internal/sqlitecli"
)

// Runtime hosts the compiled Belm app state and serves its HTTP API on top of SQLite.
type Runtime struct {
	App            *model.App
	DB             *sqlitecli.DB
	entitiesByRes  map[string]*model.Entity
	entitiesByName map[string]*model.Entity
	aliasesByName  map[string]*model.TypeAlias
	actionsByName  map[string]*model.Action
	rules          map[string][]compiledRule
	authorizers    map[string]map[string]expr.Expr
	authUser       *model.Entity
	metrics        *metricsCollector
	authLogOnce    sync.Once
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
	Message string
	Details map[string]any
}

// Error implements error for API-layer errors that carry HTTP metadata.
func (e *apiError) Error() string {
	return e.Message
}

// New builds a runtime from an app model, compiles expressions, and applies migrations.
func New(app *model.App) (*Runtime, error) {
	if app == nil {
		return nil, errors.New("app is nil")
	}
	db := sqlitecli.Open(app.Database)

	r := &Runtime{
		App:            app,
		DB:             db,
		entitiesByRes:  map[string]*model.Entity{},
		entitiesByName: map[string]*model.Entity{},
		aliasesByName:  map[string]*model.TypeAlias{},
		actionsByName:  map[string]*model.Action{},
		rules:          map[string][]compiledRule{},
		authorizers:    map[string]map[string]expr.Expr{},
		metrics:        newMetricsCollector(),
	}

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
	if app.Auth != nil {
		r.authUser = r.entitiesByName[app.Auth.UserEntity]
	}

	if err := r.compileExpressions(); err != nil {
		return nil, err
	}
	if err := r.runMigrations(); err != nil {
		return nil, err
	}
	return r, nil
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	return nil
}

// Serve starts the HTTP server and blocks until shutdown or an unrecoverable server error.
func (r *Runtime) Serve(ctx context.Context) error {
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", r.App.Port),
		Handler:      http.HandlerFunc(r.handleHTTP),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		r.printStartupBanner()
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

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

// handleHTTP applies shared transport behavior before delegating to route.
func (r *Runtime) handleHTTP(w http.ResponseWriter, req *http.Request) {
	startedAt := time.Now()
	routeLabel := r.metricsRouteLabel(req)
	writer := &statusRecorder{ResponseWriter: w}

	setCORSHeaders(writer)
	if req.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		r.metrics.recordRequest(req.Method, routeLabel, writer.statusCode(), time.Since(startedAt))
		return
	}

	if err := r.route(writer, req); err != nil {
		r.writeError(writer, err)
	}

	if writer.statusCode() == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	r.metrics.recordRequest(req.Method, routeLabel, writer.statusCode(), time.Since(startedAt))
}

// route resolves Belm endpoints for health, schema, auth, and entity CRUD operations.
func (r *Runtime) route(w http.ResponseWriter, req *http.Request) error {
	path := strings.TrimSuffix(req.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	method := req.Method

	if method == http.MethodGet && path == "/health" {
		r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": r.App.AppName})
		return nil
	}
	if method == http.MethodGet && path == "/_belm/schema" {
		r.writeJSON(w, http.StatusOK, r.schemaPayload())
		return nil
	}
	if method == http.MethodPost && path == "/_belm/bootstrap-admin" {
		payload, err := readJSONBody(req)
		if err != nil {
			return err
		}
		return r.handleBootstrapAdmin(w, payload)
	}

	auth, err := r.resolveAuth(req)
	if err != nil {
		return err
	}

	if method == http.MethodGet && path == "/_belm/perf" {
		if !r.authEnabled() {
			return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
		}
		if !auth.Authenticated {
			return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
		}
		if !isAdminRole(auth.Role) {
			return &apiError{Status: http.StatusForbidden, Message: "Admin role required"}
		}
		r.writeJSON(w, http.StatusOK, r.perfPayload())
		return nil
	}

	if method == http.MethodPost && (path == "/_belm/backups" || path == "/_belm/backup") {
		if !r.authEnabled() {
			return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
		}
		if !auth.Authenticated {
			return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
		}
		if !isAdminRole(auth.Role) {
			return &apiError{Status: http.StatusForbidden, Message: "Admin role required"}
		}
		result, err := CreateSQLiteBackup(r.App.Database, 20)
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
	if method == http.MethodGet && path == "/_belm/backups" {
		if !r.authEnabled() {
			return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
		}
		if !auth.Authenticated {
			return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
		}
		if !isAdminRole(auth.Role) {
			return &apiError{Status: http.StatusForbidden, Message: "Admin role required"}
		}
		backups, err := ListSQLiteBackups(r.App.Database, 100)
		if err != nil {
			return err
		}
		r.writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"backups": backups,
		})
		return nil
	}

	if r.authEnabled() {
		switch {
		case method == http.MethodPost && path == "/auth/request-code":
			payload, err := readJSONBody(req)
			if err != nil {
				return err
			}
			return r.handleAuthRequestCode(w, payload)
		case method == http.MethodPost && path == "/auth/login":
			payload, err := readJSONBody(req)
			if err != nil {
				return err
			}
			return r.handleAuthLogin(w, payload)
		case method == http.MethodPost && path == "/auth/logout":
			return r.handleAuthLogout(w, auth)
		case method == http.MethodGet && path == "/auth/me":
			if !auth.Authenticated {
				return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
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
			return &apiError{Status: http.StatusNotFound, Message: "Route not found"}
		}
		if method != http.MethodPost {
			return &apiError{Status: http.StatusMethodNotAllowed, Message: "Method not allowed"}
		}
		payload, err := readJSONBody(req)
		if err != nil {
			return err
		}
		return r.handleAction(w, name, auth, payload)
	}

	for i := range r.App.Entities {
		entity := &r.App.Entities[i]
		base := entity.Resource
		if path == base {
			switch method {
			case http.MethodGet:
				return r.handleList(w, entity, auth)
			case http.MethodPost:
				payload, err := readJSONBody(req)
				if err != nil {
					return err
				}
				return r.handleCreate(w, entity, auth, payload)
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
				return &apiError{Status: http.StatusBadRequest, Message: fmt.Sprintf("Invalid %s", entity.PrimaryKey)}
			}

			switch method {
			case http.MethodGet:
				return r.handleGet(w, entity, auth, id)
			case http.MethodPut, http.MethodPatch:
				payload, err := readJSONBody(req)
				if err != nil {
					return err
				}
				return r.handleUpdate(w, entity, auth, id, payload)
			case http.MethodDelete:
				return r.handleDelete(w, entity, auth, id)
			}
		}
	}

	return &apiError{Status: http.StatusNotFound, Message: "Route not found"}
}

func (r *Runtime) writeJSON(w http.ResponseWriter, status int, payload any) {
	writeJSON(w, status, payload)
}

// writeError converts internal errors into consistent JSON API responses.
func (r *Runtime) writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	msg := err.Error()
	payload := map[string]any{"error": msg}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		payload["error"] = apiErr.Message
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
	return r.App.Auth != nil
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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
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
	case "/health", "/_belm/schema", "/_belm/perf", "/_belm/backups", "/_belm/bootstrap-admin":
		return path
	case "/_belm/backup":
		// Backward compatibility alias kept for one version.
		return "/_belm/backups"
	}

	if strings.HasPrefix(path, "/auth/") {
		if path == "/auth/request-code" || path == "/auth/login" || path == "/auth/logout" || path == "/auth/me" {
			return path
		}
		return "/auth/:unknown"
	}

	if strings.HasPrefix(path, "/actions/") {
		name := strings.TrimPrefix(path, "/actions/")
		if name != "" && !strings.Contains(name, "/") {
			return "/actions/:name"
		}
		return "/actions/:unknown"
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

	return "/unknown"
}

func isAdminRole(role any) bool {
	roleText, ok := role.(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(roleText), "admin")
}
