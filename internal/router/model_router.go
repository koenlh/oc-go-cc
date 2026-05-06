// Package router defines HTTP route registration and middleware chaining,
// as well as model selection based on request scenarios.
package router

import (
	"fmt"
	"sort"
	"strings"

	"oc-go-cc/internal/config"
)

const (
	SelectionModeScenario       = "scenario"
	SelectionModeRequestedModel = "requested_model"
	SelectionModeClaudeCategory = "claude_category"
)

// ModelRouter handles model selection based on scenarios.
type ModelRouter struct {
	config *config.Config
}

// NewModelRouter creates a new model router.
func NewModelRouter(cfg *config.Config) *ModelRouter {
	return &ModelRouter{config: cfg}
}

// RouteResult contains the selected model and fallback chain.
type RouteResult struct {
	Primary           config.ModelConfig
	Fallbacks         []config.ModelConfig
	Scenario          Scenario
	SelectionMode     string
	RequestedModel    string
	RequestedCategory string
	Reason            string
}

// Route determines which model to use for a request.
func (r *ModelRouter) Route(messages []MessageContent, tokenCount int) (RouteResult, error) {
	return r.RouteWithRequestModel("", messages, tokenCount)
}

// RouteWithRequestModel determines which model to use for a request, preferring
// an explicit Claude Code category mapping when the incoming model name matches.
func (r *ModelRouter) RouteWithRequestModel(requestModel string, messages []MessageContent, tokenCount int) (RouteResult, error) {
	if result, ok := r.routeByRequestedModel(requestModel); ok {
		return result, nil
	}
	if result, ok := r.routeByClaudeCategory(requestModel); ok {
		return result, nil
	}

	result := DetectScenario(messages, tokenCount, r.config)

	// Get primary model for scenario
	primary, ok := r.config.Models[string(result.Scenario)]
	if !ok {
		// Fall back to default if scenario model not configured
		primary, ok = r.config.Models["default"]
		if !ok {
			return RouteResult{}, fmt.Errorf("no default model configured")
		}
	}

	// Get fallbacks for scenario
	fallbacks := r.config.Fallbacks[string(result.Scenario)]
	if len(fallbacks) == 0 {
		// Fall back to default fallbacks
		fallbacks = r.config.Fallbacks["default"]
	}

	return RouteResult{
		Primary:       primary,
		Fallbacks:     fallbacks,
		Scenario:      result.Scenario,
		SelectionMode: SelectionModeScenario,
		Reason:        result.Reason,
	}, nil
}

// GetModelChain returns the full chain of models to try (primary + fallbacks).
func (rr *RouteResult) GetModelChain() []config.ModelConfig {
	chain := []config.ModelConfig{rr.Primary}
	chain = append(chain, rr.Fallbacks...)
	return chain
}

// RouteForStreaming determines which model to use for streaming requests.
// Prioritizes fast TTFT (time-to-first-token) over capability.
func (r *ModelRouter) RouteForStreaming(messages []MessageContent, tokenCount int) RouteResult {
	return r.RouteForStreamingWithRequestModel("", messages, tokenCount)
}

// RouteForStreamingWithRequestModel determines which model to use for streaming requests,
// preferring an explicit Claude Code category mapping when the incoming model name matches.
func (r *ModelRouter) RouteForStreamingWithRequestModel(requestModel string, messages []MessageContent, tokenCount int) RouteResult {
	if result, ok := r.routeByRequestedModel(requestModel); ok {
		return result
	}
	if result, ok := r.routeByClaudeCategory(requestModel); ok {
		return result
	}

	result := RouteForStreaming(messages, tokenCount, r.config)

	// Get primary model for scenario
	primary, ok := r.config.Models[string(result.Scenario)]
	if !ok {
		// Fall back to fast scenario if not configured
		primary, ok = r.config.Models["fast"]
		if !ok {
			// Fall back to default
			primary = r.config.Models["default"]
		}
	}

	// Get fallbacks for scenario
	fallbacks := r.config.Fallbacks[string(result.Scenario)]
	if len(fallbacks) == 0 {
		// Fall back to fast fallbacks
		fallbacks = r.config.Fallbacks["fast"]
	}

	return RouteResult{
		Primary:       primary,
		Fallbacks:     fallbacks,
		Scenario:      result.Scenario,
		SelectionMode: SelectionModeScenario,
		Reason:        result.Reason,
	}
}

func (r *ModelRouter) routeByRequestedModel(requestModel string) (RouteResult, bool) {
	if r == nil || r.config == nil {
		return RouteResult{}, false
	}

	requestedModel := strings.TrimSpace(requestModel)
	if requestedModel == "" {
		return RouteResult{}, false
	}

	if primary, fallbacks, ok := r.findConfiguredRequestedModel(requestedModel); ok {
		return RouteResult{
			Primary:        primary,
			Fallbacks:      fallbacks,
			SelectionMode:  SelectionModeRequestedModel,
			RequestedModel: requestedModel,
			Reason:         fmt.Sprintf("requested model %q matched full model name %s", requestModel, primary.ModelID),
		}, true
	}

	if !LooksLikeDirectModelID(requestedModel) {
		return RouteResult{}, false
	}

	return RouteResult{
		Primary: config.ModelConfig{
			Provider: "opencode-go",
			ModelID:  requestedModel,
		},
		SelectionMode:  SelectionModeRequestedModel,
		RequestedModel: requestedModel,
		Reason:         fmt.Sprintf("requested model %q used direct full model pass-through", requestModel),
	}, true
}

