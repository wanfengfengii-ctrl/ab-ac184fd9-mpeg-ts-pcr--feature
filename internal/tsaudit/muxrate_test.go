package tsaudit_test

import (
	"testing"

	"mpegtsaudit/internal/tsaudit"
	"mpegtsaudit/internal/tsbuild"
)

// rateFragment lays PAT/PMT followed by PCR-bearing packets at adjacent
// packet positions with the given 27 MHz PCR values. At one packet (188 bytes)
// per 27000 ticks the mux rate is exactly 1,504,000 bps.
func rateFragment(pcrs ...int64) []byte {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	for _, v := range pcrs {
		b.AddPCR(b.Opt.PCRPID, v)
	}
	return b.Bytes()
}

const exactRateBps = int64(1504000) // 188 bytes * 8 / 1 ms

func rateOptions(rateBps, ppm int64) tsaudit.Options {
	return tsaudit.Options{MaxPcrGapMs: 1000, ExpectedMuxRateBps: rateBps, MaxRateErrorPpm: ppm}
}

func TestMuxRateConstantAccepted(t *testing.T) {
	d := rateFragment(0, 27000, 54000, 81000)
	rep, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 1))
	if err != nil {
		t.Fatalf("exact constant rate must pass: %v", err)
	}
	if rep.MuxRate == nil {
		t.Fatal("muxRate block missing from report")
	}
	if rep.MuxRate.ExpectedMuxRateBps != exactRateBps {
		t.Errorf("expectedMuxRateBps = %d", rep.MuxRate.ExpectedMuxRateBps)
	}
	if rep.MuxRate.TolerancePpm != 1 {
		t.Errorf("tolerancePpm = %d", rep.MuxRate.TolerancePpm)
	}
	if rep.MuxRate.IntervalsChecked != 3 {
		t.Errorf("intervalsChecked = %d, want 3", rep.MuxRate.IntervalsChecked)
	}
}

func TestMuxRateDisabledOmitsBlock(t *testing.T) {
	rep, err := tsaudit.Audit(rateFragment(0, 27000, 54000), 1000)
	if err != nil {
		t.Fatalf("legacy audit must pass: %v", err)
	}
	if rep.MuxRate != nil {
		t.Errorf("muxRate must be omitted when rate mode is disabled: %+v", rep.MuxRate)
	}
}

func TestMuxRateLocalBurstRejected(t *testing.T) {
	// Second interval covers one packet in 26000 ticks instead of 27000:
	// the average duration looks normal but the local rate is ~3.8% high.
	d := rateFragment(0, 27000, 53000, 80000)
	_, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 1000))
	if err == nil {
		t.Fatal("local burst must be rejected")
	}
	if err.Code != tsaudit.ErrMuxRateMismatch {
		t.Fatalf("code = %s, want %s", err.Code, tsaudit.ErrMuxRateMismatch)
	}
	// Earliest bad interval ends at the latter PCR: packet 4, PCR PID 0x0100.
	if err.Packet != 4 || err.PID != 0x0100 {
		t.Errorf("location = packet %d pid %#x, want 4 / 0x100", err.Packet, err.PID)
	}
}

func TestMuxRateLocalStallRejected(t *testing.T) {
	// 28000 ticks for one packet makes the interval ~3.6% too slow.
	d := rateFragment(0, 27000, 55000)
	_, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 1000))
	if err == nil || err.Code != tsaudit.ErrMuxRateMismatch {
		t.Fatalf("want %s, got %v", tsaudit.ErrMuxRateMismatch, err)
	}
	if err.Packet != 4 {
		t.Errorf("packet = %d, want 4", err.Packet)
	}
}

func TestMuxRateToleranceBoundaryInclusive(t *testing.T) {
	// PCR packets 11 indices apart (10 intervening) clocked 270000 ticks:
	// measured = 11*1504000*27000/270000 = 1,654,400 = expected * 1.1,
	// i.e. an error of exactly 100000 ppm.
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 0)
	for i := 0; i < 10; i++ {
		b.AddPayload(b.Opt.Media[0].PID)
	}
	b.AddPCR(b.Opt.PCRPID, 270000)
	d := b.Bytes()

	if _, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 100000)); err != nil {
		t.Fatalf("error equal to the tolerance must pass: %v", err)
	}
	_, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 99999))
	if err == nil {
		t.Fatal("one ppm tighter than the actual deviation must fail")
	}
	if err.Code != tsaudit.ErrMuxRateMismatch || err.Packet != 13 {
		t.Fatalf("want %s at packet 13, got %v", tsaudit.ErrMuxRateMismatch, err)
	}
}

