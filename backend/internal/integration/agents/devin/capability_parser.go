package devin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

// parseModelCatalog parses the output of `devin models list --format json` into
// agent.Capabilities. The exact JSON shape requires authentication to observe
// (see Phase 0.5 of the plan), so this parser handles common shapes
// conservatively and falls back gracefully.
//
// Supported shapes:
//   - Array of model objects: [{"id":"...","name":"..."}, ...]
//   - Object with "models" array: {"models":[...], "default":"..."}
//   - Object with "data" array: {"data":[...]}
//
// Each model object may use any of these field names for its identifier:
// id, name, model, value. For its display label: name, displayName, label, title.
func parseModelCatalog(raw []byte) (agent.Capabilities, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return fallbackCapabilities(), nil
	}

	models, defaultID, err := extractModels(raw)
	if err != nil {
		return agent.Capabilities{}, err
	}

	caps := agent.Capabilities{
		Provider:    agent.ProviderDevin,
		Label:       "Devin",
		Source:      agent.CapabilitySourceLive,
		Models:      agent.WithAutoModel(models, "Devin default"),
		Modes:       agent.ProviderModes(true),
		DefaultMode: agent.RunModeDefault,
	}
	if defaultID != "" {
		for i := range caps.Models {
			if caps.Models[i].ID == defaultID {
				caps.Models[i].ProviderDefault = true
			}
		}
	}
	return caps, nil
}

func extractModels(raw []byte) ([]agent.ModelCapability, string, error) {
	// Try array first: [{"id":"..."}, ...]
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		models := parseModelItems(items, "")
		return models, "", nil
	}

	// Try object with known array keys.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, "", fmt.Errorf("decode devin model catalog: %w", err)
	}

	defaultID := ""
	if rawDefault, ok := root["default"]; ok {
		_ = json.Unmarshal(rawDefault, &defaultID)
		defaultID = strings.TrimSpace(defaultID)
	}
	if rawDefault, ok := root["defaultModel"]; ok {
		var dm string
		if json.Unmarshal(rawDefault, &dm) == nil && dm != "" {
			defaultID = strings.TrimSpace(dm)
		}
	}

	for _, key := range []string{"models", "data", "items"} {
		if rawArray, ok := root[key]; ok && len(rawArray) > 0 && string(rawArray) != "null" {
			if err := json.Unmarshal(rawArray, &items); err != nil {
				continue
			}
			models := parseModelItems(items, defaultID)
			return models, defaultID, nil
		}
	}

	// No models found — return empty (caller will use auto model).
	return nil, defaultID, nil
}

func parseModelItems(items []json.RawMessage, defaultID string) []agent.ModelCapability {
	models := make([]agent.ModelCapability, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		var obj map[string]json.RawMessage
		if json.Unmarshal(item, &obj) != nil {
			continue
		}
		id := rawString(obj, "id", "name", "model", "value")
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := rawString(obj, "name", "displayName", "label", "title")
		if label == "" {
			label = id
		}
		description := rawString(obj, "description", "details")
		models = append(models, agent.ModelCapability{
			ID:              id,
			Label:           label,
			Description:     description,
			ProviderDefault: id == defaultID,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func rawString(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}
