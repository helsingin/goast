package greeter

import "strings"

// sanitizeName cleans up a name for use in greetings.
func sanitizeName(name string) string {
	return strings.TrimSpace(name)
}
