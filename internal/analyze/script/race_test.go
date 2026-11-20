//go:build race

package script

// raceEnabled reports a -race build, whose instrumentation makes timing
// assertions meaningless.
const raceEnabled = true
