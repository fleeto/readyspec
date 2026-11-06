package compiler

import "encoding/json"

// routeEvaluation validates the single route returned by `ip -j route get`
// and records the selected policy table without leaking unrelated addresses.
func routeEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"target": params["target"]}
	var routes []struct {
		Device  string `json:"dev"`
		Gateway string `json:"gateway"`
		Source  string `json:"prefsrc"`
		From    string `json:"from"`
		Table   string `json:"table"`
	}
	if err := json.Unmarshal([]byte(text), &routes); err != nil || len(routes) != 1 || routes[0].Device == "" {
		summary["error"] = "INVALID_ROUTE_OBSERVATION"
		return "ERROR", summary
	}
	route := routes[0]
	summary["interface"] = route.Device
	if route.Gateway != "" {
		summary["gateway"] = route.Gateway
	}
	if route.Source != "" {
		summary["source"] = route.Source
	}
	if route.From != "" {
		summary["requested_source"] = route.From
	}
	if route.Table != "" {
		summary["table"] = route.Table
	}
	if route.Device != params["interface"] {
		return "FAIL", summary
	}
	if gateway, ok := params["gateway"]; ok && route.Gateway != gateway {
		return "FAIL", summary
	}
	return "PASS", summary
}
