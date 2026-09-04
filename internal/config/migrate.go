package config

import "encoding/json"

func migrateByteWindows(data []byte, version int) (bool, []byte, error) {
	if version >= 4 {
		return false, data, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil, err
	}
	var tools map[string]json.RawMessage
	if value, ok := raw["tools"]; ok {
		if err := json.Unmarshal(value, &tools); err != nil {
			return false, nil, err
		}
	} else {
		tools = map[string]json.RawMessage{}
	}
	for _, name := range []string{"read_file", "fetch"} {
		var settings map[string]json.RawMessage
		value, ok := tools[name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(value, &settings); err != nil {
			return false, nil, err
		}
		settings["default_limit"], _ = json.Marshal(16 << 10)
		settings["max_limit"], _ = json.Marshal(64 << 10)
		delete(settings, "max_line_chars")
		tools[name], _ = json.Marshal(settings)
	}
	raw["tools"], _ = json.Marshal(tools)
	raw["config_version"], _ = json.Marshal(CurrentConfigVersion)
	out, err := json.Marshal(raw)
	return true, out, err
}

func migrateOperatorIdleTimeout(data []byte, version int) (bool, bool, []byte, error) {
	if version == CurrentConfigVersion {
		return false, false, data, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, false, nil, err
	}
	remapped := false
	if shellRaw := raw["shell"]; shellRaw != nil {
		var shell map[string]json.RawMessage
		if err := json.Unmarshal(shellRaw, &shell); err != nil {
			return false, false, nil, err
		}
		if old, present := shell["operator_context_timeout_minutes"]; present {
			if _, alreadyPresent := shell["operator_context_idle_timeout_minutes"]; !alreadyPresent {
				shell["operator_context_idle_timeout_minutes"] = old
			}
			delete(shell, "operator_context_timeout_minutes")
			remapped = true
		}
		raw["shell"], _ = json.Marshal(shell)
	}
	raw["config_version"], _ = json.Marshal(CurrentConfigVersion)
	out, err := json.Marshal(raw)
	return true, remapped, out, err
}

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
