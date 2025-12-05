//revive:disable-next-line:var-naming
package common

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

var (
	ErrChatNotFound      = errors.New("chat not found")
	ErrSessionNotFound   = errors.New("session not found")
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrInternal          = errors.New("internal server error")
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidUUID       = errors.New("invalid UUID")
	ErrItineraryNotFound = errors.New("itinerary not found")
)

// ContentCounts represents counts of different content types found in responses
type ContentCounts struct {
	POIs         int
	Hotels       int
	Restaurants  int
	HasItinerary bool
	Categories   []string
}

// CountContentFromResponse analyzes a response to count different content types
func CountContentFromResponse(response string) ContentCounts {
	counts := ContentCounts{
		Categories: make([]string, 0),
	}

	// Try to parse as JSON first
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(response), &jsonData); err == nil {
		// Handle JSON response
		if pois, ok := jsonData["points_of_interest"].([]interface{}); ok {
			counts.POIs = len(pois)
			counts.Categories = append(counts.Categories, "attractions")
		}
		if hotels, ok := jsonData["hotels"].([]interface{}); ok {
			counts.Hotels = len(hotels)
			counts.Categories = append(counts.Categories, "accommodation")
		}
		if restaurants, ok := jsonData["restaurants"].([]interface{}); ok {
			counts.Restaurants = len(restaurants)
			counts.Categories = append(counts.Categories, "dining")
		}
		if _, ok := jsonData["itinerary_response"]; ok {
			counts.HasItinerary = true
			counts.Categories = append(counts.Categories, "itinerary")
		}
		if _, ok := jsonData["itinerary_name"]; ok {
			counts.HasItinerary = true
			counts.Categories = append(counts.Categories, "itinerary")
		}
	} else {
		// Handle text response with pattern matching
		lowerResponse := strings.ToLower(response)

		// Count mentions of different content types
		if strings.Contains(lowerResponse, "hotel") || strings.Contains(lowerResponse, "accommodation") {
			counts.Hotels = 1
			counts.Categories = append(counts.Categories, "accommodation")
		}
		if strings.Contains(lowerResponse, "restaurant") || strings.Contains(lowerResponse, "dining") {
			counts.Restaurants = 1
			counts.Categories = append(counts.Categories, "dining")
		}
		if strings.Contains(lowerResponse, "attraction") || strings.Contains(lowerResponse, "visit") || strings.Contains(lowerResponse, "see") {
			counts.POIs = 1
			counts.Categories = append(counts.Categories, "attractions")
		}
		if strings.Contains(lowerResponse, "itinerary") || strings.Contains(lowerResponse, "plan") || strings.Contains(lowerResponse, "schedule") {
			counts.HasItinerary = true
			counts.Categories = append(counts.Categories, "itinerary")
		}
	}

	return counts
}

// CalculateComplexityScore calculates a complexity score from 1-10 based on session content
func CalculateComplexityScore(pois, hotels, restaurants, messageCount int, hasItinerary bool) int {
	score := 1

	// Base score from content count
	totalContent := pois + hotels + restaurants
	if totalContent > 20 {
		score += 3
	} else if totalContent > 10 {
		score += 2
	} else if totalContent > 5 {
		score++
	}

	// Bonus for having itinerary
	if hasItinerary {
		score += 2
	}

	// Bonus for message count (engagement)
	if messageCount > 20 {
		score += 2
	} else if messageCount > 10 {
		score++
	}

	// Bonus for content diversity
	contentTypes := 0
	if pois > 0 {
		contentTypes++
	}
	if hotels > 0 {
		contentTypes++
	}
	if restaurants > 0 {
		contentTypes++
	}
	if contentTypes >= 3 {
		score += 2
	} else if contentTypes >= 2 {
		score++
	}

	// Cap at 10
	if score > 10 {
		score = 10
	}

	return score
}

