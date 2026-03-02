package runtime

func (r *Runtime) schemaPayload() map[string]any {
	entities := make([]map[string]any, 0, len(r.App.Entities))
	for _, entity := range r.App.Entities {
		fields := make([]map[string]any, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			fields = append(fields, map[string]any{
				"name":     field.Name,
				"type":     field.Type,
				"primary":  field.Primary,
				"auto":     field.Auto,
				"optional": field.Optional,
			})
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
	if r.authEnabled() {
		payload["auth"] = map[string]any{
			"enabled":        true,
			"userEntity":     r.App.Auth.UserEntity,
			"emailField":     r.App.Auth.EmailField,
			"roleField":      r.App.Auth.RoleField,
			"emailTransport": r.App.Auth.EmailTransport,
		}
	}
	return payload
}
