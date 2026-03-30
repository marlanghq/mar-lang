package runtime

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"mar/internal/model"
	"mar/internal/sqlitecli"
)

const (
	defaultRequestLogsBufferSize = 200
	minRequestLogsBufferSize     = 10
	maxRequestLogsBufferSize     = 5000
	maxSQLPreviewLength          = 600
)

var (
	emailPattern           = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	bearerPattern          = regexp.MustCompile(`(?i)(bearer\s+)([a-z0-9._\-]+)`)
	tokenQueryParamPattern = regexp.MustCompile(`(?i)(token=)([^&\s]+)`)
	sqlTokenPattern        = regexp.MustCompile(`(?i)(token\s*=\s*')([^']*)(')`)
	sqlCodePattern         = regexp.MustCompile(`(?i)(code\s*=\s*')([^']*)(')`)
	sqlEmailPattern        = regexp.MustCompile(`(?i)(email\s*=\s*')([^']*)(')`)
	plainCodePattern       = regexp.MustCompile(`(?i)(code\s*[:=]\s*)([a-z0-9._\-]+)`)
	jsonTokenPattern       = regexp.MustCompile(`(?i)("token"\s*:\s*")([^"]*)(")`)
	jsonCodePattern        = regexp.MustCompile(`(?i)("code"\s*:\s*")([^"]*)(")`)
	jsonEmailPattern       = regexp.MustCompile(`(?i)("email"\s*:\s*")([^"]*)(")`)
	sqlInsertValuesPattern = regexp.MustCompile(`(?is)\binsert\s+into\s+[^\(]+\(([^)]*)\)\s+values\s*\(([^)]*)\)`)
)

type dbQueryTrace struct {
	RequestID  string
	Timestamp  time.Time
	SQL        string
	DurationMs float64
	RowCount   int
	Error      string
}

type dbQueryCollector struct {
	mu      sync.RWMutex
	nextSeq uint64
	maxSize int
	items   []dbQueryTrace
}

func newDBQueryCollector(requestBuffer int) *dbQueryCollector {
	size := requestBuffer * 20
	if size < 200 {
		size = 200
	}
	if size > 50000 {
		size = 50000
	}
	return &dbQueryCollector{
		maxSize: size,
		items:   make([]dbQueryTrace, 0, size),
	}
}

func (c *dbQueryCollector) record(event sqlitecli.QueryEvent) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextSeq++
	trace := dbQueryTrace{
		RequestID:  strings.TrimSpace(event.RequestID),
		Timestamp:  time.Now(),
		SQL:        normalizeSQLForLog(event.SQL),
		DurationMs: event.DurationMs,
		RowCount:   event.RowCount,
		Error:      strings.TrimSpace(event.Error),
	}
	c.items = append(c.items, trace)
	if len(c.items) > c.maxSize {
		overflow := len(c.items) - c.maxSize
		c.items = append([]dbQueryTrace(nil), c.items[overflow:]...)
	}
}

func (c *dbQueryCollector) rangeByRequestID(requestID string) []dbQueryTrace {
	if c == nil {
		return nil
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.items) == 0 {
		return nil
	}
	out := make([]dbQueryTrace, 0, 8)
	for _, item := range c.items {
		if item.RequestID == requestID {
			out = append(out, item)
		}
	}
	return out
}

type requestLogQuery struct {
	SQL        string  `json:"sql"`
	Reason     string  `json:"reason,omitempty"`
	DurationMs float64 `json:"durationMs"`
	RowCount   int     `json:"rowCount"`
	Error      string  `json:"error,omitempty"`
}

type requestLogEntry struct {
	ID           string            `json:"id"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Route        string            `json:"route"`
	UserEmail    string            `json:"userEmail,omitempty"`
	UserRole     string            `json:"userRole,omitempty"`
	Status       int               `json:"status"`
	DurationMs   float64           `json:"durationMs"`
	Timestamp    string            `json:"timestamp"`
	QueryCount   int               `json:"queryCount"`
	QueryTimeMs  float64           `json:"queryTimeMs"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Queries      []requestLogQuery `json:"queries"`
}

