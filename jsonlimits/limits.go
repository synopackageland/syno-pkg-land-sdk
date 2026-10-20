// Package jsonlimits bounds JSON complexity before a decoder allocates values.
package jsonlimits

const (
	MaxDepth           = 32
	MaxStructuralBytes = 65536
)

// WithinLimits scans raw JSON using constant memory. It counts punctuation
// outside strings, bounding both wide collections and nesting independently
// of the transport's byte limit. Syntax validation remains the decoder's job.
func WithinLimits(body []byte) bool {
	depth, structural := 0, 0
	inString, escaped := false, false
	for _, c := range body {
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			structural++
			if depth > MaxDepth {
				return false
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
			structural++
		case ',', ':':
			structural++
		}
		if structural > MaxStructuralBytes {
			return false
		}
	}
	return true
}
