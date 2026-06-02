package llm

import (
	"encoding/json"
	"errors"
)

func firstJSONString(data []byte) (string, error) {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	if found, ok := walkForJSONText(value); ok {
		return found, nil
	}
	return "", errors.New("no JSON diagnosis found in model response")
}

func walkForJSONText(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"output_text", "text", "content"} {
			if raw, ok := typed[key].(string); ok && looksLikeJSONObject(raw) {
				return raw, true
			}
		}
		for _, child := range typed {
			if found, ok := walkForJSONText(child); ok {
				return found, true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if found, ok := walkForJSONText(child); ok {
				return found, true
			}
		}
	case string:
		if looksLikeJSONObject(typed) {
			return typed, true
		}
	}
	return "", false
}

func looksLikeJSONObject(value string) bool {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '\n', '\r', '\t':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}