// CountMessagesByRole counts messages by user and assistant roles
func CountMessagesByRole(messages []locitypes.ConversationMessage) (userCount, assistantCount int) {
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		}
	}
	return userCount, assistantCount
}

// CalculateAverageMessageLength calculates the average length of all messages
func CalculateAverageMessageLength(messages []locitypes.ConversationMessage) int {
	if len(messages) == 0 {
		return 0
	}

	totalLength := 0
	for _, msg := range messages {
		totalLength += len(msg.Content)
	}

	return totalLength / len(messages)
}

// CalculateEngagementLevel determines engagement level based on metrics
func CalculateEngagementLevel(messageCount int, duration time.Duration, complexityScore int) string {
	score := 0

	// Message count factor
	if messageCount > 15 {
		score += 3
	} else if messageCount > 8 {
		score += 2
	} else if messageCount > 3 {
		score++
	}

	// Duration factor (more than 10 minutes indicates engagement)
	if duration > 30*time.Minute {
		score += 3
	} else if duration > 10*time.Minute {
		score += 2
	} else if duration > 2*time.Minute {
		score++
	}

	// Complexity factor
	if complexityScore >= 8 {
		score += 2
	} else if complexityScore >= 5 {
		score++
	}

	// Determine level
	if score >= 6 {
		return "high"
	} else if score >= 3 {
		return "medium"
	}
	return "low"
}

// UniqueStringSlice removes duplicates from a string slice
func UniqueStringSlice(slice []string) []string {
	unique := make(map[string]bool)
	result := make([]string, 0)

	for _, item := range slice {
		if !unique[item] && item != "" {
			unique[item] = true
			result = append(result, item)
		}
	}

	return result
}

func CleanJSONResponse(response string) string {
	response = strings.TrimSpace(response)

	// Remove markdown code blocks (```json or ```)
	// Use regex to remove everything before and after code blocks
	codeBlockPattern := regexp.MustCompile("(?s)```(?:json)?\\s*([\\s\\S]*?)```")
	if matches := codeBlockPattern.FindStringSubmatch(response); len(matches) > 1 {
		response = matches[1]
		response = strings.TrimSpace(response)
	} else {
		// Fallback to prefix/suffix removal
		if strings.HasPrefix(response, "```json") {
			response = strings.TrimPrefix(response, "```json")
		} else if strings.HasPrefix(response, "```") {
			response = strings.TrimPrefix(response, "```")
		}
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	// Find the first { and last balanced }
	firstBrace := strings.Index(response, "{")
	if firstBrace == -1 {
		return response
	}

	// Count braces to find the matching closing brace
	braceCount := 0
	lastValidBrace := -1
	inString := false
	escapeNext := false

	for i := firstBrace; i < len(response); i++ {
		char := response[i]

		// Handle string escaping
		if escapeNext {
			escapeNext = false
			continue
		}
		if char == '\\' {
			escapeNext = true
			continue
		}

		// Track if we're inside a string
		if char == '"' {
			inString = !inString
			continue
		}

		// Only count braces outside of strings
		if !inString {
			switch char {
			case '{':
				braceCount++
			case '}':
				braceCount--
				if braceCount == 0 {
					lastValidBrace = i
					break
				}
			}
		}
	}

	// If braces are unbalanced, try to find the last }
	if braceCount != 0 {
		lastBrace := strings.LastIndex(response, "}")
		if lastBrace == -1 || lastBrace <= firstBrace {
			return response
		}
		lastValidBrace = lastBrace
	}

	if lastValidBrace == -1 {
		return response
	}

	// Extract just the JSON portion
	jsonPortion := response[firstBrace : lastValidBrace+1]

	// Remove any remaining backticks
	jsonPortion = strings.ReplaceAll(jsonPortion, "`", "")

	// Remove trailing commas before closing braces/brackets
	jsonPortion = regexp.MustCompile(`,(\s*[}\\]])`).ReplaceAllString(jsonPortion, "$1")

	return strings.TrimSpace(jsonPortion)
}
