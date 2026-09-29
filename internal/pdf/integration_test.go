//go:build integration

package pdf

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/testutil"
)

func TestRealPDFExtraction(t *testing.T) {
	extractor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile("../../testdata/boarding_pass.pdf")
	if err != nil {
		t.Fatal(err)
	}
	payload := "M1CYPRIAN/MICHAL       ZKN85B KSCPRGFR 7774 105Y029C0126 100"
	other := strings.Replace(payload, "CYPRIAN/MICHAL", "EXAMPLE/ALICE ", 1)
	tests := []struct {
		name     string
		data     []byte
		want     error
		contains string
	}{
		{"assignment PDF", sample, nil, "CYPRIAN/MICHAL"},
		{"corrupt", []byte("%PDF-1.4\nnot a document"), ErrInvalid, ""},
		{"blank", testutil.PDF(t, ""), ErrNoBarcode, ""},
		{"second page", testutil.PDF(t, "", payload), nil, payload},
		{"duplicate barcode", testutil.PDF(t, payload, payload), nil, payload},
		{"distinct barcodes", testutil.PDF(t, payload, other), ErrMultiple, ""},
		{"too many pages", testutil.PDF(t, make([]string, MaxPages+1)...), ErrTooManyPages, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result, err := extractor.Extract(ctx, tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && !strings.Contains(result, tc.contains) {
				t.Fatalf("unexpected payload %q", result)
			}
		})
	}
}

func TestBarcodeOrientationsOnSamePage(t *testing.T) {
	extractor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	payload := "M1CYPRIAN/MICHAL       ZKN85B KSCPRGFR 7774 105Y029C0126 100"
	other := strings.Replace(payload, "CYPRIAN/MICHAL", "EXAMPLE/ALICE ", 1)
	for _, transform := range []string{"1 0 0 1 30 450", "0 1 -1 0 730 30", "-1 0 0 -1 730 730", "0 -1 1 0 500 730"} {
		for _, duplicate := range []bool{false, true} {
			second := other
			if duplicate {
				second = payload
			}
			name := transform + "/distinct"
			if duplicate {
				name = transform + "/duplicate"
			}
			t.Run(name, func(t *testing.T) {
				data := testutil.BarcodePDF(t, []testutil.PDFBarcode{
					{Payload: payload, Transform: "1 0 0 1 30 30"},
					{Payload: second, Transform: transform},
				})
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				result, err := extractor.Extract(ctx, data)
				if duplicate {
					if err != nil || result != payload {
						t.Fatalf("duplicate barcode: %q, %v", result, err)
					}
				} else if !errors.Is(err, ErrMultiple) {
					t.Fatalf("distinct barcodes returned %q, %v", result, err)
				}
			})
		}
	}
}
