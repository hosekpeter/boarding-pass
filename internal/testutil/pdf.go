//go:build integration

package testutil

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/pdf417"
)

// PDFBarcode positions a vector barcode using a PDF transformation matrix.
type PDFBarcode struct {
	Payload   string
	Transform string
}

// PDF places one barcode on each page; an empty payload makes a blank page.
func PDF(t testing.TB, payloads ...string) []byte {
	t.Helper()
	pages := make([][]PDFBarcode, len(payloads))
	for i, payload := range payloads {
		if payload != "" {
			pages[i] = []PDFBarcode{{Payload: payload, Transform: "1 0 0 1 30 30"}}
		}
	}
	return BarcodePDF(t, pages...)
}

// BarcodePDF produces valid PDFs without depending on the production decoder.
func BarcodePDF(t testing.TB, pages ...[]PDFBarcode) []byte {
	t.Helper()
	var kids strings.Builder
	for i := range pages {
		fmt.Fprintf(&kids, "%d 0 R ", 3+2*i)
	}
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), kids.String())}
	for i, page := range pages {
		var content strings.Builder
		for _, symbol := range page {
			matrix := testBarcode(t, symbol.Payload)
			fmt.Fprintf(&content, "q %s cm 0 g\n", symbol.Transform)
			for y := 0; y < matrix.Bounds().Dy(); y++ {
				for x := 0; x < matrix.Bounds().Dx(); x++ {
					r, _, _, _ := matrix.At(x, y).RGBA()
					if r == 0 {
						fmt.Fprintf(&content, "%d %d 2 2 re f\n", 2*x, 2*(matrix.Bounds().Dy()-1-y))
					}
				}
			}
			content.WriteString("Q\n")
		}
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 800 800] /Resources << >> /Contents %d 0 R >>", 4+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

func testBarcode(t testing.TB, payload string) barcode.Barcode {
	t.Helper()
	// boombuler v1.1.0 misencodes the left row-count indicator unless the
	// number of rows is divisible by three. Vary ECC to get a valid fixture;
	// do not adapt the production reader to malformed test barcodes.
	for level := byte(2); level <= 8; level++ {
		matrix, err := pdf417.Encode(payload, level)
		if err == nil && (matrix.Bounds().Dy()/2)%3 == 0 {
			return matrix
		}
	}
	t.Fatal("could not generate a PDF417 fixture with consistent row indicators")
	return nil
}
