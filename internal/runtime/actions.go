package runtime

import (
	"fmt"
	"net/http"
	"strings"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/sqlitecli"
	"mar/internal/suggest"
)

// handleAction executes a typed action in a single BEGIN IMMEDIATE transaction.
func (r *Runtime) handleAction(w http.ResponseWriter, requestID, actionName string, auth authSession, payload map[string]any) error {
	action := r.actionsByName[actionName]
	if action == nil {
		return newAPIError(http.StatusNotFound, "action_not_found", "Action not found")
	}
	alias := r.aliasesByName[action.InputAlias]
	if alias == nil {
		return newAPIError(http.StatusInternalServerError, "action_misconfigured", fmt.Sprintf("Action %s is misconfigured: missing input alias %s", action.Name, action.InputAlias))
	}

	input, err := normalizeActionInput(alias, payload)
	if err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_action_input", err.Error())
	}

	writeCount := 0
	allWritesAreCreates := true
	if err := r.DB.WithImmediateTxTagged(requestID, func(tx *sqlitecli.ImmediateTx) error {
		contextValues := map[string]any{}
		for key, value := range input {
			contextValues["input."+key] = value
		}

		for _, step := range action.Steps {
			entity := r.entitiesByName[step.Entity]
			if entity == nil {
				return newAPIError(http.StatusInternalServerError, "action_misconfigured", fmt.Sprintf("Action %s references unknown entity %s", action.Name, step.Entity))
			}

			result, err := r.executeActionStep(tx, action, entity, step, auth, contextValues)
			if err != nil {
				return err
			}

			if step.Kind != "load" {
				writeCount++
				if step.Kind != "create" {
					allWritesAreCreates = false
				}
			}
			if step.Alias != "" && result != nil {
				for key, value := range result {
					contextValues[step.Alias+"."+key] = value
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	status := http.StatusOK
	if writeCount > 0 && allWritesAreCreates {
		status = http.StatusCreated
	}
	r.writeJSON(w, status, map[string]any{
		"ok":     true,
		"action": action.Name,
		"steps":  len(action.Steps),
	})
	return nil
}

func (r *Runtime) executeActionStep(tx *sqlitecli.ImmediateTx, action *model.Action, entity *model.Entity, step model.ActionStep, auth authSession, contextValues map[string]any) (map[string]any, error) {
	stepPayload, err := resolveActionStepValues(step, contextValues)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step %s %s: %s", action.Name, step.Kind, entity.Name, err.Error()))
	}

	switch step.Kind {
	case "load":
		id, err := normalizePrimaryKeyValue(entity, stepPayload[entity.PrimaryKey])
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step load %s: %s", action.Name, entity.Name, err.Error()))
		}
		row, found, err := fetchByIDInTx(tx, entity, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
		}
		decoded := decodeEntityRow(entity, row)
		if err := r.ensureAuthorized(entity, "read", auth, decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case "create":
		insert, err := r.buildInsert(entity, stepPayload, auth)
		if err != nil {
			if apiErr, ok := err.(*apiError); ok {
				return nil, apiErr
			}
			return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step create %s: %s", action.Name, entity.Name, err.Error()))
		}
		if err := r.ensureAuthorized(entity, "create", auth, insert.Context); err != nil {
			return nil, err
		}
		if err := r.validateEntityRules(entity, insert.Context); err != nil {
			return nil, err
		}

		resultID, err := executeInsertInTx(tx, entity, insert, stepPayload)
		if err != nil {
			return nil, err
		}
		if step.Alias == "" {
			return nil, nil
		}
		created, found, err := fetchByIDInTx(tx, entity, resultID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newAPIError(http.StatusInternalServerError, "created_entity_load_failed", "failed to load created entity")
		}
		return decodeEntityRow(entity, created), nil
	case "update":
		id, err := normalizePrimaryKeyValue(entity, stepPayload[entity.PrimaryKey])
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step update %s: %s", action.Name, entity.Name, err.Error()))
		}
		row, found, err := fetchByIDInTx(tx, entity, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
		}

		current := decodeEntityRow(entity, row)
		updatePayload := map[string]any{}
		for key, value := range stepPayload {
			if key == entity.PrimaryKey {
				continue
			}
			updatePayload[key] = value
		}
		update, err := r.buildUpdate(entity, updatePayload, current, auth)
		if err != nil {
			if apiErr, ok := err.(*apiError); ok {
				return nil, apiErr
			}
			return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step update %s: %s", action.Name, entity.Name, err.Error()))
		}
		if err := r.ensureAuthorized(entity, "update", auth, update.Context); err != nil {
			return nil, err
		}
		if err := r.validateEntityRules(entity, update.Context); err != nil {
			return nil, err
		}

		if err := executeUpdateInTx(tx, entity, id, update); err != nil {
			return nil, err
		}
		if step.Alias == "" {
			return nil, nil
		}
		updated, found, err := fetchByIDInTx(tx, entity, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newAPIError(http.StatusInternalServerError, "updated_entity_load_failed", "failed to load updated entity")
		}
		return decodeEntityRow(entity, updated), nil
	case "delete":
		id, err := normalizePrimaryKeyValue(entity, stepPayload[entity.PrimaryKey])
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_action_input", fmt.Sprintf("Action %s step delete %s: %s", action.Name, entity.Name, err.Error()))
		}
		row, found, err := fetchByIDInTx(tx, entity, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
		}
		decoded := decodeEntityRow(entity, row)
		if err := r.ensureAuthorized(entity, "delete", auth, decoded); err != nil {
			return nil, err
		}
		if err := executeDeleteInTx(tx, entity, id); err != nil {
			return nil, err
		}
		if step.Alias == "" {
			return nil, nil
		}
		return decoded, nil
	default:
		return nil, newAPIError(http.StatusInternalServerError, "action_misconfigured", fmt.Sprintf("Action %s uses unsupported step kind %s", action.Name, step.Kind))
	}
}

func normalizeActionInput(alias *model.TypeAlias, payload map[string]any) (map[string]any, error) {
	fieldNames := aliasFieldNames(alias)
	for key := range payload {
		if !aliasHasField(alias, key) {
			return nil, fmt.Errorf("unknown input field %q for %s%s", key, alias.Name, suggest.DidYouMeanSuffix(key, fieldNames))
		}
	}
	out := map[string]any{}
	for _, field := range alias.Fields {
		raw, ok := payload[field.Name]
		if !ok {
			return nil, fmt.Errorf("missing required input field %q for %s", field.Name, alias.Name)
		}
		value, err := normalizeActionInputValue(field.Name, field.Type, raw)
		if err != nil {
			return nil, err
		}
		out[field.Name] = value
	}
	return out, nil
}

func aliasHasField(alias *model.TypeAlias, fieldName string) bool {
	for _, f := range alias.Fields {
		if f.Name == fieldName {
			return true
		}
	}
	return false
}

func aliasFieldNames(alias *model.TypeAlias) []string {
	out := make([]string, 0, len(alias.Fields))
	for _, field := range alias.Fields {
		out = append(out, field.Name)
	}
	return out
}

func normalizeActionInputValue(name, typ string, raw any) (any, error) {
	switch typ {
	case "Int":
		n, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("input.%s must be Int", name)
		}
		return n, nil
	case "Date":
		n, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("input.%s must be Date (Unix milliseconds)", name)
		}
		return normalizeDateMillis(n), nil
	case "DateTime":
		n, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("input.%s must be DateTime (Unix milliseconds)", name)
		}
		return n, nil
	case "Float":
		f, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("input.%s must be Float", name)
		}
		return f, nil
	case "String":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("input.%s must be String", name)
		}
		return s, nil
	case "Bool":
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("input.%s must be Bool", name)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("unsupported input type %s for input.%s", typ, name)
	}
}

