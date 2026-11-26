//go:build race

package statetest

// raceEnabled scales the latency budgets of the performance checks: the race detector
// slows SQLite and PostgreSQL drivers several times over on small CI runners.
const raceEnabled = true
