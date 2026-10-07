package tsaudit_test

import (
	"testing"

	"mpegtsaudit/internal/tsaudit"
	"mpegtsaudit/internal/tsbuild"
)

// A constant 10 Mbit/s stream: 5 packets per PCR interval and 20304 27 MHz
// ticks per interval (5*188*8*27e6/20304 == 10,000,000 bit/s exactly).
const (
	rateExpectedBps   int64 = 10_000_000
	ratePacketsPerPCR       = 5
	rateTicksPerPCR         = 20304
)

func constantRateFragment(pcrTicks []int64) []byte {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	for _, ticks := range pcrTicks {
		b.AddPCR(b.Opt.PCRPID, ticks)
		for j := 0; j < ratePacketsPerPCR-1; j++ {
			b.AddPayload(b.Opt.Media[j%2].PID)
		}
	}
	return b.Bytes()
}

func onePCRFragment() []byte {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 0)
	b.AddPayload(b.Opt.Media[0].PID)
	return b.Bytes()
}

func TestRateModeAccepted(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR, 2 * rateTicksPerPCR, 3 * rateTicksPerPCR})
	rep, err := tsaudit.AuditWithRate(data, 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	})
	if err != nil {
		t.Fatalf("constant-rate stream must pass: %v", err)
	}
	if rep.Rate == nil {
		t.Fatal("rate summary missing")
	}
	if rep.Rate.ExpectedMuxRateBps != rateExpectedBps {
		t.Errorf("expectedMuxRateBps = %d", rep.Rate.ExpectedMuxRateBps)
	}
	if rep.Rate.MaxRateErrorPpm != 100 {
		t.Errorf("maxRateErrorPpm = %d", rep.Rate.MaxRateErrorPpm)
	}
	if rep.Rate.CheckedIntervals != 3 {
		t.Errorf("checkedIntervals = %d, want 3", rep.Rate.CheckedIntervals)
	}
}

func TestNormalModeHasNoRateSummary(t *testing.T) {
	rep, err := tsaudit.Audit(validFragment(), 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Rate != nil {
		t.Errorf("rate summary must be absent without rate options: %+v", rep.Rate)
	}
}

func TestRateModeRejectsDeviatingInterval(t *testing.T) {
	// Third PCR arrives late: interval 1 is ~6.4 % slower than 10 Mbit/s.
	data := constantRateFragment([]int64{0, rateTicksPerPCR, 42000, 3 * rateTicksPerPCR})
	_, err := tsaudit.AuditWithRate(data, 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	})
	if err == nil {
		t.Fatal("deviating interval must be rejected")
	}
	if err.Code != tsaudit.ErrMuxRateErrorExceeded {
		t.Fatalf("code = %s, want %s", err.Code, tsaudit.ErrMuxRateErrorExceeded)
	}
	// PAT=0, PMT=1, PCR packets at 2, 7, 12, 17; interval ending at packet 12
	// is the earliest bad one.
	if err.Packet != 12 {
		t.Errorf("packet = %d, want 12", err.Packet)
	}
	if err.PID != 0x0100 {
		t.Errorf("pid = %#x, want 0x100", err.PID)
	}
}

func TestRateModeToleranceBoundary(t *testing.T) {
	// Same ~64,000 ppm deviation: rejected with a tight tolerance, accepted
	// with a loose one.
	data := constantRateFragment([]int64{0, rateTicksPerPCR, 42000})
	if _, err := tsaudit.AuditWithRate(data, 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100000,
	}); err != nil {
		t.Errorf("deviation within 10%% tolerance must pass: %v", err)
	}
	if _, err := tsaudit.AuditWithRate(data, 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	}); err == nil || err.Code != tsaudit.ErrMuxRateErrorExceeded {
		t.Fatalf("want %s, got %v", tsaudit.ErrMuxRateErrorExceeded, err)
	}
}

func TestRateModeRequiresTwoPCRs(t *testing.T) {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 0)
	b.AddPayload(b.Opt.Media[0].PID)
	_, err := tsaudit.AuditWithRate(b.Bytes(), 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	})
	if err == nil || err.Code != tsaudit.ErrRateNeedsTwoPCRs {
		t.Fatalf("want %s, got %v", tsaudit.ErrRateNeedsTwoPCRs, err)
	}
	if err.Packet != 2 || err.PID != 0x0100 {
		t.Errorf("location = packet %d pid %#x, want 2 / 0x100", err.Packet, err.PID)
	}
}

func TestRateModeZeroClockIncrementUncomputable(t *testing.T) {
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, 1000)
	b.AddPayload(b.Opt.Media[0].PID)
	b.AddPCR(b.Opt.PCRPID, 1000) // same clock value: stalled PCR
	_, err := tsaudit.AuditWithRate(b.Bytes(), 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	})
	if err == nil || err.Code != tsaudit.ErrRateIntervalUncomputable {
		t.Fatalf("want %s, got %v", tsaudit.ErrRateIntervalUncomputable, err)
	}
	if err.Packet != 4 || err.PID != 0x0100 {
		t.Errorf("location = packet %d pid %#x, want 4 / 0x100", err.Packet, err.PID)
	}
}

func TestRateModeAcrossWrap(t *testing.T) {
	const cycle = int64(1) << 33 * 300
	b := tsbuild.New()
	b.AddPAT()
	b.AddPMT()
	b.AddPCR(b.Opt.PCRPID, cycle-rateTicksPerPCR)
	for j := 0; j < ratePacketsPerPCR-1; j++ {
		b.AddPayload(b.Opt.Media[j%2].PID)
	}
	b.AddPCR(b.Opt.PCRPID, 0) // wrapped; unwrapped delta is exactly rateTicksPerPCR
	rep, err := tsaudit.AuditWithRate(b.Bytes(), 1000, tsaudit.RateOptions{
		ExpectedMuxRateBps: rateExpectedBps,
		MaxRateErrorPpm:    100,
	})
	if err != nil {
		t.Fatalf("rate check must follow the 33-bit unwrap: %v", err)
	}
	if rep.Rate.CheckedIntervals != 1 {
		t.Errorf("checkedIntervals = %d, want 1", rep.Rate.CheckedIntervals)
	}
}

func TestRateModeInvalidOptions(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR})
	for _, opt := range []tsaudit.RateOptions{
		{ExpectedMuxRateBps: 99999, MaxRateErrorPpm: 100},
		{ExpectedMuxRateBps: 200000001, MaxRateErrorPpm: 100},
		{ExpectedMuxRateBps: rateExpectedBps, MaxRateErrorPpm: 0},
		{ExpectedMuxRateBps: rateExpectedBps, MaxRateErrorPpm: 100001},
	} {
		if _, err := tsaudit.AuditWithRate(data, 1000, opt); err == nil {
			t.Errorf("%+v must be rejected", opt)
		}
	}
}