type requestLogStore struct {
	mu     sync.RWMutex
	buffer int
	items  []requestLogEntry
	total  uint64
}

func newRequestLogStore(bufferSize int) *requestLogStore {
	size := clampRequestBuffer(bufferSize)
	return &requestLogStore{
		buffer: size,
		items:  make([]requestLogEntry, 0, size),
	}
}

func clampRequestBuffer(value int) int {
	if value <= 0 {
		return defaultRequestLogsBufferSize
	}
	if value < minRequestLogsBufferSize {
		return minRequestLogsBufferSize
	}
	if value > maxRequestLogsBufferSize {
		return maxRequestLogsBufferSize
	}
	return value
}

func (s *requestLogStore) add(entry requestLogEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.total++
	entry.ID = fmt.Sprintf("req-%d", s.total)
	s.items = append(s.items, entry)
	if len(s.items) > s.buffer {
		overflow := len(s.items) - s.buffer
		s.items = append([]requestLogEntry(nil), s.items[overflow:]...)
	}
}

func (s *requestLogStore) list(limit int) []requestLogEntry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > s.buffer {
		limit = s.buffer
	}
	if len(s.items) == 0 {
		return []requestLogEntry{}
	}

	out := make([]requestLogEntry, 0, limit)
	for i := len(s.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.items[i])
	}
	return out
}

func (s *requestLogStore) bufferSize() int {
	if s == nil {
		return defaultRequestLogsBufferSize
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buffer
}

func (s *requestLogStore) totalCaptured() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

func normalizeSQLForLog(sqlText string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(sqlText)), " ")
	if normalized == "" {
		return ""
	}
	if len(normalized) > maxSQLPreviewLength {
		return normalized[:maxSQLPreviewLength] + "..."
	}
	return normalized
}

func requestLogsBufferSize(app *model.App) int {
	if app == nil || app.System == nil {
		return defaultRequestLogsBufferSize
	}
	return clampRequestBuffer(app.System.RequestLogsBuffer)
}

func (r *Runtime) captureRequestLog(req *http.Request, requestID, route string, status int, duration time.Duration, errMessage string, auth authSession) {
	if r == nil || r.requestLogs == nil {
		return
	}
	if strings.TrimSpace(route) == "/health" {
		return
	}

	rawQueries := r.dbQueries.rangeByRequestID(requestID)
	queries := make([]requestLogQuery, 0, len(rawQueries))
	queryTimeMs := 0.0
	for _, query := range rawQueries {
		queryTimeMs += query.DurationMs
		queries = append(queries, requestLogQuery{
			SQL:        query.SQL,
			Reason:     r.describeQueryReason(req.Method, route, query.SQL),
			DurationMs: query.DurationMs,
			RowCount:   query.RowCount,
			Error:      query.Error,
		})
	}

	entry := requestLogEntry{
		Method:       req.Method,
		Path:         req.URL.Path,
		Route:        route,
		UserEmail:    strings.TrimSpace(auth.Email),
		UserRole:     requestLogUserRole(auth),
		Status:       status,
		DurationMs:   duration.Seconds() * 1000,
		Timestamp:    time.Now().Format("2006-01-02 15:04:05"),
		QueryCount:   len(queries),
		QueryTimeMs:  queryTimeMs,
		ErrorMessage: strings.TrimSpace(errMessage),
		Queries:      queries,
	}
	r.requestLogs.add(entry)
}

func sanitizeRequestLogs(entries []requestLogEntry) []requestLogEntry {
	out := make([]requestLogEntry, 0, len(entries))
	for _, entry := range entries {
		sanitized := requestLogEntry{
			ID:           entry.ID,
			Method:       entry.Method,
			Path:         sanitizeSensitiveText(entry.Path),
			Route:        entry.Route,
			UserEmail:    entry.UserEmail,
			UserRole:     entry.UserRole,
			Status:       entry.Status,
			DurationMs:   entry.DurationMs,
			Timestamp:    entry.Timestamp,
			QueryCount:   entry.QueryCount,
			QueryTimeMs:  entry.QueryTimeMs,
			ErrorMessage: sanitizeSensitiveText(entry.ErrorMessage),
			Queries:      make([]requestLogQuery, 0, len(entry.Queries)),
		}
		for _, query := range entry.Queries {
			sanitized.Queries = append(sanitized.Queries, requestLogQuery{
				SQL:        sanitizeSQLForLogs(query.SQL),
				Reason:     query.Reason,
				DurationMs: query.DurationMs,
				RowCount:   query.RowCount,
				Error:      sanitizeSensitiveText(query.Error),
			})
		}
		out = append(out, sanitized)
	}
	return out
}

