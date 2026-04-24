package runtime

import "mar/internal/model"

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
			if len(field.EnumValues) > 0 {
				fieldPayload["enumValues"] = field.EnumValues
			}
			if field.RelationEntity != "" {
				fieldPayload["relationEntity"] = field.RelationEntity
			}
			if field.CurrentUser {
				fieldPayload["currentUser"] = true
			}
			if field.Default != nil {
				fieldPayload["default"] = field.Default
			}
			fields = append(fields, fieldPayload)
		}
		uniqueConstraints := make([]map[string]any, 0, len(entity.Unique))
		for _, constraint := range entity.Unique {
			uniqueConstraints = append(uniqueConstraints, map[string]any{
				"fields": constraint.Fields,
			})
		}
		entities = append(entities, map[string]any{
			"name":       entity.Name,
			"table":      entity.Table,
			"resource":   entity.Resource,
			"primaryKey": entity.PrimaryKey,
			"fields":     fields,
			"unique":     uniqueConstraints,
		})
	}

	payload := map[string]any{
		"appName":  r.App.AppName,
		"port":     r.App.Port,
		"database": r.App.Database,
		"entities": entities,
	}
	if len(r.App.Records) > 0 {
		records := make([]map[string]any, 0, len(r.App.Records))
		for _, record := range r.App.Records {
			fields := make([]map[string]any, 0, len(record.Fields))
			for _, field := range record.Fields {
				fields = append(fields, map[string]any{
					"name": field.Name,
					"type": field.Type,
				})
			}
			records = append(records, map[string]any{
				"name":   record.Name,
				"fields": fields,
			})
		}
		payload["records"] = records
	}
	if len(r.App.Types) > 0 {
		types := make([]map[string]any, 0, len(r.App.Types))
		for _, typ := range r.App.Types {
			variants := make([]map[string]any, 0, len(typ.Variants))
			for _, variant := range typ.Variants {
				fields := make([]map[string]any, 0, len(variant.Fields))
				for _, field := range variant.Fields {
					fields = append(fields, map[string]any{
						"name": field.Name,
						"type": field.Type,
					})
				}
				variants = append(variants, map[string]any{
					"name":   variant.Name,
					"fields": fields,
				})
			}
			types = append(types, map[string]any{
				"name":     typ.Name,
				"variants": variants,
			})
		}
		payload["types"] = types
	}
	if len(r.App.InputAliases) > 0 {
		aliases := make([]map[string]any, 0, len(r.App.InputAliases))
		for _, alias := range r.App.InputAliases {
			fields := make([]map[string]any, 0, len(alias.Fields))
			for _, field := range alias.Fields {
				fieldPayload := map[string]any{
					"name": field.Name,
					"type": field.Type,
				}
				if len(field.EnumValues) > 0 {
					fieldPayload["enumValues"] = field.EnumValues
				}
				if field.RelationEntity != "" {
					fieldPayload["relationEntity"] = field.RelationEntity
				}
				fields = append(fields, fieldPayload)
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
				"path":       model.PublicActionPath(action.Name),
				"inputAlias": action.InputAlias,
				"steps":      len(action.Steps),
			})
		}
		payload["actions"] = actions
	}
	if len(r.App.Queries) > 0 {
		queries := make([]map[string]any, 0, len(r.App.Queries))
		for _, query := range r.App.Queries {
			queries = append(queries, map[string]any{
				"name":           query.Name,
				"path":           model.PublicQueryPath(query.Name),
				"parameters":     query.Parameters,
				"parameterTypes": query.ParameterTypes,
				"entity":         query.Entity,
			})
		}
		payload["queries"] = queries
	}
	if r.App.Screens != nil {
		payload["screens"] = r.App.Screens
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
		"needsBootstrap": needsBootstrap,
	}
	return payload
}
