package pdf

import (
	"bytes"
	"context"
	"fmt"
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
	t.Parallel()

	for _, tc := range []struct {
		info  string
		count int
		err   error
	}{
		{"Title: Sample\nPages: 2\n", 2, nil}, {"Pages: 10", 10, nil}, {"Pages: 11", 0, ErrTooManyPages},
		{"Pages: -1", 0, ErrInvalid}, {"Pages: zero", 0, ErrInvalid}, {"missing", 0, ErrInvalid},
		{"Title: Fake\nPages: 1\nPages: 11\n", 0, ErrInvalid},
		{"Pages: 2\nPages: 2\n", 0, ErrInvalid},
	} {
		n, err := pageCount(tc.info)
		assert.Equal(t, tc.count, n, tc.info)
		assert.ErrorIs(t, err, tc.err, tc.info)
	}
}

func TestBoundedCommandOutput(t *testing.T) {
	t.Parallel()

	b := &boundedBuffer{remaining: 4}
	n, err := b.Write([]byte("1234567"))
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	_, _ = b.Write([]byte("more"))
	assert.Equal(t, "1234", b.buffer.String())
	assert.True(t, b.truncated)
}

// Subprocess tests change the process environment and must stay sequential.
func TestActualCommandOutputIsBounded(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("PDF_TEST_COMMAND", "large-output")
	output, err := command(t.Context(), executable, "-test.run=^TestPDFCommandHelper$")
	require.ErrorIs(t, err, errOutputLimit)
	assert.Len(t, output, 64<<10)
	assert.ErrorIs(t, processError(t.Context(), err), ErrInvalid)
}

func TestCommandFailures(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, code := range []int{1, 2, 3, 99} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Setenv("PDF_TEST_COMMAND", fmt.Sprint(code))
			_, err := command(t.Context(), executable, "-test.run=^TestPDFCommandHelper$")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "diagnostic")
			assert.Less(t, len(err.Error()), 5000, "stderr must also be bounded")
			err = processError(t.Context(), err)
			if code == 1 || code == 3 {
				assert.ErrorIs(t, err, ErrInvalid)
			} else {
				assert.NotErrorIs(t, err, ErrInvalid)
			}
		})
	}
	_, err = command(t.Context(), filepath.Join(t.TempDir(), "missing-pdftoppm"))
	require.Error(t, err)
	assert.NotErrorIs(t, processError(t.Context(), err), ErrInvalid)
}

// A real child process exercises os/exec's io.Copy path, including ReaderFrom.
func TestPDFCommandHelper(t *testing.T) {
	mode := os.Getenv("PDF_TEST_COMMAND")
	if mode == "" {
		return
	}
	if mode == "large-output" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 1<<20))
		os.Exit(0)
	}
	_, _ = fmt.Fprint(os.Stderr, "diagnostic: "+strings.Repeat("x", 1<<20))
	var code int
	_, _ = fmt.Sscan(mode, &code)
	os.Exit(code)
}

func TestEarlyRejection(t *testing.T) {
	t.Parallel()

	extractor := &Extractor{}
	_, err := extractor.Extract(context.Background(), []byte("hello"))
	require.ErrorIs(t, err, ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = extractor.Extract(ctx, []byte("%PDF-1.4"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestImageWithoutBarcode(t *testing.T) {
	t.Parallel()

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
