package tsaudit_test

import (
	"net/http"
	"testing"

	"mpegtsaudit/internal/tsaudit"
)

func rateQuery(extra string) string {
	return "maxPcrGapMs=1000&expectedMuxRateBps=10000000&maxRateErrorPpm=100" + extra
}

func TestHTTPRateModeSuccess(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR, 2 * rateTicksPerPCR, 3 * rateTicksPerPCR})
	status, body := doAudit(t, data, rateQuery(""), "application/octet-stream")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v", status, body)
	}
	rep := body["report"].(map[string]any)
	rate := rep["rate"].(map[string]any)
	if rate["expectedMuxRateBps"].(float64) != 10000000 {
		t.Errorf("expectedMuxRateBps = %v", rate["expectedMuxRateBps"])
	}
	if rate["maxRateErrorPpm"].(float64) != 100 {
		t.Errorf("maxRateErrorPpm = %v", rate["maxRateErrorPpm"])
	}
	if rate["checkedIntervals"].(float64) != 3 {
		t.Errorf("checkedIntervals = %v", rate["checkedIntervals"])
	}
}

func TestHTTPRateModeFailureShape(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR, 42000, 3 * rateTicksPerPCR})
	status, body := doAudit(t, data, rateQuery(""), "application/octet-stream")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["error"].(map[string]any)["code"] != tsaudit.ErrMuxRateErrorExceeded {
		t.Fatalf("body=%v", body)
	}
	if body["packet"].(float64) != 12 {
		t.Errorf("packet = %v, want 12", body["packet"])
	}
	if body["pid"].(float64) != 0x0100 {
		t.Errorf("pid = %v, want 256", body["pid"])
	}
	if _, partial := body["report"]; partial {
		t.Fatal("partial report present on failure")
	}
}

func TestHTTPRateParamsMustBePaired(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR})
	cases := []struct {
		name  string
		query string
	}{
		{"only expected rate", "maxPcrGapMs=1000&expectedMuxRateBps=10000000"},
		{"only ppm", "maxPcrGapMs=1000&maxRateErrorPpm=100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doAudit(t, data, tc.query, "application/octet-stream")
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v", status, body)
			}
			if body["error"].(map[string]any)["code"] != tsaudit.ErrRateParamsMustBePaired {
				t.Fatalf("body=%v", body)
			}
		})
	}
}

func TestHTTPRateParamRanges(t *testing.T) {
	data := constantRateFragment([]int64{0, rateTicksPerPCR})
	cases := []struct {
		name  string
		query string
		code  string
	}{
		{"rate too low", "maxPcrGapMs=1000&expectedMuxRateBps=99999&maxRateErrorPpm=100", tsaudit.ErrInvalidExpectedMuxRate},
		{"rate too high", "maxPcrGapMs=1000&expectedMuxRateBps=200000001&maxRateErrorPpm=100", tsaudit.ErrInvalidExpectedMuxRate},
		{"rate nan", "maxPcrGapMs=1000&expectedMuxRateBps=abc&maxRateErrorPpm=100", tsaudit.ErrInvalidExpectedMuxRate},
		{"ppm zero", "maxPcrGapMs=1000&expectedMuxRateBps=10000000&maxRateErrorPpm=0", tsaudit.ErrInvalidMaxRateErrorPpm},
		{"ppm too high", "maxPcrGapMs=1000&expectedMuxRateBps=10000000&maxRateErrorPpm=100001", tsaudit.ErrInvalidMaxRateErrorPpm},
		{"ppm nan", "maxPcrGapMs=1000&expectedMuxRateBps=10000000&maxRateErrorPpm=abc", tsaudit.ErrInvalidMaxRateErrorPpm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doAudit(t, data, tc.query, "application/octet-stream")
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v", status, body)
			}
			if body["error"].(map[string]any)["code"] != tc.code {
				t.Fatalf("body=%v", body)
			}
		})
	}
}

func TestHTTPRateModeNeedsTwoPCRs(t *testing.T) {
	b := onePCRFragment()
	status, body := doAudit(t, b, rateQuery(""), "application/octet-stream")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["error"].(map[string]any)["code"] != tsaudit.ErrRateNeedsTwoPCRs {
		t.Fatalf("body=%v", body)
	}
}
