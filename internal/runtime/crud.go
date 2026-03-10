package runtime

import (
	"fmt"
	"net/http"
	"strings"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/suggest"
)

// handleList returns all rows from an entity resource after authorization.
func (r *Runtime) handleList(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession) error {
	if err := r.ensureAuthorized(entity, "list", auth, entityNullContext(entity)); err != nil {
		return err
	}
	table, _ := quoteIdentifier(entity.Table)
	pk, _ := quoteIdentifier(entity.PrimaryKey)
	query := fmt.Sprintf("SELECT * FROM %s ORDER BY %s DESC", table, pk)
	rows, err := queryRowsForRequest(r.DB, requestID, query)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, decodeEntityRow(entity, row))
	}
	r.writeJSON(w, http.StatusOK, out)
	return nil
}

// handleGet returns a single entity row by primary key after authorization.
func (r *Runtime) handleGet(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, id any) error {
	row, ok, err := r.fetchByID(requestID, entity, id)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	decoded := decodeEntityRow(entity, row)
	if err := r.ensureAuthorized(entity, "get", auth, decoded); err != nil {
		return err
	}
	r.writeJSON(w, http.StatusOK, decoded)
	return nil
}

// handleDelete removes a single entity row by primary key after authorization.
func (r *Runtime) handleDelete(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, id any) error {
	row, ok, err := r.fetchByID(requestID, entity, id)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	decoded := decodeEntityRow(entity, row)
	if err := r.ensureAuthorized(entity, "delete", auth, decoded); err != nil {
		return err
	}
	table, _ := quoteIdentifier(entity.Table)
	pk, _ := quoteIdentifier(entity.PrimaryKey)
	res, err := r.DB.ExecTagged(requestID, fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, pk), id)
	if err != nil {
		return err
	}
	affected := res.Changes
	if affected == 0 {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	r.writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	return nil
}

// handleCreate validates payload, checks rules/authorization, inserts, and returns the created row.
func (r *Runtime) handleCreate(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, payload map[string]any) error {
	insert, err := buildInsert(entity, payload)
	if err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_entity_payload", err.Error())
	}
	if err := r.ensureAuthorized(entity, "create", auth, insert.Context); err != nil {
		return err
	}
	if err := r.validateEntityRules(entity, insert.Context); err != nil {
		return err
	}

	table, _ := quoteIdentifier(entity.Table)
	var resultID any
	if len(insert.Columns) == 0 {
		res, err := r.DB.ExecTagged(requestID, fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", table))
		if err != nil {
			return err
		}
		resultID = res.LastInsertRow
	} else {
		cols := make([]string, 0, len(insert.Columns))
		for _, c := range insert.Columns {
			q, _ := quoteIdentifier(c)
			cols = append(cols, q)
		}
		placeholders := make([]string, len(insert.Columns))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		sqlText := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		res, err := r.DB.ExecTagged(requestID, sqlText, insert.Values...)
		if err != nil {
			return err
		}
		pk := primaryField(entity)
		if pk != nil && pk.Auto {
			resultID = res.LastInsertRow
		} else {
			resultID = payload[entity.PrimaryKey]
		}
	}

	created, ok, err := r.fetchByID(requestID, entity, resultID)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusInternalServerError, "created_entity_load_failed", "failed to load created entity")
	}
	r.writeJSON(w, http.StatusCreated, decodeEntityRow(entity, created))
	return nil
}

// handleUpdate validates payload, checks rules/authorization, updates, and returns the updated row.
func (r *Runtime) handleUpdate(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, id any, payload map[string]any) error {
	row, ok, err := r.fetchByID(requestID, entity, id)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	current := decodeEntityRow(entity, row)
	update, err := buildUpdate(entity, payload, current)
	if err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_entity_payload", err.Error())
	}
	if err := r.ensureAuthorized(entity, "update", auth, update.Context); err != nil {
		return err
	}
	if err := r.validateEntityRules(entity, update.Context); err != nil {
		return err
	}

	table, _ := quoteIdentifier(entity.Table)
	pk, _ := quoteIdentifier(entity.PrimaryKey)
	assignments := make([]string, 0, len(update.Columns))
	for _, c := range update.Columns {
		q, _ := quoteIdentifier(c)
		assignments = append(assignments, fmt.Sprintf("%s = ?", q))
	}
	args := append(update.Values, id)
	sqlText := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ?", table, strings.Join(assignments, ", "), pk)
	res, err := r.DB.ExecTagged(requestID, sqlText, args...)
	if err != nil {
		return err
	}
	affected := res.Changes
	if affected == 0 {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}

	updated, ok, err := r.fetchByID(requestID, entity, id)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusInternalServerError, "updated_entity_load_failed", "failed to load updated entity")
	}
	r.writeJSON(w, http.StatusOK, decodeEntityRow(entity, updated))
	return nil
}

func (r *Runtime) fetchByID(requestID string, entity *model.Entity, id any) (map[string]any, bool, error) {
	table, _ := quoteIdentifier(entity.Table)
	pk, _ := quoteIdentifier(entity.PrimaryKey)
	return queryRowForRequest(r.DB, requestID, fmt.Sprintf("SELECT * FROM %s WHERE %s = ?", table, pk), id)
}

