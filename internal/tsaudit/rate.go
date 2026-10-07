package tsaudit

import "math/big"

// RateOptions enables the constant-mux-rate mode. When supplied to Audit,
// every interval between adjacent PCRs is checked against
// ExpectedMuxRateBps within MaxRateErrorPpm parts per million.
type RateOptions struct {
	ExpectedMuxRateBps int64
	MaxRateErrorPpm    int
}

// RateReport summarises a successful constant-rate audit.
type RateReport struct {
	ExpectedMuxRateBps int64 `json:"expectedMuxRateBps"`
	MaxRateErrorPpm    int   `json:"maxRateErrorPpm"`
	CheckedIntervals   int   `json:"checkedIntervals"`
}

// muxRateWithinTolerance compares the interval bit rate with the expected
// value. The measured rate is byteSpan*8 bits over dt27 ticks of the 27 MHz
// clock, i.e. byteSpan*8*27e6/dt27 bps. The ppm inequality is evaluated
// exactly with big.Int because its terms can exceed int64.
func muxRateWithinTolerance(byteSpan, dt27, expectedBps int64, ppm int) bool {
	// measured bps = byteSpan*8 bits * 27e6 ticks/s / dt27 ticks
	num := new(big.Int).Mul(big.NewInt(byteSpan), big.NewInt(8*27_000_000))
	den := big.NewInt(dt27)
	exp := big.NewInt(expectedBps)

	// |num/den - exp| * 1e6 <= ppm * exp
	// <=> |num - exp*den| * 1e6 <= ppm * exp * den
	expDen := new(big.Int).Mul(exp, den)
	diff := new(big.Int).Sub(num, expDen)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	lhs := new(big.Int).Mul(diff, big.NewInt(1_000_000))
	rhs := new(big.Int).Mul(expDen, big.NewInt(int64(ppm)))
	return lhs.Cmp(rhs) <= 0
}
