package tsaudit_test

import (
	"net/http"
	"testing"

	"mpegtsaudit/internal/tsaudit"
)

func rateQuery(extra string) string {
	return "maxPcrGapMs=1000&expectedMuxRateBps=1504000&maxRateErrorPpm=1000" + extra
}

func TestHTTPMuxRateAccepted(t *testing.T) {
	status, body := doAudit(t, rateFragment(0, 27000, 54000, 81000),
		rateQuery(""), "application/octet-stream")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v", status, body)
	}
	rep := body["report"].(map[string]any)
	mr := rep["muxRate"].(map[string]any)
	if mr["expectedMuxRateBps"].(float64) != 1504000 {
		t.Errorf("expectedMuxRateBps=%v", mr["expectedMuxRateBps"])
	}
	if mr["tolerancePpm"].(float64) != 1000 {
		t.Errorf("tolerancePpm=%v", mr["tolerancePpm"])
	}
	if mr["intervalsChecked"].(float64) != 3 {
		t.Errorf("intervalsChecked=%v", mr["intervalsChecked"])
	}
}

func TestHTTPMuxRateMismatchShape(t *testing.T) {
	// Local burst in the second interval, tight tolerance.
	status, body := doAudit(t, rateFragment(0, 27000, 53000, 80000),
		rateQuery(""), "application/octet-stream")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%v", status, body)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != tsaudit.ErrMuxRateMismatch {
		t.Fatalf("code=%v body=%v", errObj["code"], body)
	}
	if body["packet"].(float64) != 4 {
		t.Errorf("packet=%v, want 4 (latter PCR)", body["packet"])
	}
	if body["pid"].(float64) != 0x0100 {
		t.Errorf("pid=%v, want 256", body["pid"])
	}
	if _, partial := body["report"]; partial {
		t.Fatal("partial report present on failure")
	}
}

func TestHTTPMuxRateParams(t *testing.T) {
	d := rateFragment(0, 27000, 54000)
	cases := []struct {
		name   string
		query  string
		status int
		code   string
	}{
		{"only rate given", "maxPcrGapMs=1000&expectedMuxRateBps=1504000",
			http.StatusBadRequest, tsaudit.ErrMissingMuxRateParam},
		{"only ppm given", "maxPcrGapMs=1000&maxRateErrorPpm=1000",
			http.StatusBadRequest, tsaudit.ErrMissingMuxRateParam},
		{"rate too low", "maxPcrGapMs=1000&expectedMuxRateBps=99999&maxRateErrorPpm=1000",
			http.StatusBadRequest, tsaudit.ErrInvalidMuxRateParam},
		{"rate too high", "maxPcrGapMs=1000&expectedMuxRateBps=200000001&maxRateErrorPpm=1000",
			http.StatusBadRequest, tsaudit.ErrInvalidMuxRateParam},
		{"ppm zero", "maxPcrGapMs=1000&expectedMuxRateBps=1504000&maxRateErrorPpm=0",
			http.StatusBadRequest, tsaudit.ErrInvalidMuxRateParam},
		{"ppm too high", "maxPcrGapMs=1000&expectedMuxRateBps=1504000&maxRateErrorPpm=100001",
			http.StatusBadRequest, tsaudit.ErrInvalidMuxRateParam},
		{"rate not numeric", "maxPcrGapMs=1000&expectedMuxRateBps=fast&maxRateErrorPpm=1000",
			http.StatusBadRequest, tsaudit.ErrInvalidMuxRateParam},
		{"boundary values accepted", "maxPcrGapMs=1000&expectedMuxRateBps=1504000&maxRateErrorPpm=100000",
			http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Loosen the fragment for the wide-tolerance boundary case.
			body2 := d
			if tc.name == "boundary values accepted" {
				body2 = rateFragment(0, 27000, 54000)
			}
			status, body := doAudit(t, body2, tc.query, "application/octet-stream")
			if status != tc.status {
				t.Fatalf("status=%d body=%v", status, body)
			}
			if tc.code != "" {
				if body["error"].(map[string]any)["code"] != tc.code {
					t.Fatalf("body=%v", body)
				}
			}
		})
	}
}

func TestHTTPLegacyRequestUnchanged(t *testing.T) {
	// No rate params: success report must not carry muxRate and the legacy
	// error ordering (missing gap beats everything) stays intact.
	status, body := doAudit(t, rateFragment(0, 27000, 54000),
		"maxPcrGapMs=1000", "application/octet-stream")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if _, ok := body["report"].(map[string]any)["muxRate"]; ok {
		t.Fatal("muxRate must be absent without rate params")
	}

	status, body = doAudit(t, rateFragment(0, 27000),
		"expectedMuxRateBps=1504000&maxRateErrorPpm=1000", "application/octet-stream")
	if status != http.StatusBadRequest ||
		body["error"].(map[string]any)["code"] != tsaudit.ErrMissingMaxPcrGap {
		t.Fatalf("missing maxPcrGapMs must still be reported first: %d %v", status, body)
	}
}
