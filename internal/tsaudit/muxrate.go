package tsaudit

import "math/big"

// muxRateChecker verifies that every interval between adjacent PCRs carries
// bytes at the expected constant mux rate within a parts-per-million tolerance.
// Interval transport rate is measured from the zero-based starting byte offset
// of each PCR-bearing packet and the unwrapped 27 MHz PCR clock delta.
type muxRateChecker struct {
	expectedBps  int64
	tolerancePpm int64

	prevPkt   int
	intervals int

	// Scratch big.Ints keep every comparison exact: the error products can
	// exceed int64 (up to ~5.4e21 for the widest legal parameters).
	measured big.Int // byteDelta * 8 * 27 MHz
	diff     big.Int // |measured - expected*clockDelta| * 1e6
	limit    big.Int // expected * tolerance * clockDelta
	tmp      big.Int
}

func newMuxRateChecker(expectedBps, tolerancePpm int64) *muxRateChecker {
	return &muxRateChecker{expectedBps: expectedBps, tolerancePpm: tolerancePpm}
}

// markFirst records the packet index of the first PCR.
func (c *muxRateChecker) markFirst(packetIdx int) {
	c.prevPkt = packetIdx
}

// observe checks the interval ending at the PCR in packet packetIdx (the
// latter PCR of the pair), whose unwrapped clock delta is clockDelta27.
func (c *muxRateChecker) observe(packetIdx, pid int, clockDelta27 int64) *AuditError {
	if clockDelta27 <= 0 {
		return auditError(ErrMuxRateIntervalUncomputable,
			"PCR clock increment is not positive, mux rate interval is uncomputable",
			packetIdx, pid)
	}
	byteDelta := int64(packetIdx-c.prevPkt) * packetSize
	if byteDelta <= 0 {
		return auditError(ErrMuxRateIntervalUncomputable,
			"PCR packets share a starting offset, mux rate interval is uncomputable",
			packetIdx, pid)
	}

	// |measuredBps - expected| / expected <= tolerance/1e6, with
	// measuredBps = byteDelta*8*27MHz/clockDelta, compared exactly:
	//   |byteDelta*8*27MHz - expected*clockDelta| * 1e6
	//     <= expected * tolerance * clockDelta
	const bitsPerByte = int64(8)
	const clockHz = int64(27000000)
	const ppmBase = int64(1000000)

	c.measured.SetInt64(byteDelta * bitsPerByte * clockHz)

	c.tmp.SetInt64(c.expectedBps)
	c.tmp.Mul(&c.tmp, big.NewInt(clockDelta27))
	c.diff.Set(&c.measured)
	c.diff.Sub(&c.diff, &c.tmp)
	c.diff.Abs(&c.diff)
	c.diff.Mul(&c.diff, big.NewInt(ppmBase))

	c.limit.SetInt64(c.expectedBps)
	c.limit.Mul(&c.limit, big.NewInt(c.tolerancePpm))
	c.limit.Mul(&c.limit, big.NewInt(clockDelta27))

	if c.diff.Cmp(&c.limit) > 0 {
		return auditError(ErrMuxRateMismatch,
			"interval mux rate deviates from expected beyond tolerance",
			packetIdx, pid)
	}

	c.prevPkt = packetIdx
	c.intervals++
	return nil
}

func (c *muxRateChecker) report() *MuxRateReport {
	return &MuxRateReport{
		ExpectedMuxRateBps: c.expectedBps,
		TolerancePpm:       c.tolerancePpm,
		IntervalsChecked:   c.intervals,
	}
}
