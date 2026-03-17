package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"mar/internal/model"
	"mar/internal/sqlitecli"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func readJSONBody(req *http.Request) (map[string]any, error) {
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		var maxBodyErr *http.MaxBytesError
		if errors.As(err, &maxBodyErr) {
			return nil, newAPIError(http.StatusRequestEntityTooLarge, "request_too_large", "Request body too large")
		}
		return nil, newAPIError(http.StatusBadRequest, "request_body_read_failed", "Failed to read request body")
	}
	if strings.TrimSpace(string(body)) == "" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_json_body", "Invalid JSON body")
	}
	if out == nil {
		return nil, newAPIError(http.StatusBadRequest, "json_body_must_be_object", "JSON body must be an object")
	}
	return out, nil
}

func normalizeInputValue(field *model.Field, value any) (dbValue any, apiValue any, err error) {
	if value == nil {
		if !field.Optional && !field.Primary {
			return nil, nil, fmt.Errorf("field %s cannot be null", field.Name)
		}
		return nil, nil, nil
	}

	switch field.Type {
	case "Int":
		n, ok := toInt64(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Int", field.Name)
		}
		return n, float64(n), nil
	case "Posix":
		n, ok := toInt64(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Posix (Unix milliseconds)", field.Name)
		}
		return n, float64(n), nil
	case "Float":
		f, ok := toFloat64(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Float", field.Name)
		}
		return f, f, nil
	case "String":
		s, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be String", field.Name)
		}
		return s, s, nil
	case "Bool":
		b, ok := value.(bool)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Bool", field.Name)
		}
		if b {
			return int64(1), true, nil
		}
		return int64(0), false, nil
	default:
		return nil, nil, fmt.Errorf("unsupported field type %s", field.Type)
	}
}

func decodeDBValue(field *model.Field, value any) any {
	if value == nil {
		return nil
	}
	switch field.Type {
	case "Bool":
		n, ok := toInt64(value)
		return ok && n == 1
	case "Int":
		n, ok := toInt64(value)
		if !ok {
			return nil
		}
		return float64(n)
	case "Posix":
		n, ok := toInt64(value)
		if !ok {
			return nil
		}
		return float64(n)
	case "Float":
		f, ok := toFloat64(value)
		if !ok {
			return nil
		}
		return f
	case "String":
		s, _ := value.(string)
		return s
	default:
		return value
	}
}

func parsePrimaryValue(entity *model.Entity, raw string) (any, bool) {
	pk := primaryField(entity)
	if pk == nil {
		return nil, false
	}
	switch pk.Type {
	case "Int":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		return n, true
	case "Posix":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		return n, true
	case "Float":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		return f, true
	case "Bool":
		if raw == "true" {
			return int64(1), true
		}
		if raw == "false" {
			return int64(0), true
		}
		return nil, false
	default:
		return raw, true
	}
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		if math.Trunc(t) != t {
			return 0, false
		}
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func queryRows(db *sqlitecli.DB, query string, args ...any) ([]map[string]any, error) {
	return db.QueryRows(query, args...)
}

func queryRow(db *sqlitecli.DB, query string, args ...any) (map[string]any, bool, error) {
	return db.QueryRow(query, args...)
}

func queryRowsForRequest(db *sqlitecli.DB, requestID string, query string, args ...any) ([]map[string]any, error) {
	return db.QueryRowsTagged(requestID, query, args...)
}

func queryRowForRequest(db *sqlitecli.DB, requestID string, query string, args ...any) (map[string]any, bool, error) {
	return db.QueryRowTagged(requestID, query, args...)
}