func requestLogUserRole(auth authSession) string {
	if auth.Role == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(auth.Role))
}

func (r *Runtime) rememberRequestAuth(requestID string, auth authSession) {
	if r == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	r.requestAuthMu.Lock()
	defer r.requestAuthMu.Unlock()
	r.requestAuthByID[requestID] = auth
}

func (r *Runtime) takeRequestAuth(requestID string) authSession {
	if r == nil || strings.TrimSpace(requestID) == "" {
		return authSession{}
	}
	r.requestAuthMu.Lock()
	defer r.requestAuthMu.Unlock()
	auth := r.requestAuthByID[requestID]
	delete(r.requestAuthByID, requestID)
	return auth
}

func sanitizeSQLForLogs(sqlText string) string {
	sanitized := sanitizeSensitiveText(sqlText)
	sanitized = sanitizeInsertValuesByColumnName(sanitized)
	return sanitized
}

func sanitizeInsertValuesByColumnName(sqlText string) string {
	match := sqlInsertValuesPattern.FindStringSubmatchIndex(sqlText)
	if len(match) < 6 {
		return sqlText
	}

	columnsPart := sqlText[match[2]:match[3]]
	valuesPart := sqlText[match[4]:match[5]]

	columns := splitSQLCSV(columnsPart)
	values := splitSQLCSV(valuesPart)
	if len(columns) == 0 || len(columns) != len(values) {
		return sqlText
	}

	changed := false
	for i := range columns {
		column := normalizeSQLIdentifier(columns[i])
		if column == "token" || column == "code" || column == "email" {
			values[i] = "'<omitted>'"
			changed = true
		}
	}
	if !changed {
		return sqlText
	}

	rebuiltValues := strings.Join(values, ", ")
	return sqlText[:match[4]] + rebuiltValues + sqlText[match[5]:]
}

func (r *Runtime) describeQueryReason(method, route, sqlText string) string {
	route = strings.TrimSpace(route)
	method = strings.ToUpper(strings.TrimSpace(method))
	sqlUpper := strings.ToUpper(strings.TrimSpace(sqlText))
	if sqlUpper == "" {
		return ""
	}

	if reason := r.describeAuthAndAdminQueryReason(route, sqlUpper); reason != "" {
		return reason
	}

	if reason := r.describeEntityQueryReason(method, route, sqlUpper); reason != "" {
		return reason
	}

	if reason := describeActionQueryReason(route, sqlUpper); reason != "" {
		return reason
	}

	return describeGenericQueryReason(sqlUpper)
}

