package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/suggest"
)

// handleList returns all rows from an entity resource after authorization.
func (r *Runtime) handleList(w http.ResponseWriter, req *http.Request, requestID string, entity *model.Entity, auth authSession) error {
	if !r.hasAuthorizer(entity, "read", auth) {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to read %s", entity.Name))
	}

	query, queryArgs, err := r.buildListQuery(entity, auth, req.URL.Query())
	if err != nil {
		return err
	}
	rows, err := queryRowsForRequest(r.DB, requestID, query, queryArgs...)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		decoded := decodeEntityRow(entity, row)
		allowed, err := r.evaluateAuthorization(entity, "read", auth, decoded)
		if err != nil {
			return err
		}
		if allowed {
			out = append(out, decoded)
		}
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
	if err := r.ensureAuthorized(entity, "read", auth, decoded); err != nil {
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

// handleCreate validates payload, checks validation/authorization, inserts, and returns the created row.
func (r *Runtime) handleCreate(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, payload map[string]any) error {
	insert, err := r.buildInsert(entity, payload, auth)
	if err != nil {
		if apiErr, ok := err.(*apiError); ok {
			return apiErr
		}
		return newAPIError(http.StatusBadRequest, "invalid_entity_payload", err.Error())
	}
	if err := r.ensureAuthorized(entity, "create", auth, insert.Context); err != nil {
		return err
	}
	if err := r.validateEntity(entity, insert.Context); err != nil {
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

// handleUpdate validates payload, checks validation/authorization, updates, and returns the updated row.
func (r *Runtime) handleUpdate(w http.ResponseWriter, requestID string, entity *model.Entity, auth authSession, id any, payload map[string]any) error {
	row, ok, err := r.fetchByID(requestID, entity, id)
	if err != nil {
		return err
	}
	if !ok {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	current := decodeEntityRow(entity, row)
	update, err := r.buildUpdate(entity, payload, current, auth)
	if err != nil {
		if apiErr, ok := err.(*apiError); ok {
			return apiErr
		}
		return newAPIError(http.StatusBadRequest, "invalid_entity_payload", err.Error())
	}
	if err := r.ensureAuthorized(entity, "update", auth, update.Context); err != nil {
		return err
	}
	if err := r.validateEntity(entity, update.Context); err != nil {
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
		out[field.Name] = decodeDBValue(&field, row[model.FieldStorageName(&field)])
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

func optionalExpressionValue(value any) any {
	if value == nil {
		return expr.TaggedValue{Tag: "nothing"}
	}
	return expr.TaggedValue{Tag: "just", Values: []any{value}}
}

func entityExpressionValues(entity *model.Entity, entityContext map[string]any) map[string]any {
	out := map[string]any{}
	if entityContext == nil {
		entityContext = map[string]any{}
	}
	for key, value := range entityContext {
		out[key] = value
	}
	if entity == nil {
		return out
	}
	for _, field := range entity.Fields {
		value := entityContext[field.Name]
		if field.Optional {
			value = optionalExpressionValue(value)
		}
		out[field.Name] = value
	}
	return out
}

// validateEntity evaluates the entity validation expression against a request context.
func (r *Runtime) validateEntity(entity *model.Entity, context map[string]any) error {
	validator, ok := r.validators[entity.Name]
	if !ok {
		return nil
	}
	ctx := r.evaluationContext(entity, context, authSession{}, false)
	value, err := validator.Eval(ctx)
	if err != nil {
		if raised, ok := err.(expr.RaisedError); ok {
			return &apiError{
				Status:  http.StatusUnprocessableEntity,
				Code:    "entity_validation_failed",
				Message: raised.Message,
				Details: map[string]any{"entity": entity.Name, "validate": entity.Validate},
			}
		}
		return &apiError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "entity_validation_failed",
			Message: fmt.Sprintf("Validation failed for %s", entity.Name),
			Details: map[string]any{"entity": entity.Name, "validate": entity.Validate},
		}
	}
	valid, err := expr.RequireBool(value)
	if err != nil {
		return &apiError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "entity_validation_failed",
			Message: fmt.Sprintf("Validation for %s must return bool", entity.Name),
			Details: map[string]any{"entity": entity.Name, "validate": entity.Validate},
		}
	}
	if !valid {
		return &apiError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "entity_validation_failed",
			Message: fmt.Sprintf("Validation failed for %s", entity.Name),
			Details: map[string]any{"entity": entity.Name, "validate": entity.Validate},
		}
	}
	return nil
}

// ensureAuthorized evaluates entity authorization for the given action and request context.
func (r *Runtime) ensureAuthorized(entity *model.Entity, action string, auth authSession, entityContext map[string]any) error {
	if !r.appAuthEnabled() {
		return nil
	}
	if !r.hasAuthorizer(entity, action, auth) {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to %s %s", action, entity.Name))
	}
	allowed, err := r.evaluateAuthorization(entity, action, auth, entityContext)
	if err != nil {
		return err
	}
	if !allowed {
		if !auth.Authenticated {
			return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to %s %s", action, entity.Name))
	}
	return nil
}

func (r *Runtime) hasAuthorizer(entity *model.Entity, action string, auth authSession) bool {
	if !r.appAuthEnabled() {
		return true
	}
	if r.allowAdminBuiltInUserAccess(entity, action, auth) {
		return true
	}
	authorizers := r.authorizers[entity.Name]
	_, hasAuthorization := authorizers[action]
	return hasAuthorization
}

func (r *Runtime) evaluateAuthorization(entity *model.Entity, action string, auth authSession, entityContext map[string]any) (bool, error) {
	if !r.appAuthEnabled() {
		return true, nil
	}
	if r.allowAdminBuiltInUserAccess(entity, action, auth) {
		return true, nil
	}
	authorizers := r.authorizers[entity.Name]
	authorization, hasAuthorization := authorizers[action]
	if !hasAuthorization {
		return false, nil
	}
	ctx := r.evaluationContext(entity, entityContext, auth, true)
	v, err := authorization.Eval(ctx)
	if err != nil {
		if raised, ok := err.(expr.RaisedError); ok {
			status := http.StatusForbidden
			code := "not_authorized"
			if !auth.Authenticated {
				status = http.StatusUnauthorized
				code = "auth_required"
			}
			return false, newAPIError(status, code, raised.Message)
		}
		return false, nil
	}
	allowed, err := expr.RequireBool(v)
	if err != nil {
		return false, newAPIError(http.StatusInternalServerError, "authorization_misconfigured", fmt.Sprintf("Authorization for %s.%s must return bool", entity.Name, action))
	}
	return allowed, nil
}

func (r *Runtime) evaluationContext(entity *model.Entity, entityContext map[string]any, auth authSession, includeAuth bool) map[string]any {
	ctx := map[string]any{}
	for k, v := range entityExpressionValues(entity, entityContext) {
		ctx[k] = v
	}
	if includeAuth {
		if auth.Authenticated {
			ctx["current_user"] = expr.TaggedValue{Tag: "authenticated", Values: []any{auth.UserID, auth.Email, auth.Role}}
		} else {
			ctx["current_user"] = expr.TaggedValue{Tag: "anonymous"}
		}
	}
	for name, value := range r.enumLiteralValues {
		ctx[name] = value
	}
	ctx["__functions"] = r.functions
	// Cap each evaluation at a finite operation budget. Exceeding it triggers
	// a structured RaisedError instead of letting unbounded recursion stack-
	// overflow the Go process.
	expr.SetFuel(ctx, expr.DefaultExecutionFuel)
	return ctx
}

func (r *Runtime) allowAdminBuiltInUserAccess(entity *model.Entity, action string, auth authSession) bool {
	if r == nil || entity == nil || r.authUser == nil {
		return false
	}
	if entity.Name != r.authUser.Name {
		return false
	}
	if !auth.Authenticated || !isAdminRole(auth.Role) {
		return false
	}

	switch action {
	case "read":
		return true
	default:
		return false
	}
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
func (r *Runtime) buildInsert(entity *model.Entity, payload map[string]any, auth authSession) (*insertBuild, error) {
	if payload == nil {
		return nil, fmt.Errorf("JSON body must be an object")
	}
	if err := assertNoUnknownFields(entity, payload, "create"); err != nil {
		return nil, err
	}

	out := &insertBuild{Context: map[string]any{}}
	now := time.Now().UnixMilli()
	for _, field := range entity.Fields {
		if field.Primary && field.Auto {
			out.Context[field.Name] = nil
			continue
		}
		if model.IsAuditTimestampField(&field) {
			if err := appendInsertFieldValue(out, &field, now); err != nil {
				return nil, err
			}
			continue
		}
		if field.CurrentUser {
			if !auth.Authenticated {
				return nil, authRequiredError()
			}
			if err := appendInsertFieldValue(out, &field, auth.UserID); err != nil {
				return nil, err
			}
			continue
		}
		value, ok := payload[field.Name]
		if !ok {
			if field.Default != nil {
				if err := appendInsertFieldValue(out, &field, field.Default); err != nil {
					return nil, err
				}
				continue
			}
			if !field.Optional {
				return nil, fmt.Errorf("missing required field %s", field.Name)
			}
			out.Context[field.Name] = nil
			continue
		}
		if err := appendInsertFieldValue(out, &field, value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// buildUpdate normalizes update payload values and merges them into current entity context.
func (r *Runtime) buildUpdate(entity *model.Entity, payload map[string]any, current map[string]any, auth authSession) (*updateBuild, error) {
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
		if model.IsCreatedAtField(&field) {
			continue
		}
		if model.IsUpdatedAtField(&field) {
			now := time.Now().UnixMilli()
			dbValue, apiValue, err := normalizeFieldValue(&field, now)
			if err != nil {
				return nil, err
			}
			out.Columns = append(out.Columns, model.FieldStorageName(&field))
			out.Values = append(out.Values, dbValue)
			out.Context[field.Name] = apiValue
			continue
		}
		if field.CurrentUser {
			if !auth.Authenticated {
				return nil, authRequiredError()
			}
			out.Context[field.Name] = auth.UserID
			continue
		}
		value, ok := payload[field.Name]
		if !ok {
			continue
		}
		dbValue, apiValue, err := normalizeFieldValue(&field, value)
		if err != nil {
			return nil, err
		}
		out.Columns = append(out.Columns, model.FieldStorageName(&field))
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
		if field.CurrentUser {
			return fmt.Errorf("field %s is managed automatically and cannot be provided", key)
		}
		if field.Auto {
			return fmt.Errorf("field %s is auto-generated and cannot be provided", key)
		}
		if mode == "update" && field.Primary {
			return fmt.Errorf("field %s cannot be updated", key)
		}
	}
	return nil
}

func authRequiredError() *apiError {
	return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
}

func appendInsertFieldValue(out *insertBuild, field *model.Field, value any) error {
	dbValue, apiValue, err := normalizeFieldValue(field, value)
	if err != nil {
		return err
	}
	out.Columns = append(out.Columns, model.FieldStorageName(field))
	out.Values = append(out.Values, dbValue)
	out.Context[field.Name] = apiValue
	return nil
}

func normalizeFieldValue(field *model.Field, value any) (any, any, error) {
	return normalizeInputValue(field, value)
}