func resolveActionStepValues(step model.ActionStep, contextValues map[string]any) (map[string]any, error) {
	payload := map[string]any{}
	for _, item := range step.Values {
		value, err := evalActionExpression(item.Expression, contextValues)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", item.Field, err)
		}
		payload[item.Field] = value
	}
	return payload, nil
}

func evalActionExpression(raw string, contextValues map[string]any) (any, error) {
	allowed := make(map[string]struct{}, len(contextValues))
	for name := range contextValues {
		allowed[name] = struct{}{}
	}
	node, err := expr.Parse(raw, expr.ParserOptions{AllowedVariables: allowed})
	if err != nil {
		return nil, err
	}
	return node.Eval(contextValues)
}

func normalizePrimaryKeyValue(entity *model.Entity, raw any) (any, error) {
	field := primaryField(entity)
	if field == nil {
		return nil, fmt.Errorf("%s has no primary key", entity.Name)
	}
	dbValue, _, err := normalizeInputValue(field, raw)
	if err != nil {
		return nil, err
	}
	return dbValue, nil
}

func fetchByIDInTx(tx *sqlitecli.ImmediateTx, entity *model.Entity, id any) (map[string]any, bool, error) {
	table, err := quoteIdentifier(entity.Table)
	if err != nil {
		return nil, false, err
	}
	pk, err := quoteIdentifier(entity.PrimaryKey)
	if err != nil {
		return nil, false, err
	}
	return tx.QueryRow(fmt.Sprintf("SELECT * FROM %s WHERE %s = ?", table, pk), id)
}

