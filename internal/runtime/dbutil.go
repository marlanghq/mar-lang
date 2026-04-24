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
	"time"

	"mar/internal/expr"
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
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
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
	case "Date":
		n, ok := toInt64(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Date (Unix milliseconds)", field.Name)
		}
		n = normalizeDateMillis(n)
		return n, float64(n), nil
	case "DateTime":
		n, ok := toInt64(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be DateTime (Unix milliseconds)", field.Name)
		}
		return n, float64(n), nil
	case "Decimal":
		f, ok := toDecimal(value)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be Decimal", field.Name)
		}
		return f.String(), f, nil
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
		if len(field.EnumValues) == 0 {
			return nil, nil, fmt.Errorf("unsupported field type %s", field.Type)
		}
		s, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("field %s must be %s", field.Name, field.Type)
		}
		for _, enumValue := range field.EnumValues {
			if s == enumValue {
				return s, s, nil
			}
		}
		return nil, nil, fmt.Errorf("field %s must be one of: %s", field.Name, strings.Join(field.EnumValues, ", "))
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
	case "Date", "DateTime":
		n, ok := toInt64(value)
		if !ok {
			return nil
		}
		return float64(n)
	case "Decimal":
		f, ok := toDecimal(value)
		if !ok {
			return nil
		}
		return f
	case "String":
		s, _ := value.(string)
		return s
	default:
		if len(field.EnumValues) > 0 {
			s, _ := value.(string)
			return s
		}
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
	case "Date", "DateTime":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		if pk.Type == "Date" {
			n = normalizeDateMillis(n)
		}
		return n, true
	case "Decimal":
		f, err := expr.ParseDecimal(raw)
		if err != nil {
			return nil, false
		}
		return f.String(), true
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

func toDecimal(v any) (expr.Decimal, bool) {
	switch t := v.(type) {
	case expr.Decimal:
		return t, true
	case int:
		return expr.NewDecimalFromInt(int64(t)), true
	case int64:
		return expr.NewDecimalFromInt(t), true
	case json.Number:
		value, err := expr.ParseDecimal(t.String())
		return value, err == nil
	case string:
		value, err := expr.ParseDecimal(t)
		return value, err == nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return expr.Decimal{}, false
		}
		value, err := expr.ParseDecimal(strconv.FormatFloat(t, 'g', -1, 64))
		return value, err == nil
	default:
		return expr.Decimal{}, false
	}
}

func normalizeDateMillis(value int64) int64 {
	t := time.UnixMilli(value).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
}

func queryRowsForRequest(db *sqlitecli.DB, requestID string, query string, args ...any) ([]map[string]any, error) {
	return db.QueryRowsTagged(requestID, query, args...)
}

func queryRowForRequest(db *sqlitecli.DB, requestID string, query string, args ...any) (map[string]any, bool, error) {
	return db.QueryRowTagged(requestID, query, args...)
}
