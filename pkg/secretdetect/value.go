package secretdetect

// RedactValue recursively sanitizes JSON-compatible values. It is useful at
// metadata and migration boundaries where arbitrary nested strings can appear.
func RedactValue(value any, config Config) any {
	switch typed := value.(type) {
	case string:
		return RedactWithConfig(typed, config).Text
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = RedactValue(typed[i], config)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			cleanKey := RedactWithConfig(key, config).Text
			out[cleanKey] = RedactValue(item, config)
		}
		return out
	default:
		return value
	}
}
