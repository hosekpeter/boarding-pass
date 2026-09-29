package pdf

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Equal(t, tc.count, n, tc.info)
		assert.ErrorIs(t, err, tc.err, tc.info)
	}
}

func TestBoundedCommandOutput(t *testing.T) {
	b := &boundedBuffer{remaining: 4}
	n, err := b.Write([]byte("1234567"))
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	_, _ = b.Write([]byte("more"))
	assert.Equal(t, "1234", b.String())
}

func TestEarlyRejection(t *testing.T) {
	extractor := &Extractor{}
	_, err := extractor.Extract(context.Background(), []byte("hello"))
	require.ErrorIs(t, err, ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = extractor.Extract(ctx, []byte("%PDF-1.4"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestImageWithoutBarcode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank.png")
	f, err := os.Create(path)
	require.NoError(t, err)
	img := image.NewGray(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	require.NoError(t, png.Encode(f, img))
	f.Close()
	results, err := decodeImage(t.Context(), path)
	require.NoError(t, err)
	assert.Empty(t, results)
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 50)), 0600))
	_, err = decodeImage(t.Context(), path)
	require.ErrorIs(t, err, ErrInvalid)
}
