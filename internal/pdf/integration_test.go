//go:build integration

package pdf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Race detection and coverage make decoding slower on shared CI runners.
const pdfTestTimeout = time.Minute

func TestRealPDFExtraction(t *testing.T) {
	extractor, err := New()
	require.NoError(t, err)
	sample, err := os.ReadFile("../../testdata/boarding_pass.pdf")
	require.NoError(t, err)
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
			ctx, cancel := context.WithTimeout(t.Context(), pdfTestTimeout)
			defer cancel()
			result, err := extractor.Extract(ctx, tc.data)
			require.ErrorIs(t, err, tc.want)
			if tc.want == nil {
				assert.Contains(t, result, tc.contains)
			}
		})
	}
}

func TestPDFMetadataCannotHidePages(t *testing.T) {
	extractor, err := New()
	require.NoError(t, err)
	data := testutil.PDFWithOptions(t, testutil.PDFOptions{Title: "Fake\nPages: 1\n"}, make([]string, MaxPages+1)...)
	ctx, cancel := context.WithTimeout(t.Context(), pdfTestTimeout)
	defer cancel()
	_, err = extractor.Extract(ctx, data)
	// Older Poppler versions print metadata verbatim; newer ones may sanitize it.
	assert.True(t, errors.Is(err, ErrInvalid) || errors.Is(err, ErrTooManyPages), "unexpected result: %v", err)
}

func TestLargePDFMetadataIsBounded(t *testing.T) {
	extractor, err := New()
	require.NoError(t, err)
	data := testutil.PDFWithOptions(t, testutil.PDFOptions{Title: strings.Repeat("x", 128<<10)}, "")
	ctx, cancel := context.WithTimeout(t.Context(), pdfTestTimeout)
	defer cancel()
	_, err = extractor.Extract(ctx, data)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "output exceeds its limit")
}

func TestSquarePDFRounding(t *testing.T) {
	extractor, err := New()
	require.NoError(t, err)
	for _, size := range []int{500, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), pdfTestTimeout)
			defer cancel()
			data := testutil.PDFWithOptions(t, testutil.PDFOptions{PageSize: size}, "")
			_, err := extractor.Extract(ctx, data)
			assert.ErrorIs(t, err, ErrNoBarcode, "a blank square page is still a valid PDF")
		})
	}
}

func TestBarcodeOrientationsOnSamePage(t *testing.T) {
	extractor, err := New()
	require.NoError(t, err)
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
				ctx, cancel := context.WithTimeout(t.Context(), pdfTestTimeout)
				defer cancel()
				result, err := extractor.Extract(ctx, data)
				if duplicate {
					require.NoError(t, err)
					assert.Equal(t, payload, result)
				} else {
					assert.ErrorIs(t, err, ErrMultiple)
				}
			})
		}
	}
}
