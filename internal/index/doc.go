package index

import (
	"go/ast"
	"strings"
)

// ExtractDoc returns the text of a comment group, or empty string if nil.
func ExtractDoc(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return cg.Text()
}

// FirstSentence extracts the first sentence from a doc string.
// It looks for the first ". " or ".\n" boundary. If the result
// exceeds 120 characters it is truncated with an ellipsis.
func FirstSentence(doc string) string {
	if doc == "" {
		return ""
	}
	doc = strings.TrimSpace(doc)

	// Find end of first sentence.
	end := -1
	for i := 0; i < len(doc)-1; i++ {
		if doc[i] == '.' && (doc[i+1] == ' ' || doc[i+1] == '\n') {
			end = i + 1 // include the period
			break
		}
	}
	if end == -1 {
		// No sentence boundary found — check if doc ends with a period.
		if doc[len(doc)-1] == '.' {
			end = len(doc)
		} else {
			end = len(doc)
		}
	}

	result := doc[:end]
	// Replace newlines with spaces for a clean single-line summary.
	result = strings.Join(strings.Fields(result), " ")

	if len(result) > 120 {
		return result[:117] + "..."
	}
	return result
}