func (r *Runtime) describeAuthAndAdminQueryReason(route, sqlUpper string) string {
	authUserTable := strings.ToUpper(r.authUserTableName())

	switch route {
	case "/_mar/schema":
		if strings.Contains(sqlUpper, "COUNT(*) AS TOTAL") {
			return "Check whether auth bootstrap is still needed"
		}
	case "/auth/login":
		switch {
		case strings.Contains(sqlUpper, "FROM MAR_AUTH_CODES"):
			return "Verify the latest login code for this email"
		case strings.Contains(sqlUpper, "UPDATE MAR_AUTH_CODES SET USED = 1"):
			return "Mark the login code as used"
		case strings.Contains(sqlUpper, "INSERT INTO MAR_SESSIONS"):
			return "Create a new authenticated session"
		case isAuthRoleUpdateQuery(sqlUpper):
			return "Promote the authenticated user to admin"
		case tableMatchesQuery(sqlUpper, authUserTable):
			return "Load the authenticated user record"
		}
	case "/auth/request-code":
		switch {
		case strings.Contains(sqlUpper, "COUNT(*) AS TOTAL"):
			return "Check whether this is the first auth user"
		case strings.Contains(sqlUpper, "INSERT INTO MAR_AUTH_CODES"):
			return "Create a new one-time login code"
		case strings.Contains(sqlUpper, "INSERT INTO "):
			return "Auto-create the auth user when allowed"
		case tableMatchesQuery(sqlUpper, authUserTable):
			return "Load the auth user for this email"
		}
	case "/_mar/admin/bootstrap":
		switch {
		case strings.Contains(sqlUpper, "COUNT(*) AS TOTAL"):
			return "Check whether first-admin bootstrap is still allowed"
		case strings.Contains(sqlUpper, "INSERT INTO MAR_AUTH_CODES"):
			return "Create the first-admin verification code"
		case isAuthRoleUpdateQuery(sqlUpper):
			return "Promote the first user to admin"
		case strings.Contains(sqlUpper, "INSERT INTO "):
			return "Create the first auth user"
		case tableMatchesQuery(sqlUpper, authUserTable):
			return "Load the newly created auth user"
		}
	case "/auth/logout":
		if strings.Contains(sqlUpper, "UPDATE MAR_SESSIONS SET REVOKED = 1") {
			return "Revoke the current session"
		}
	case "/auth/me":
		switch {
		case strings.Contains(sqlUpper, "FROM MAR_SESSIONS"):
			return "Load the current session"
		case tableMatchesQuery(sqlUpper, authUserTable):
			return "Load the current authenticated user"
		}
	}

	switch {
	case strings.Contains(sqlUpper, "FROM MAR_SESSIONS"):
		return "Load the current session"
	case strings.Contains(sqlUpper, "UPDATE MAR_SESSIONS SET REVOKED = 1"):
		return "Revoke the current session"
	case route != "/auth/request-code" && route != "/_mar/admin/bootstrap" && tableMatchesQuery(sqlUpper, authUserTable):
		return "Load the current authenticated user"
	}

	return ""
}

func (r *Runtime) describeEntityQueryReason(method, route, sqlUpper string) string {
	entity, routeKind := r.entityForRouteLabel(route)
	if entity == nil {
		return ""
	}
	entityTable := strings.ToUpper(entity.Table)

	if !tableMatchesQuery(sqlUpper, entityTable) {
		return ""
	}

	switch routeKind {
	case "collection":
		switch method {
		case http.MethodGet:
			if strings.HasPrefix(sqlUpper, "SELECT ") {
				return "Load rows for the entity list"
			}
		case http.MethodPost:
			switch {
			case strings.HasPrefix(sqlUpper, "INSERT INTO "):
				return "Create a new entity entry"
			case strings.HasPrefix(sqlUpper, "SELECT "):
				return "Load the newly created entity entry"
			}
		}
	case "item":
		switch method {
		case http.MethodGet:
			if strings.HasPrefix(sqlUpper, "SELECT ") {
				return "Load the selected entity entry"
			}
		case http.MethodPut, http.MethodPatch:
			switch {
			case strings.HasPrefix(sqlUpper, "UPDATE "):
				return "Update the selected entity entry"
			case strings.HasPrefix(sqlUpper, "SELECT "):
				return "Load the selected entity entry"
			}
		case http.MethodDelete:
			switch {
			case strings.HasPrefix(sqlUpper, "DELETE FROM "):
				return "Delete the selected entity entry"
			case strings.HasPrefix(sqlUpper, "SELECT "):
				return "Load the selected entity entry before deleting it"
			}
		}
	}

	return ""
}

func describeActionQueryReason(route, sqlUpper string) string {
	if route != "/actions/:name" {
		return ""
	}

	switch {
	case strings.HasPrefix(sqlUpper, "INSERT INTO "):
		return "Create rows for this action"
	case strings.HasPrefix(sqlUpper, "UPDATE "):
		return "Update rows for this action"
	case strings.HasPrefix(sqlUpper, "DELETE FROM "):
		return "Delete rows for this action"
	case strings.HasPrefix(sqlUpper, "SELECT "):
		return "Load data needed to run this action"
	}

	return ""
}

