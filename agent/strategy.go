package agent

import (
	"math"
	"strings"
	"x10/index"
)

// ModelStrategy determines which model to use based on task complexity.
type ModelStrategy struct {
	SimpleModel    string  // e.g., "gpt-4o-mini"
	StandardModel  string  // e.g., "claude-3-5-sonnet"
	PremiumModel   string  // e.g., "claude-opus"
	SimpleThreshold    float64 // < this score uses simple model
	StandardThreshold  float64 // < this score uses standard model, else premium
}

// DefaultStrategy provides sensible defaults using Anthropic models.
func DefaultStrategy() ModelStrategy {
	return ModelStrategy{
		SimpleModel:       "claude-haiku-4-5-20251001",
		StandardModel:     "claude-sonnet-4-6",
		PremiumModel:      "claude-opus-4-6",
		SimpleThreshold:   0.3,
		StandardThreshold: 0.7,
	}
}

// SelectModel chooses a model based on task complexity.
// Returns the recommended model name.
func (s ModelStrategy) SelectModel(task string, idx *index.Index) string {
	if idx == nil {
		// No index: can't estimate complexity, use standard
		return s.StandardModel
	}

	complexity := EstimateComplexity(task, idx)

	if complexity < s.SimpleThreshold {
		return s.SimpleModel
	}
	if complexity < s.StandardThreshold {
		return s.StandardModel
	}
	return s.PremiumModel
}

// EstimateComplexity analyzes the task and codebase to estimate required model power.
// Returns a score 0.0-1.0 where:
//   0.0-0.3: simple (gpt-4o-mini sufficient)
//   0.3-0.7: standard (claude-3.5-sonnet appropriate)
//   0.7-1.0: complex (claude-opus recommended)
func EstimateComplexity(task string, idx *index.Index) float64 {
	if idx == nil {
		return 0.5 // default to standard
	}

	// Find matching symbols
	symbols, _ := idx.Search(task, 20)
	if len(symbols) == 0 {
		// No symbols found: likely simple documentation question
		return 0.1
	}

	// Count unique files involved
	fileSet := make(map[string]bool)
	var totalLines int
	for _, s := range symbols {
		fileSet[s.File] = true
		totalLines += (s.EndLine - s.StartLine + 1)
	}
	uniqueFiles := len(fileSet)

	// Task keywords that suggest complexity
	complexityKeywords := map[string]int{
		"refactor": 3,
		"redesign": 3,
		"architecture": 3,
		"optimize": 2,
		"performance": 2,
		"security": 2,
		"concurrent": 3,
		"parallel": 3,
		"async": 2,
		"integration": 2,
		"test": 1,
		"fix": 0,
		"bug": 1,
		"error": 1,
		"simple": -1,
		"quick": -1,
	}

	taskScore := 0
	lowerTask := strings.ToLower(task)
	for keyword, weight := range complexityKeywords {
		if strings.Contains(lowerTask, keyword) {
			taskScore += weight
		}
	}

	// Normalize components:
	// 1. Number of files (0-1 scale, capped at 10 files = 1.0)
	fileScore := math.Min(1.0, float64(uniqueFiles)/10.0)

	// 2. Amount of code touched (0-1 scale, capped at 500 lines = 1.0)
	codeScore := math.Min(1.0, float64(totalLines)/500.0)

	// 3. Task keywords (0-1 scale, capped at +3 complexity = 1.0)
	keywordScore := math.Min(1.0, float64(taskScore)/3.0)

	// Weighted average: files (30%), code (30%), keywords (40%)
	complexity := 0.3*fileScore + 0.3*codeScore + 0.4*keywordScore

	return math.Min(1.0, math.Max(0.0, complexity))
}

// ContextBuilderWithGraph creates a context builder that uses dependency graphs.
// This builder:
// 1. Builds a minimal context graph (entry points + 2-hop closure)
// 2. Formats it as relevant symbols for the LLM
// 3. Stays under a token budget to keep context lean
func ContextBuilderWithGraph(idx *index.Index, maxTokens int, cache *index.GraphCache) func(task string) string {
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	return func(task string) string {
		if idx == nil {
			return ""
		}

		taskHash := index.HashString(task)

		// Check cache first
		if cache != nil {
			if cachedGraph, ok := cache.Get(taskHash); ok {
				return cachedGraph.Format()
			}
		}

		// Build graph (fast if index is hot)
		graph := idx.BuildContextGraph(task, maxTokens)

		// Cache the result
		if cache != nil {
			cache.Set(taskHash, graph)
		}

		return graph.Format()
	}
}

// Helper to export hashString
func HashString(s string) string {
	return index.HashString(s)
}
