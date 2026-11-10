package publicview

import "github.com/fleeto/readyspec/internal/runstore"

type DisplayItem struct {
	RoleID       string `json:"role_id"`
	ItemID       string `json:"item_id"`
	ItemName     string `json:"item_name"`
	TemplateName string `json:"template_name"`
	Importance   string `json:"importance,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type DisplayMetadata struct {
	RunID      string        `json:"run_id"`
	PlanDigest string        `json:"plan_digest"`
	Items      []DisplayItem `json:"items"`
}

// DisplayMetadataValue projects names from the Run's immutable plan, never from
// the current registry or editable draft. Report bytes remain unchanged.
func DisplayMetadataValue(run runstore.Run) DisplayMetadata {
	result := DisplayMetadata{RunID: run.Acceptance.RunID, PlanDigest: run.Acceptance.PlanDigest, Items: []DisplayItem{}}
	if run.Plan == nil {
		return result
	}
	for _, role := range run.Plan.Roles {
		for _, item := range role.Items {
			entry := DisplayItem{RoleID: role.RoleID, ItemID: item.ItemID, ItemName: item.Name, Importance: item.Importance, Enabled: item.Enabled}
			for _, snapshot := range run.Plan.EmbeddedTemplates {
				if snapshot.TemplateID == item.TemplateID && snapshot.Version == item.TemplateVersion && snapshot.Digest == item.TemplateDigest {
					entry.TemplateName = snapshot.Name
					break
				}
			}
			result.Items = append(result.Items, entry)
		}
	}
	return result
}