func describeGenericQueryReason(sqlUpper string) string {
	switch {
	case strings.HasPrefix(sqlUpper, "SELECT "):
		return "Load data needed for this request"
	case strings.HasPrefix(sqlUpper, "INSERT INTO "):
		return "Create data needed for this request"
	case strings.HasPrefix(sqlUpper, "UPDATE "):
		return "Update data for this request"
	case strings.HasPrefix(sqlUpper, "DELETE FROM "):
		return "Delete data for this request"
	case strings.HasPrefix(sqlUpper, "VACUUM INTO "):
		return "Create a SQLite backup snapshot"
	default:
		return "Run a database operation for this request"
	}
}

func (r *Runtime) authUserTableName() string {
	if r != nil && r.authUser != nil {
		return r.authUser.Table
	}
	return "users"
}

func (r *Runtime) entityForRouteLabel(route string) (*model.Entity, string) {
	if r == nil {
		return nil, ""
	}
	for i := range r.App.Entities {
		entity := &r.App.Entities[i]
		switch route {
		case entity.Resource:
			return entity, "collection"
		case entity.Resource + "/:id":
			return entity, "item"
		}
	}
	return nil, ""
}

func tableMatchesQuery(sqlUpper, expectedTable string) bool {
	tableName := extractSQLTableName(sqlUpper)
	return tableName != "" && tableName == expectedTable
}

func extractSQLTableName(sqlUpper string) string {
	parts := strings.Fields(sqlUpper)
	if len(parts) < 2 {
		return ""
	}

	switch parts[0] {
	case "SELECT":
		for i := 1; i < len(parts)-1; i++ {
			if parts[i] == "FROM" {
				return normalizeSQLTableName(parts[i+1])
			}
		}
	case "INSERT":
		if len(parts) >= 3 && parts[1] == "INTO" {
			return normalizeSQLTableName(parts[2])
		}
	case "UPDATE":
		return normalizeSQLTableName(parts[1])
	case "DELETE":
		if len(parts) >= 3 && parts[1] == "FROM" {
			return normalizeSQLTableName(parts[2])
		}
	}

	return ""
}

func normalizeSQLTableName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "\"`[]")
	return strings.Trim(trimmed, ",")
}

func isAuthRoleUpdateQuery(sqlUpper string) bool {
	return strings.Contains(sqlUpper, " SET ") && strings.Contains(sqlUpper, "ROLE") && strings.Contains(sqlUpper, " WHERE ")
}

func splitSQLCSV(part string) []string {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		return nil
	}

	items := make([]string, 0, 8)
	start := 0
	inQuotes := false
	runes := []rune(trimmed)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		if ch == '\'' {
			if inQuotes && i+1 < len(runes) && runes[i+1] == '\'' {
				i++
				continue
			}
			inQuotes = !inQuotes
			continue
		}

		if ch == ',' && !inQuotes {
			items = append(items, strings.TrimSpace(string(runes[start:i])))
			start = i + 1
		}
	}

	if start < len(runes) {
		items = append(items, strings.TrimSpace(string(runes[start:])))
	}

	return items
}

func normalizeSQLIdentifier(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, "\"`[]")
	if dot := strings.LastIndex(value, "."); dot >= 0 && dot+1 < len(value) {
		value = value[dot+1:]
	}
	value = strings.Trim(value, "\"`[]")
	return strings.ToLower(strings.TrimSpace(value))
}

func sanitizeSensitiveText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	sanitized := text
	sanitized = bearerPattern.ReplaceAllString(sanitized, "${1}<omitted>")
	sanitized = tokenQueryParamPattern.ReplaceAllString(sanitized, "${1}<omitted>")
	sanitized = sqlTokenPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = sqlCodePattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = sqlEmailPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = plainCodePattern.ReplaceAllString(sanitized, "${1}<omitted>")
	sanitized = jsonTokenPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = jsonCodePattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = jsonEmailPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = emailPattern.ReplaceAllStringFunc(sanitized, func(_ string) string {
		return "<omitted>"
	})
	return sanitized
}
