package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"belm/internal/expr"
	"belm/internal/model"
	"belm/internal/sqlitecli"
)

type Runtime struct {
	App            *model.App
	DB             *sqlitecli.DB
	entitiesByRes  map[string]*model.Entity
	entitiesByName map[string]*model.Entity
	rules          map[string][]compiledRule
	authorizers    map[string]map[string]expr.Expr
	authUser       *model.Entity
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

func (e *apiError) Error() string {
	return e.Message
}

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
		rules:          map[string][]compiledRule{},
		authorizers:    map[string]map[string]expr.Expr{},
	}

	for i := range app.Entities {
		ent := &app.Entities[i]
		r.entitiesByRes[ent.Resource] = ent
		r.entitiesByName[ent.Name] = ent
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

func (r *Runtime) Close() error {
	return nil
}

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
		fmt.Printf("Belm app %q running on http://localhost:%d\n", r.App.AppName, r.App.Port)
		fmt.Printf("SQLite database: %s\n", r.App.Database)
		for _, entity := range r.App.Entities {
			fmt.Printf("CRUD endpoints: %s\n", entity.Resource)
		}
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

func (r *Runtime) handleHTTP(w http.ResponseWriter, req *http.Request) {
	if err := r.route(w, req); err != nil {
		r.writeError(w, err)
	}
}

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

	auth, err := r.resolveAuth(req)
	if err != nil {
		return err
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
	return r.App.Auth != nil
}

var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteIdentifier(name string) (string, error) {
	if !identifierRe.MatchString(name) {
		return "", fmt.Errorf("unsafe SQL identifier %q", name)
	}
	return `"` + name + `"`, nil
}

func fatalIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
