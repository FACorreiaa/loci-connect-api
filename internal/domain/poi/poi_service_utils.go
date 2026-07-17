package poi

import (
	"regexp"
	"strings"
)

var trailingCommaBeforeBracketRE = regexp.MustCompile(`,(\s*[}\\]])`)

func cleanLLMResponse(responseText string) string {
	cleaned := strings.TrimSpace(responseText)

	if after, ok := strings.CutPrefix(cleaned, "```json"); ok {
		cleaned = after
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	} else if after, ok := strings.CutPrefix(cleaned, "```"); ok {
		cleaned = after
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	}

	cleaned = trailingCommaBeforeBracketRE.ReplaceAllString(cleaned, "$1")
	return cleaned
}

func cleanJSONResponse(response string) string {
	response = strings.TrimSpace(response)

	if after, ok := strings.CutPrefix(response, "```json"); ok {
		response = after
	} else if after, ok := strings.CutPrefix(response, "```"); ok {
		response = after
	}

	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	firstBrace := strings.Index(response, "{")
	if firstBrace == -1 {
		return response
	}

	braceCount := 0
	lastValidBrace := -1
loop:
	for i := firstBrace; i < len(response); i++ {
		switch response[i] {
		case '{':
			braceCount++
		case '}':
			braceCount--
			if braceCount == 0 {
				lastValidBrace = i
				break loop
			}
		}
	}

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

	jsonPortion := response[firstBrace : lastValidBrace+1]
	jsonPortion = strings.ReplaceAll(jsonPortion, "`", "")
	jsonPortion = trailingCommaBeforeBracketRE.ReplaceAllString(jsonPortion, "$1")

	return strings.TrimSpace(jsonPortion)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
