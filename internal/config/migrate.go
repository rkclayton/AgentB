package config

import "encoding/json"

func migrateV1(data []byte) (bool, []byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil, err
	}
	serverRaw, ok := raw["server"]
	if !ok {
		return false, data, nil
	}
	base := Defaults("sandbox")
	profile := base.Servers[0]
	var server struct {
		Label           string `json:"label"`
		BaseURL         string `json:"base_url"`
		Model           string `json:"model"`
		APIKey          string `json:"api_key"`
		RequestTimeoutS int    `json:"request_timeout_s"`
		ProbeMode       string `json:"probe_mode"`
	}
	if err := json.Unmarshal(serverRaw, &server); err != nil {
		return false, nil, err
	}
	if server.Label != "" {
		profile.Label = server.Label
	}
	if server.BaseURL != "" {
		profile.BaseURL = server.BaseURL
	}
	if server.Model != "" {
		profile.Model = server.Model
	}
	profile.APIKey = server.APIKey
	if server.RequestTimeoutS > 0 {
		profile.RequestTimeoutS = server.RequestTimeoutS
	}
	if server.ProbeMode != "" {
		profile.ProbeMode = server.ProbeMode
	}
	if v := raw["sampling_thinking"]; v != nil {
		_ = json.Unmarshal(v, &profile.Sampling.Thinking)
	}
	if v := raw["sampling_nonthinking"]; v != nil {
		_ = json.Unmarshal(v, &profile.Sampling.Nonthinking)
	}
	if v := raw["thinking"]; v != nil {
		_ = json.Unmarshal(v, &profile.Reasoning)
	}
	if v := raw["context"]; v != nil {
		var old struct {
			NCtxOverride  int     `json:"n_ctx_override"`
			ReserveOutput int     `json:"reserve_output"`
			SoftPct       float64 `json:"soft_pct"`
			SummaryPct    float64 `json:"summary_pct"`
			Accounting    string  `json:"accounting"`
		}
		_ = json.Unmarshal(v, &old)
		profile.Context.NCtxOverride = old.NCtxOverride
		if old.ReserveOutput > 0 {
			profile.Context.ReserveOutput = old.ReserveOutput
		}
		raw["context"], _ = json.Marshal(GlobalContext{SoftPct: old.SoftPct, SummaryPct: old.SummaryPct, Accounting: old.Accounting})
	}
	var cfg Config
	delete(raw, "server")
	delete(raw, "sampling_thinking")
	delete(raw, "sampling_nonthinking")
	delete(raw, "thinking")
	clean, _ := json.Marshal(raw)
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return false, nil, err
	}
	cfg.Servers = []Profile{profile}
	applyDefaults(&cfg)
	out, err := json.Marshal(cfg)
	return true, out, err
}
