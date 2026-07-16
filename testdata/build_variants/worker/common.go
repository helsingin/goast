// Package worker exercises coherent typed-reference build contexts.
package worker

// Runner has one platform-specific Run implementation per build context.
type Runner struct{}

// CommonCall is present in every build context.
func CommonCall(r Runner) {
	r.Run()
}
