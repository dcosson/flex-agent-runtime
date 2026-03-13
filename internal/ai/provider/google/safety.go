package google

import (
	"fmt"
	"strings"
)

// defaultSafetySettings returns safety settings with all categories set to OFF.
// For agent tool use, we disable safety filters by default so the agent can
// process all content without interruption.
func defaultSafetySettings() []safetySetting {
	categories := []string{
		"HARM_CATEGORY_HARASSMENT",
		"HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT",
		"HARM_CATEGORY_DANGEROUS_CONTENT",
		"HARM_CATEGORY_CIVIC_INTEGRITY",
	}
	settings := make([]safetySetting, len(categories))
	for i, cat := range categories {
		settings[i] = safetySetting{Category: cat, Threshold: "OFF"}
	}
	return settings
}

// isSafetyBlock returns true if the finish reason indicates a safety block.
func isSafetyBlock(finishReason string) bool {
	switch finishReason {
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return true
	default:
		return false
	}
}

// formatSafetyRatings returns a human-readable string of blocked safety categories.
func formatSafetyRatings(ratings []safetyRating) string {
	var blocked []string
	for _, r := range ratings {
		if r.Blocked {
			blocked = append(blocked, fmt.Sprintf("%s (%s)", r.Category, r.Probability))
		}
	}
	if len(blocked) == 0 {
		return "unknown safety block"
	}
	return "blocked by: " + strings.Join(blocked, ", ")
}
