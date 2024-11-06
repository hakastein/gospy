package pyroscope

import "testing"

func BenchmarkString(b *testing.B) {
	// Example requestData instance for benchmarking
	rd := &requestData{
		name:   "testName",
		from:   1234567890,
		until:  2345678901,
		rateHz: 1000,
	}

	// Reset the timer to ignore setup time
	b.ResetTimer()

	// Run the benchmark loop
	_ = rd.String()
}
