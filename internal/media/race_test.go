//go:build race

package media

// The race detector slows the host side of every worker call several times
// over. Tests keep the real memory budgets and scale only the deadlines.
func init() {
	renderDeadline *= 4
	hashDeadline *= 4
}