func TestMuxRateRequiresTwoPCRs(t *testing.T) {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 0)
	b.AddPayload(b.Opt.Media[0].PID)
	_, err := tsaudit.AuditWithOptions(b.Bytes(), rateOptions(exactRateBps, 1000))
	if err == nil {
		t.Fatal("rate mode with a single PCR must be rejected")
	}
	if err.Code != tsaudit.ErrMuxRateIntervalUncomputable {
		t.Fatalf("code = %s, want %s", err.Code, tsaudit.ErrMuxRateIntervalUncomputable)
	}
	if err.Packet != 2 || err.PID != 0x0100 {
		t.Errorf("location = packet %d pid %#x, want 2 / 0x100", err.Packet, err.PID)
	}
}

func TestMuxRateZeroClockIncrementRejected(t *testing.T) {
	// Two PCRs with identical values: not a backwards move, but the interval
	// clock delta is zero so the rate is uncomputable.
	d := rateFragment(100*27000, 100*27000)
	_, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 1000))
	if err == nil {
		t.Fatal("zero PCR clock increment must be rejected in rate mode")
	}
	if err.Code != tsaudit.ErrMuxRateIntervalUncomputable {
		t.Fatalf("code = %s, want %s", err.Code, tsaudit.ErrMuxRateIntervalUncomputable)
	}
	if err.Packet != 3 || err.PID != 0x0100 {
		t.Errorf("location = packet %d pid %#x, want 3 / 0x100", err.Packet, err.PID)
	}
}

func TestMuxRateAcross33BitWrap(t *testing.T) {
	const cycle = int64(1) << 33 * 300
	// Adjacent packets straddling the wrap with a 27000-tick delta keep the
	// same one-packet-per-millisecond rate.
	d := rateFragment(cycle-13500, 13500, 40500)
	rep, err := tsaudit.AuditWithOptions(d, rateOptions(exactRateBps, 1))
	if err != nil {
		t.Fatalf("rate across 33-bit wraparound must pass: %v", err)
	}
	if rep.MuxRate.IntervalsChecked != 2 {
		t.Errorf("intervalsChecked = %d, want 2", rep.MuxRate.IntervalsChecked)
	}
	if rep.Duration27MHz != 54000 {
		t.Errorf("duration27mhz = %d, want 54000", rep.Duration27MHz)
	}
}

func TestMuxRateCountsWireBytesAcrossOtherPackets(t *testing.T) {
	// Two PCR packets separated by 3 packets (2 payload + null) with a clock
	// delta matching 4 packets at the nominal rate.
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 0)
	b.AddPayload(b.Opt.Media[0].PID)
	b.AddPayload(b.Opt.Media[1].PID)
	b.AddNull()
	b.AddPCR(b.Opt.PCRPID, 4*27000)
	rep, err := tsaudit.AuditWithOptions(b.Bytes(), rateOptions(exactRateBps, 1))
	if err != nil {
		t.Fatalf("rate measured over intervening media and null packets must pass: %v", err)
	}
	if rep.MuxRate.IntervalsChecked != 1 {
		t.Errorf("intervalsChecked = %d, want 1", rep.MuxRate.IntervalsChecked)
	}
}

func TestMuxRateInvalidOptions(t *testing.T) {
	d := rateFragment(0, 27000, 54000)
	for _, o := range []tsaudit.Options{
		{MaxPcrGapMs: 1000, ExpectedMuxRateBps: 99999, MaxRateErrorPpm: 1000},
		{MaxPcrGapMs: 1000, ExpectedMuxRateBps: 200000001, MaxRateErrorPpm: 1000},
		{MaxPcrGapMs: 1000, ExpectedMuxRateBps: 100000, MaxRateErrorPpm: 0},
		{MaxPcrGapMs: 1000, ExpectedMuxRateBps: 100000, MaxRateErrorPpm: 100001},
	} {
		if _, err := tsaudit.AuditWithOptions(d, o); err == nil || err.Code != tsaudit.ErrInvalidMuxRateParam {
			t.Errorf("options %+v must yield %s, got %v", o, tsaudit.ErrInvalidMuxRateParam, err)
		}
	}
}