func decodeEntityRow(entity *model.Entity, row map[string]any) map[string]any {
	out := map[string]any{}
	for _, field := range entity.Fields {
		out[field.Name] = decodeDBValue(&field, row[field.Name])
	}
	return out
}

func entityNullContext(entity *model.Entity) map[string]any {
	ctx := map[string]any{}
	for _, field := range entity.Fields {
		ctx[field.Name] = nil
	}
	return ctx
}

// validateEntityRules evaluates compiled entity rules against a request context.
func (r *Runtime) validateEntityRules(entity *model.Entity, context map[string]any) error {
	for _, rule := range r.rules[entity.Name] {
		v, err := rule.Expr.Eval(context)
		if err != nil {
			return &apiError{Status: http.StatusUnprocessableEntity, Code: "entity_rule_failed", Message: rule.Message, Details: map[string]any{"entity": entity.Name, "rule": rule.Expression}}
		}
		if !expr.ToBool(v) {
			return &apiError{Status: http.StatusUnprocessableEntity, Code: "entity_rule_failed", Message: rule.Message, Details: map[string]any{"entity": entity.Name, "rule": rule.Expression}}
		}
	}
	return nil
}

// ensureAuthorized evaluates entity authorization for the given action and request context.
func (r *Runtime) ensureAuthorized(entity *model.Entity, action string, auth authSession, entityContext map[string]any) error {
	if !r.appAuthEnabled() {
		return nil
	}
	authorizers := r.authorizers[entity.Name]
	rule, hasRule := authorizers[action]
	if !hasRule {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return nil
	}

	ctx := map[string]any{}
	for k, v := range entityContext {
		ctx[k] = v
	}
	ctx["auth_authenticated"] = auth.Authenticated
	ctx["auth_email"] = auth.Email
	ctx["auth_user_id"] = auth.UserID
	ctx["auth_role"] = auth.Role

	v, err := rule.Eval(ctx)
	if err != nil {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to %s %s", action, entity.Name))
	}
	if !expr.ToBool(v) {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to %s %s", action, entity.Name))
	}
	return nil
}

type insertBuild struct {
	Columns []string
	Values  []any
	Context map[string]any
}

type updateBuild struct {
	Columns []string
	Values  []any
	Context map[string]any
}

// buildInsert normalizes create payload values and builds SQL-ready insert input.
func buildInsert(entity *model.Entity, payload map[string]any) (*insertBuild, error) {
	if payload == nil {
		return nil, fmt.Errorf("JSON body must be an object")
	}
	if err := assertNoUnknownFields(entity, payload, "create"); err != nil {
		return nil, err
	}

	out := &insertBuild{Context: map[string]any{}}
	for _, field := range entity.Fields {
		if field.Primary && field.Auto {
			out.Context[field.Name] = nil
			continue
		}
		value, ok := payload[field.Name]
		if !ok {
			if !field.Optional {
				return nil, fmt.Errorf("missing required field %s", field.Name)
			}
			out.Context[field.Name] = nil
			continue
		}
		dbValue, apiValue, err := normalizeInputValue(&field, value)
		if err != nil {
			return nil, err
		}
		out.Columns = append(out.Columns, field.Name)
		out.Values = append(out.Values, dbValue)
		out.Context[field.Name] = apiValue
	}
	return out, nil
}

// buildUpdate normalizes update payload values and merges them into current entity context.
func buildUpdate(entity *model.Entity, payload map[string]any, current map[string]any) (*updateBuild, error) {
	if payload == nil {
		return nil, fmt.Errorf("JSON body must be an object")
	}
	if err := assertNoUnknownFields(entity, payload, "update"); err != nil {
		return nil, err
	}

	out := &updateBuild{Context: map[string]any{}}
	for k, v := range current {
		out.Context[k] = v
	}

	for _, field := range entity.Fields {
		if field.Primary {
			continue
		}
		value, ok := payload[field.Name]
		if !ok {
			continue
		}
		dbValue, apiValue, err := normalizeInputValue(&field, value)
		if err != nil {
			return nil, err
		}
		out.Columns = append(out.Columns, field.Name)
		out.Values = append(out.Values, dbValue)
		out.Context[field.Name] = apiValue
	}

	if len(out.Columns) == 0 {
		return nil, fmt.Errorf("no updatable fields provided")
	}
	return out, nil
}

func assertNoUnknownFields(entity *model.Entity, payload map[string]any, mode string) error {
	known := map[string]*model.Field{}
	knownNames := make([]string, 0, len(entity.Fields))
	for i := range entity.Fields {
		known[entity.Fields[i].Name] = &entity.Fields[i]
		knownNames = append(knownNames, entity.Fields[i].Name)
	}
	for key := range payload {
		field := known[key]
		if field == nil {
			return fmt.Errorf("unknown field %q%s", key, suggest.DidYouMeanSuffix(key, knownNames))
		}
		if mode == "create" && field.Primary && field.Auto {
			return fmt.Errorf("field %s is auto-generated and cannot be provided", key)
		}
		if mode == "update" && field.Primary {
			return fmt.Errorf("field %s cannot be updated", key)
		}
	}
	return nil
}
