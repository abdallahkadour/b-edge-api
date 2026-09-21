package membership

import "fmt"

// plural picks a singular or plural template and fills in n. Small enough to
// inline, kept separate so the error constructors stay readable.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}
