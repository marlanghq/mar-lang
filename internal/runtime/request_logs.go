package runtime

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"belm/internal/model"
	"belm/internal/sqlitecli"
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
	devCodePattern         = regexp.MustCompile(`(?i)(devcode\s*[:=]\s*)([a-z0-9._\-]+)`)
	jsonTokenPattern       = regexp.MustCompile(`(?i)("token"\s*:\s*")([^"]*)(")`)
	jsonCodePattern        = regexp.MustCompile(`(?i)("code"\s*:\s*")([^"]*)(")`)
	jsonEmailPattern       = regexp.MustCompile(`(?i)("email"\s*:\s*")([^"]*)(")`)
	sqlBindPlaceholder     = regexp.MustCompile(`\?(?:\d+)?`)
	sqlInsertValuesPattern = regexp.MustCompile(`(?is)\binsert\s+into\s+[^\(]+\(([^)]*)\)\s+values\s*\(([^)]*)\)`)
)

type dbQueryTrace struct {
	Seq        uint64
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
		Seq:        c.nextSeq,
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

func (c *dbQueryCollector) latestSeq() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nextSeq
}

func (c *dbQueryCollector) rangeSince(startSeq uint64) []dbQueryTrace {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.items) == 0 {
		return nil
	}
	out := make([]dbQueryTrace, 0, 8)
	for _, item := range c.items {
		if item.Seq > startSeq {
			out = append(out, item)
		}
	}
	return out
}

type requestLogQuery struct {
	SQL        string  `json:"sql"`
	DurationMs float64 `json:"durationMs"`
	RowCount   int     `json:"rowCount"`
	Error      string  `json:"error,omitempty"`
}

type requestLogEntry struct {
	ID           string            `json:"id"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Route        string            `json:"route"`
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

func (r *Runtime) captureRequestLog(req *http.Request, route string, status int, duration time.Duration, errMessage string, querySeqStart uint64) {
	if r == nil || r.requestLogs == nil {
		return
	}

	rawQueries := r.dbQueries.rangeSince(querySeqStart)
	queries := make([]requestLogQuery, 0, len(rawQueries))
	queryTimeMs := 0.0
	for _, query := range rawQueries {
		queryTimeMs += query.DurationMs
		queries = append(queries, requestLogQuery{
			SQL:        query.SQL,
			DurationMs: query.DurationMs,
			RowCount:   query.RowCount,
			Error:      query.Error,
		})
	}

	entry := requestLogEntry{
		Method:       req.Method,
		Path:         req.URL.Path,
		Route:        route,
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
				DurationMs: query.DurationMs,
				RowCount:   query.RowCount,
				Error:      sanitizeSensitiveText(query.Error),
			})
		}
		out = append(out, sanitized)
	}
	return out
}

func sanitizeSQLForLogs(sqlText string) string {
	sanitized := sanitizeSensitiveText(sqlText)
	sanitized = sanitizeInsertValuesByColumnName(sanitized)
	sanitized = labelSQLBindPlaceholders(sanitized)
	return sanitized
}

func labelSQLBindPlaceholders(sqlText string) string {
	index := 0
	return sqlBindPlaceholder.ReplaceAllStringFunc(sqlText, func(_ string) string {
		index++
		return fmt.Sprintf("$%d", index)
	})
}

func sanitizeInsertValuesByColumnName(sqlText string) string {
	match := sqlInsertValuesPattern.FindStringSubmatchIndex(sqlText)
	if match == nil || len(match) < 6 {
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
	sanitized = devCodePattern.ReplaceAllString(sanitized, "${1}<omitted>")
	sanitized = jsonTokenPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = jsonCodePattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = jsonEmailPattern.ReplaceAllString(sanitized, "${1}<omitted>${3}")
	sanitized = emailPattern.ReplaceAllStringFunc(sanitized, func(_ string) string {
		return "<omitted>"
	})
	return sanitized
}
