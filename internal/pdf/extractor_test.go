package pdf

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageCount(t *testing.T) {
	for _, tc := range []struct {
		info  string
		count int
		err   error
	}{
		{"Title: Sample\nPages: 2\n", 2, nil}, {"Pages: 10", 10, nil}, {"Pages: 11", 0, ErrTooManyPages},
		{"Pages: -1", 0, ErrInvalid}, {"Pages: zero", 0, ErrInvalid}, {"missing", 0, ErrInvalid},
	} {
		n, err := pageCount(tc.info)
		if n != tc.count || !errors.Is(err, tc.err) {
			t.Fatalf("%q: %d, %v", tc.info, n, err)
		}
	}
}

func TestBoundedCommandOutput(t *testing.T) {
	b := &boundedBuffer{remaining: 4}
	n, err := b.Write([]byte("1234567"))
	if n != 7 || err != nil {
		t.Fatalf("write: %d %v", n, err)
	}
	_, _ = b.Write([]byte("more"))
	if b.String() != "1234" {
		t.Fatalf("unbounded output: %q", b.String())
	}
}

func TestEarlyRejection(t *testing.T) {
	extractor := &Extractor{}
	if _, err := extractor.Extract(context.Background(), []byte("hello")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := extractor.Extract(ctx, []byte("%PDF-1.4")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestImageWithoutBarcode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	results, err := decodeImage(t.Context(), path)
	if err != nil || len(results) != 0 {
		t.Fatalf("unexpected barcodes: %v %v", results, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 50)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeImage(t.Context(), path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid image: %v", err)
	}
}
