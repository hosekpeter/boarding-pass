# Go / Python parser benchmarks

These benchmarks compare the **actual implementations** in this Go repository
and the separate `boarding-pass-python` repository. They do not establish a
general performance ranking of Go and Python.

## Run Go benchmarks

Requires the project's Go version. The PDF benchmark also requires Poppler
(`pdfinfo` and `pdftoppm`); no PostgreSQL or network service is needed.

```bash
make bench
```

For just the BCBP parser, without Poppler:

```bash
go test ./benchmarks -run '^$' -bench '^BenchmarkBCBP$' -benchmem -count=5 -cpu=1
```

The output includes time per operation and Go allocations. Run benchmarks
without the race detector or coverage instrumentation.

## Compare with the Python application

Use Python 3.12+ from the Python project's virtual environment with its runtime
requirements installed. The benchmark imports that project's parser, models,
and PDF extractor directly; it does not contain a replacement Python parser.
The Python checkout is read without modifying its source files.

With the repositories next to each other:

```bash
make bench-compare PYTHON=../boarding-pass-python/.venv/bin/python
```

For another checkout location or measurement settings:

```bash
/path/to/python-project/.venv/bin/python benchmarks/compare.py \
  --python-project /path/to/python-project \
  --rounds 5 --seconds 0.5 --pdf-iterations 3 \
  --output benchmarks/results.local.json
```

The runner prints median timings and writes all individual samples, raw Go
benchmark output, runtime/library versions, CPU information, source hashes and
input hashes to JSON. It refuses to finish if the implementation source changes
during measurement. `results.local.json` is ignored by Git; the checked-in
[results.json](results.json) records the measured baseline.

## What is measured

| Case | Work performed |
| --- | --- |
| `sample` | Parse the assignment PDF's decoded BCBP, including validation and one flight model. |
| `four_legs` | Parse an itinerary with four flight legs, including validation and all models. |
| `invalid_truncated` | Reject a payload whose declared optional data is truncated. |
| `pdf_to_bcbp` | Render the same original PDF, decode its PDF417 barcode and parse the resulting BCBP. |

Both versions read the same [cases.json](cases.json), validate expected model
values and reject the same invalid case before timing. The PDF case reads the
same `testdata/boarding_pass.pdf` into memory before timing and verifies its
parsed output. Go fixture checks also run under normal `go test`.

The comparison uses five repeated samples by default, alternates which language
runs first, and reports medians plus the minimum/maximum of the sample averages.
BCBP loops target 0.5 seconds per sample; PDF samples use three operations each.
Go compilation, imports, fixture loading, initial correctness checks, JSON
serialization of results and reporting are excluded. Assertions are outside
successful measured loops. Garbage collection stays enabled in both runtimes.
Only one operation runs at a time; Go uses `GOMAXPROCS=1`. The CPU is not pinned
and other work on the machine can affect timings. Sample ranges are not request
latency percentiles.

## How to interpret the comparison

- Python builds Pydantic models and uses regular expressions; Go builds native
  structs and validates fields with loops. Those costs are included because they
  are part of the actual parsers.
- The PDF stacks differ: Go uses **Poppler + zxinggo**; Python uses
  **PDFium + zxing-cpp**, whose heavy work executes in native C/C++ libraries.
  Both cap page dimensions at 2400 pixels but use their own production rendering
  settings and orientation handling. Go scans all four orientations explicitly.
- Child-process startup and temporary files created by each production PDF
  pipeline are included. The Python worker is started afresh for each operation.
- HTTP handling, airport enrichment, PostgreSQL and JSON response serialization
  are excluded. These numbers describe parsing, not complete endpoint latency
  or concurrent throughput. The PDF comparison covers the supplied sample, not
  equivalence on every supported or malformed PDF.
- Go `B/op` and `allocs/op` cover allocations tracked by the Go runtime; they do
  not include Poppler's memory. There is no cross-language memory comparison.

## Recorded baseline

Measured 2026-09-29T10:17:43.961009+00:00 on AMD Ryzen 7 4800H with Radeon Graphics,
go version go1.27.1 linux/amd64; CPython 3.14.4.
Medians of 5 sample averages; ranges show the smallest/largest sample averages.

| Case | Go median (range) | Python median (range) | Faster implementation |
| --- | --- | --- | --- |
| `sample` | 0.368 µs (0.356–0.420) | 17.033 µs (16.233–17.725) | Go 46.26× |
| `four_legs` | 1.020 µs (0.967–1.095) | 41.591 µs (38.558–46.370) | Go 40.78× |
| `invalid_truncated` | 0.642 µs (0.614–0.682) | 10.734 µs (10.625–11.149) | Go 16.73× |
| `pdf_to_bcbp` | 1607.596 ms (1556.840–1642.260) | 316.157 ms (275.026–372.466) | Python 5.08× |

The PDF stage dominates the sample document’s processing time. This baseline
does not measure full API latency or isolate the effect of the language from
its libraries. The Python source is an external working checkout; its exact
source fingerprint is recorded in `results.json`.
