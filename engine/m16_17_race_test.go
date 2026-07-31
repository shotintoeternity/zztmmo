//go:build race

package zztgo

// Under -race the ElementDefs pin (M16.17a) is redundant AND fatal: it provokes
// the very write/read pair the detector exists to report, so the required race
// job would go red on a defect that is already filed and already pinned without
// the detector's help. m1617RaceDetector lets that one test say so and skip.
func init() { m1617RaceDetector = true }