func executeInsertInTx(tx *sqlitecli.ImmediateTx, entity *model.Entity, insert *insertBuild, stepPayload map[string]any) (any, error) {
	table, err := quoteIdentifier(entity.Table)
	if err != nil {
		return nil, err
	}

	var result sqlitecli.Result
	if len(insert.Columns) == 0 {
		result, err = tx.Exec(fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", table))
		if err != nil {
			return nil, err
		}
	} else {
		cols := make([]string, 0, len(insert.Columns))
		placeholders := make([]string, len(insert.Columns))
		for i, c := range insert.Columns {
			q, err := quoteIdentifier(c)
			if err != nil {
				return nil, err
			}
			cols = append(cols, q)
			placeholders[i] = "?"
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		result, err = tx.Exec(query, insert.Values...)
		if err != nil {
			return nil, err
		}
	}

	pkField := primaryField(entity)
	if pkField != nil && pkField.Auto {
		return result.LastInsertRow, nil
	}
	return normalizePrimaryKeyValue(entity, stepPayload[entity.PrimaryKey])
}

func executeUpdateInTx(tx *sqlitecli.ImmediateTx, entity *model.Entity, id any, update *updateBuild) error {
	table, err := quoteIdentifier(entity.Table)
	if err != nil {
		return err
	}
	pk, err := quoteIdentifier(entity.PrimaryKey)
	if err != nil {
		return err
	}
	assignments := make([]string, 0, len(update.Columns))
	for _, c := range update.Columns {
		q, err := quoteIdentifier(c)
		if err != nil {
			return err
		}
		assignments = append(assignments, fmt.Sprintf("%s = ?", q))
	}
	args := append(update.Values, id)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ?", table, strings.Join(assignments, ", "), pk)
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	return nil
}

func executeDeleteInTx(tx *sqlitecli.ImmediateTx, entity *model.Entity, id any) error {
	table, err := quoteIdentifier(entity.Table)
	if err != nil {
		return err
	}
	pk, err := quoteIdentifier(entity.PrimaryKey)
	if err != nil {
		return err
	}
	res, err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, pk), id)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return newAPIError(http.StatusNotFound, "entity_not_found", entity.Name+" not found")
	}
	return nil
}
