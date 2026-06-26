// Package secret contains internal implementation details.
package secret

// Token represents an internal auth token.
type Token struct {
	Value string
}

// Validate checks if the token is valid.
func (t *Token) Validate() bool {
	return t.Value != ""
}
