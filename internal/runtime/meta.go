package runtime

// schemaPayload builds metadata consumed by the admin UI and generated clients.
func (r *Runtime) schemaPayload(requestID string) map[string]any {
	entities := make([]map[string]any, 0, len(r.App.Entities))
	for _, entity := range r.App.Entities {
		fields := make([]map[string]any, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			fieldPayload := map[string]any{
				"name":     field.Name,
				"type":     field.Type,
				"primary":  field.Primary,
				"auto":     field.Auto,
				"optional": field.Optional,
			}
			if field.Default != nil {
				fieldPayload["default"] = field.Default
			}
			fields = append(fields, fieldPayload)
		}
		entities = append(entities, map[string]any{
			"name":       entity.Name,
			"table":      entity.Table,
			"resource":   entity.Resource,
			"primaryKey": entity.PrimaryKey,
			"fields":     fields,
		})
	}

	payload := map[string]any{
		"appName":  r.App.AppName,
		"port":     r.App.Port,
		"database": r.App.Database,
		"entities": entities,
	}
	if len(r.App.InputAliases) > 0 {
		aliases := make([]map[string]any, 0, len(r.App.InputAliases))
		for _, alias := range r.App.InputAliases {
			fields := make([]map[string]any, 0, len(alias.Fields))
			for _, field := range alias.Fields {
				fields = append(fields, map[string]any{
					"name": field.Name,
					"type": field.Type,
				})
			}
			aliases = append(aliases, map[string]any{
				"name":   alias.Name,
				"fields": fields,
			})
		}
		payload["inputAliases"] = aliases
	}
	if len(r.App.Actions) > 0 {
		actions := make([]map[string]any, 0, len(r.App.Actions))
		for _, action := range r.App.Actions {
			actions = append(actions, map[string]any{
				"name":       action.Name,
				"inputAlias": action.InputAlias,
				"steps":      len(action.Steps),
			})
		}
		payload["actions"] = actions
	}
	cfg := r.authConfig()
	needsBootstrap := false
	if totalUsers, err := r.countAuthUsers(requestID); err == nil {
		needsBootstrap = totalUsers == 0
	}
	payload["auth"] = map[string]any{
		"enabled":        true,
		"userEntity":     "User",
		"emailField":     cfg.EmailField,
		"roleField":      cfg.RoleField,
		"emailTransport": cfg.EmailTransport,
		"needsBootstrap": needsBootstrap,
	}
	return payload
}
