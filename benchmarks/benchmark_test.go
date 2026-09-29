package benchmarks

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/pdf"

	"github.com/stretchr/testify/require"
)

type benchmarkCase struct {
	Name     string            `json:"name"`
	Payload  string            `json:"payload"`
	Invalid  bool              `json:"invalid"`
	Expected bcbp.BoardingPass `json:"expected"`
}

func loadCases(t testing.TB) []benchmarkCase {
	t.Helper()
	data, err := os.ReadFile("cases.json")
	require.NoError(t, err)
	var cases []benchmarkCase
	require.NoError(t, json.Unmarshal(data, &cases))
	require.NotEmpty(t, cases)
	return cases
}

func checkResult(t testing.TB, tc benchmarkCase, result bcbp.BoardingPass, err error) {
	t.Helper()
	if tc.Invalid {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	require.Equal(t, tc.Expected, result)
}

// These are the same fixtures and expected results used by compare.py.
func TestBenchmarkFixtures(t *testing.T) {
	for _, tc := range loadCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			result, err := bcbp.Parse(tc.Payload)
			checkResult(t, tc, result, err)
		})
	}
}

var parsedPass bcbp.BoardingPass
var parseError error

func BenchmarkBCBP(b *testing.B) {
	for _, tc := range loadCases(b) {
		b.Run(tc.Name, func(b *testing.B) {
			result, err := bcbp.Parse(tc.Payload)
			checkResult(b, tc, result, err)
			b.ReportAllocs()
			// Loop excludes setup and keeps results alive. Assertions and JSON
			// conversion stay outside the measured loop in both languages.
			for b.Loop() {
				parsedPass, parseError = bcbp.Parse(tc.Payload)
			}
			checkResult(b, tc, parsedPass, parseError)
		})
	}
}

func BenchmarkPDFToBCBP(b *testing.B) {
	cases := loadCases(b)
	var sample benchmarkCase
	for _, tc := range cases {
		if tc.Name == "sample" {
			sample = tc
		}
	}
	require.NotEmpty(b, sample.Payload)
	data, err := os.ReadFile("../testdata/boarding_pass.pdf")
	require.NoError(b, err)
	extractor, err := pdf.New()
	require.NoError(b, err)
	parsePDF := func() (bcbp.BoardingPass, error) {
		ctx, cancel := context.WithTimeout(b.Context(), 30*time.Second)
		defer cancel()
		payload, err := extractor.Extract(ctx, data)
		if err != nil {
			return bcbp.BoardingPass{}, err
		}
		return bcbp.Parse(payload)
	}
	result, err := parsePDF()
	checkResult(b, sample, result, err)
	b.ReportAllocs()
	// Includes Poppler processes, temporary files, rendering, barcode decoding,
	// validation and model construction. Excludes HTTP, airport lookup and DB.
	for b.Loop() {
		parsedPass, parseError = parsePDF()
		if parseError != nil {
			require.NoError(b, parseError)
		}
	}
	checkResult(b, sample, parsedPass, parseError)
}
