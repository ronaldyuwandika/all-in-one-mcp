package secretdetect

// Finding describes a detected secret without retaining its value.
// Offset is a byte offset, matching Go's string and regexp conventions.
type Finding struct {
	Type        string  `json:"type"`
	Offset      int     `json:"offset,omitempty"`
	End         int     `json:"end,omitempty"`
	Confidence  float64 `json:"confidence"`
	Fingerprint string  `json:"fingerprint"`
}

type Result struct {
	Text     string    `json:"text"`
	Findings []Finding `json:"findings,omitempty"`
}

type match struct {
	start       int
	end         int
	kind        string
	replacement string
	name        string
	value       string
	confidence  float64
}