func (r *ModelRouter) routeByClaudeCategory(requestModel string) (RouteResult, bool) {
	if r == nil || r.config == nil || requestModel == "" {
		return RouteResult{}, false
	}

	category := NormalizeClaudeCategory(requestModel)
	if category == "" {
		return RouteResult{}, false
	}

	primary, ok := r.config.ClaudeCode.Models[category]
	if !ok || primary.ModelID == "" {
		return RouteResult{}, false
	}

	return RouteResult{
		Primary:           primary,
		Fallbacks:         r.config.ClaudeCode.Fallbacks[category],
		SelectionMode:     SelectionModeClaudeCategory,
		RequestedModel:    requestModel,
		RequestedCategory: category,
		Reason:            fmt.Sprintf("requested model %q matched Claude category %s", requestModel, category),
	}, true
}

func (r *ModelRouter) findConfiguredRequestedModel(requestModel string) (config.ModelConfig, []config.ModelConfig, bool) {
	if primary, fallbacks, ok := findModelInConfigMapByModelID(r.config.Models, r.config.Fallbacks, requestModel); ok {
		return primary, fallbacks, true
	}
	if primary, fallbacks, ok := findModelInClaudeCategoryMapByModelID(r.config.ClaudeCode.Models, r.config.ClaudeCode.Fallbacks, requestModel); ok {
		return primary, fallbacks, true
	}
	if primary, fallbacks, ok := findModelInFallbackMapByModelID(r.config.Fallbacks, requestModel); ok {
		return primary, fallbacks, true
	}
	if primary, fallbacks, ok := findModelInClaudeCategoryFallbacksByModelID(r.config.ClaudeCode.Fallbacks, requestModel); ok {
		return primary, fallbacks, true
	}
	return config.ModelConfig{}, nil, false
}

func findModelInConfigMapByModelID(models map[string]config.ModelConfig, fallbacks map[string][]config.ModelConfig, requestModel string) (config.ModelConfig, []config.ModelConfig, bool) {
	for _, key := range sortedModelKeys(models) {
		model := models[key]
		if strings.EqualFold(model.ModelID, requestModel) {
			return model, fallbacks[key], true
		}
	}
	return config.ModelConfig{}, nil, false
}

func findModelInClaudeCategoryMapByModelID(models map[string]config.ModelConfig, fallbacks map[string][]config.ModelConfig, requestModel string) (config.ModelConfig, []config.ModelConfig, bool) {
	for _, category := range orderedClaudeCategories() {
		model, ok := models[category]
		if ok && strings.EqualFold(model.ModelID, requestModel) {
			return model, fallbacks[category], true
		}
	}
	return config.ModelConfig{}, nil, false
}

func findModelInFallbackMapByModelID(fallbacks map[string][]config.ModelConfig, requestModel string) (config.ModelConfig, []config.ModelConfig, bool) {
	for _, key := range sortedFallbackKeys(fallbacks) {
		models := fallbacks[key]
		for i, model := range models {
			if strings.EqualFold(model.ModelID, requestModel) {
				return model, models[i+1:], true
			}
		}
	}
	return config.ModelConfig{}, nil, false
}

func findModelInClaudeCategoryFallbacksByModelID(fallbacks map[string][]config.ModelConfig, requestModel string) (config.ModelConfig, []config.ModelConfig, bool) {
	for _, category := range orderedClaudeCategories() {
		models := fallbacks[category]
		for i, model := range models {
			if strings.EqualFold(model.ModelID, requestModel) {
				return model, models[i+1:], true
			}
		}
	}
	return config.ModelConfig{}, nil, false
}

func orderedClaudeCategories() []string {
	return []string{
		string(config.ClaudeCategoryHaiku),
		string(config.ClaudeCategorySonnet),
		string(config.ClaudeCategoryOpus),
	}
}

func sortedModelKeys(models map[string]config.ModelConfig) []string {
	keys := make([]string, 0, len(models))
	for key := range models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedFallbackKeys(fallbacks map[string][]config.ModelConfig) []string {
	keys := make([]string, 0, len(fallbacks))
	for key := range fallbacks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func LooksLikeDirectModelID(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return false
	}
	if NormalizeClaudeCategory(trimmed) != "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "claude") {
		return false
	}
	return strings.ContainsAny(trimmed, "-.")
}

// NormalizeClaudeCategory maps an incoming Claude model name to haiku, sonnet, or opus.
func NormalizeClaudeCategory(modelName string) string {
	lower := strings.ToLower(modelName)
	switch {
	case strings.Contains(lower, string(config.ClaudeCategoryHaiku)):
		return string(config.ClaudeCategoryHaiku)
	case strings.Contains(lower, string(config.ClaudeCategorySonnet)):
		return string(config.ClaudeCategorySonnet)
	case strings.Contains(lower, string(config.ClaudeCategoryOpus)):
		return string(config.ClaudeCategoryOpus)
	default:
		return ""
	}
}
