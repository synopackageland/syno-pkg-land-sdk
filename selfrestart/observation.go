package selfrestart

import "encoding/json"

// Only retain bounded, normalized outcomes, never raw DSM output or sessions.
func classify(policy Policy, observation Observation) (string, string) {
	if len(observation.Bulk) > 32<<10 || len(observation.Current) > 8<<10 {
		return "unknown", "invalid-observation"
	}
	var bulk struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
		Results []struct {
			Package  string `json:"package"`
			Version  string `json:"version"`
			Action   string `json:"action"`
			Stage    string `json:"stage"`
			Finished bool   `json:"finished"`
			Success  bool   `json:"success"`
			Error    struct {
				Code int `json:"code"`
			} `json:"error"`
		} `json:"results"`
	}
	if json.Unmarshal(observation.Bulk, &bulk) != nil || len(bulk.Results) != 2 {
		return "unknown", "native-result-unavailable"
	}
	for i, r := range bulk.Results {
		want := []string{"stop", "start"}[i]
		if r.Package != policy.Package || r.Version != policy.Version || r.Action != want {
			return "unknown", "native-result-mismatch"
		}
		if !r.Finished {
			return "unknown", "native-result-incomplete"
		}
		if !r.Success || r.Error.Code != 0 {
			if i == 0 {
				return "failed", "stop-failed"
			}
			return "failed", "start-failed"
		}
		if r.Stage != []string{"stopped", "started"}[i] {
			return "unknown", "native-result-incomplete"
		}
	}
	if !bulk.Success || bulk.Error.Code != 0 {
		return "unknown", "native-batch-failed"
	}
	var current struct {
		ID         string `json:"id"`
		Version    string `json:"version"`
		Additional struct {
			Status string `json:"status"`
		} `json:"additional"`
	}
	if json.Unmarshal(observation.Current, &current) != nil || current.ID != policy.Package || current.Version != policy.Version || current.Additional.Status != "running" {
		return "unknown", "running-unconfirmed"
	}
	return "confirmed-running", ""
}
