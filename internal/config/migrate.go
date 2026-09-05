package config

import (
	"encoding/json"
	"fmt"
)

func migrateModelProfiles(data []byte, version int) (bool, []byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil, err
	}
	var servers []map[string]json.RawMessage
	if value := raw["servers"]; value != nil {
		if err := json.Unmarshal(value, &servers); err != nil {
			return false, nil, fmt.Errorf("migrate servers: %w", err)
		}
	}
	changed := false
	if version < 5 {
		mainID, err := legacyMainProfile(data)
		if err != nil {
			return false, nil, err
		}
		raw["roles"], _ = json.Marshal(Roles{Main: mainID})
		for _, profile := range servers {
			var context map[string]json.RawMessage
			if value := profile["context"]; value != nil {
				if err := json.Unmarshal(value, &context); err != nil {
					return false, nil, fmt.Errorf("migrate profile context: %w", err)
				}
			} else {
				context = map[string]json.RawMessage{}
			}
			if context["n_ctx"] == nil {
				nctx := 0
				if value := context["n_ctx_override"]; value != nil {
					if err := json.Unmarshal(value, &nctx); err != nil {
						return false, nil, fmt.Errorf("migrate profile n_ctx_override: %w", err)
					}
				}
				if nctx == 0 {
					var capabilities struct {
						NCtx int `json:"n_ctx"`
					}
					if value := profile["capabilities"]; value != nil {
						if err := json.Unmarshal(value, &capabilities); err != nil {
							return false, nil, fmt.Errorf("migrate profile capabilities: %w", err)
						}
					}
					nctx = capabilities.NCtx
				}
				context["n_ctx"], _ = json.Marshal(nctx)
			}
			delete(context, "n_ctx_override")
			profile["context"], _ = json.Marshal(context)
		}
		changed = true
	}
	for _, profile := range servers {
		key, present := profile["api_key"]
		if !present {
			continue
		}
		var value string
		if err := json.Unmarshal(key, &value); err != nil {
			return false, nil, fmt.Errorf("migrate profile api_key: %w", err)
		}
		if value != "" && profile["credential"] == nil {
			var id string
			if err := json.Unmarshal(profile["id"], &id); err != nil {
				return false, nil, fmt.Errorf("migrate profile id: %w", err)
			}
			profile["credential"], _ = json.Marshal(id)
		}
		changed = true
	}
	if !changed {
		return false, data, nil
	}
	raw["servers"], _ = json.Marshal(servers)
	raw["config_version"], _ = json.Marshal(CurrentConfigVersion)
	out, err := json.Marshal(raw)
	return true, out, err
}

func legacyMainProfile(data []byte) (string, error) {
	var value struct {
		Servers []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			BaseURL string `json:"base_url"`
			Model   string `json:"model"`
			Context struct {
				NCtxOverride int `json:"n_ctx_override"`
			} `json:"context"`
			Capabilities Capabilities `json:"capabilities"`
		} `json:"servers"`
		Context GlobalContext `json:"context"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("select migrated main profile: %w", err)
	}
	if len(value.Servers) == 0 {
		return "", nil
	}
	for _, profile := range value.Servers {
		nctx := profile.Capabilities.NCtx
		if profile.Context.NCtxOverride > 0 {
			nctx = profile.Context.NCtxOverride
		}
		if profile.BaseURL != "" && profile.Model != "" && nctx > 0 && profile.Capabilities.ToolCalls && profile.Capabilities.Streaming && profile.Capabilities.OverflowBehavior != "truncate" && (value.Context.Accounting != "exact" || profile.Capabilities.Tokenize) {
			return profile.ID, nil
		}
	}
	return value.Servers[0].ID, nil
}

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
		if err := json.Unmarshal(v, &profile.Sampling.Thinking); err != nil {
			return false, nil, fmt.Errorf("migrate sampling_thinking: %w", err)
		}
	}
	if v := raw["sampling_nonthinking"]; v != nil {
		if err := json.Unmarshal(v, &profile.Sampling.Nonthinking); err != nil {
			return false, nil, fmt.Errorf("migrate sampling_nonthinking: %w", err)
		}
	}
	if v := raw["thinking"]; v != nil {
		if err := json.Unmarshal(v, &profile.Reasoning); err != nil {
			return false, nil, fmt.Errorf("migrate thinking: %w", err)
		}
	}
	if v := raw["context"]; v != nil {
		var old struct {
			NCtxOverride  int     `json:"n_ctx_override"`
			ReserveOutput int     `json:"reserve_output"`
			SoftPct       float64 `json:"soft_pct"`
			SummaryPct    float64 `json:"summary_pct"`
			Accounting    string  `json:"accounting"`
		}
		if err := json.Unmarshal(v, &old); err != nil {
			return false, nil, fmt.Errorf("migrate context: %w", err)
		}
		profile.Context.NCtx = old.NCtxOverride
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
