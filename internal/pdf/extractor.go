// Package pdf renders PDF pages with Poppler and decodes their PDF417 barcodes.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	zxing "github.com/ericlevine/zxinggo"
	"github.com/ericlevine/zxinggo/binarizer"
	"github.com/ericlevine/zxinggo/pdf417"
)

var (
	ErrInvalid      = errors.New("invalid or encrypted PDF")
	ErrNoBarcode    = errors.New("no readable PDF417 barcode found")
	ErrMultiple     = errors.New("PDF contains multiple distinct boarding passes")
	ErrTooManyPages = errors.New("PDF exceeds the 10-page limit")
	errOutputLimit  = errors.New("PDF tool output exceeds its limit")
)

const MaxPages = 10
const maxDimension = 2400

type Extractor struct{ infoPath, renderPath string }

func New() (*Extractor, error) {
	info, err := exec.LookPath("pdfinfo")
	if err != nil {
		return nil, fmt.Errorf("pdfinfo is required (install poppler-utils): %w", err)
	}
	render, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, fmt.Errorf("pdftoppm is required (install poppler-utils): %w", err)
	}
	return &Extractor{infoPath: info, renderPath: render}, nil
}

func (e *Extractor) Extract(ctx context.Context, data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "boarding-pass-*")
	if err != nil {
		return "", fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "input.pdf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}

	info, err := command(ctx, e.infoPath, path)
	if err != nil {
		return "", processError(ctx, err)
	}
	pages, err := pageCount(string(info))
	if err != nil {
		return "", err
	}
	var payload string
	for page := 1; page <= pages; page++ {
		prefix := filepath.Join(dir, "page")
		_, err := command(ctx, e.renderPath, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
			"-singlefile", "-scale-to", strconv.Itoa(maxDimension), "-gray", "-png", path, prefix)
		if err != nil {
			return "", processError(ctx, err)
		}
		results, err := decodeImage(ctx, prefix+".png")
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for _, result := range results {
			if payload != "" && result.Text != payload {
				return "", ErrMultiple
			}
			payload = result.Text
		}
	}
	if payload == "" {
		return "", ErrNoBarcode
	}
	return payload, nil
}

func decodeImage(ctx context.Context, path string) ([]*zxing.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	config, err := png.DecodeConfig(f)
	// Poppler can round a scaled edge up by one pixel.
	if err != nil || config.Width > maxDimension+1 || config.Height > maxDimension+1 {
		return nil, ErrInvalid
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, err := png.Decode(f)
	if err != nil {
		return nil, ErrInvalid
	}
	bitmap := zxing.NewBinaryBitmap(binarizer.NewHybrid(zxing.NewImageLuminanceSource(img)))
	var results []*zxing.Result
	seen := make(map[string]bool)
	// The reader stops at the first orientation containing a barcode. Scan
	// every orientation so another passenger's rotated barcode is not missed.
	reader := pdf417.NewPDF417Reader()
	for rotation := 0; rotation < 4; rotation++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		found, err := reader.DecodeMultiple(bitmap, &zxing.DecodeOptions{TryHarder: true})
		if err != nil && !errors.Is(err, zxing.ErrNotFound) {
			return nil, err
		}
		for _, result := range found {
			if !seen[result.Text] {
				seen[result.Text] = true
				results = append(results, result)
			}
		}
		if rotation < 3 {
			bitmap = bitmap.RotateCounterClockwise()
		}
	}
	return results, nil
}

func pageCount(info string) (int, error) {
	var pages int
	for line := range strings.SplitSeq(info, "\n") {
		if value, ok := strings.CutPrefix(line, "Pages:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			// Metadata can contain newlines. Reject an injected Pages field
			// rather than accepting it before the real count printed by pdfinfo.
			if err != nil || n < 1 || pages != 0 {
				return 0, ErrInvalid
			}
			pages = n
		}
	}
	if pages == 0 {
		return 0, ErrInvalid
	}
	if pages > MaxPages {
		return 0, ErrTooManyPages
	}
	return pages, nil
}

func processError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, errOutputLimit) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// Poppler uses 1 for an unreadable PDF, 3 for PDF permission errors,
	// and 2 for output-file failures. Signals and execution errors are server failures.
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 3) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}

// Retain only a small amount of command output, even for a hostile PDF. No shell
// is involved; user filenames are never passed to external programs.
func command(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output := &boundedBuffer{remaining: 64 << 10}
	stderr := &boundedBuffer{remaining: 4 << 10}
	cmd.Stdout = output
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return output.buffer.Bytes(), fmt.Errorf("%s: %w; stderr: %q", filepath.Base(path), err, stderr.buffer.String())
	}
	if output.truncated {
		return output.buffer.Bytes(), errOutputLimit
	}
	return output.buffer.Bytes(), nil
}

type boundedBuffer struct {
	// Do not embed bytes.Buffer: its ReadFrom would bypass Write in io.Copy.
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, b.remaining)
	_, _ = b.buffer.Write(p[:keep])
	b.remaining -= keep
	b.truncated = b.truncated || keep < n
	return n, nil
}
