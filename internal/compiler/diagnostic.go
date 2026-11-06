package compiler

import "sort"

type Diagnostic struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	RoleID       string   `json:"role_id,omitempty"`
	ItemID       string   `json:"item_id,omitempty"`
	Resource     string   `json:"resource,omitempty"`
	Property     string   `json:"property,omitempty"`
	RelatedItems []string `json:"related_items,omitempty"`
}

func (d *Diagnostic) Error() string { return d.Code + ": " + d.Message }

func conflict(role, item, resource, property string, origins []Origin) *Diagnostic {
	seen := map[string]bool{item: true}
	for _, origin := range origins {
		if origin.Resource == resource && origin.Property == property {
			seen[origin.ItemID] = true
		}
	}
	items := make([]string, 0, len(seen))
	for id := range seen {
		items = append(items, id)
	}
	sort.Strings(items)
	return &Diagnostic{Code: "RESOURCE_CONFLICT", Message: "同一资源属性存在不同要求", RoleID: role, ItemID: item, Resource: resource, Property: property, RelatedItems: items}
}
