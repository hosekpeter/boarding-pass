#!/usr/bin/env python3
"""Compare the real Go and Python parsers on shared, verified inputs."""

import argparse
import hashlib
import importlib.metadata
import json
import math
import os
import platform
import re
import statistics
import subprocess
import sys
import tempfile
import time
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BENCHMARKS = ROOT / "benchmarks"


def run(command, **kwargs):
    result = subprocess.run(command, capture_output=True, text=True, **kwargs)
    if result.returncode:
        raise RuntimeError(
            f"Command failed: {command}\n{result.stdout}\n{result.stderr}"
        )
    return result.stdout


def fingerprint(base, paths):
    digest = hashlib.sha256()
    for path in sorted(paths):
        digest.update(str(path.relative_to(base)).encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def cpu_name():
    path = Path("/proc/cpuinfo")
    if path.exists():
        for line in path.read_text().splitlines():
            if line.startswith("model name"):
                return line.split(":", 1)[1].strip()
    return platform.processor() or "unknown"


def python_sample(operation, iterations):
    started = time.perf_counter_ns()
    result = None
    for _ in range(iterations):
        result = operation()
    elapsed = time.perf_counter_ns() - started
    # Keep the final result alive until after the clock stops, as in Go.
    del result
    return {"iterations": iterations, "ns_per_op": elapsed / iterations}


def calibrate(operation, seconds):
    iterations = 1
    while True:
        sample = python_sample(operation, iterations)
        elapsed = sample["ns_per_op"] * iterations / 1e9
        if elapsed >= 0.02:
            return max(1, math.ceil(iterations * seconds / elapsed))
        iterations *= 10


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--python-project", type=Path, default=ROOT.parent / "boarding-pass-python"
    )
    parser.add_argument("--rounds", type=int, default=5)
    parser.add_argument(
        "--seconds", type=float, default=0.5, help="target seconds per BCBP sample"
    )
    parser.add_argument("--pdf-iterations", type=int, default=3)
    parser.add_argument(
        "--output", type=Path, default=BENCHMARKS / "results.local.json"
    )
    args = parser.parse_args()
    if (
        args.rounds < 3
        or not math.isfinite(args.seconds)
        or args.seconds <= 0
        or args.pdf_iterations < 1
    ):
        parser.error(
            "use at least 3 rounds, positive seconds, and at least 1 PDF iteration"
        )

    source = args.python_project.resolve() / "src"
    if not (source / "boarding_pass" / "bcbp.py").is_file():
        parser.error(
            "--python-project must point to the existing boarding-pass-python repository"
        )
    # The PDF worker must import the same source using the same interpreter.
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(source))
    os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
    os.environ["PYTHONPATH"] = str(source)
    from boarding_pass.bcbp import parse
    from boarding_pass.errors import APIError
    from boarding_pass.pdf import extract

    cases = json.loads((BENCHMARKS / "cases.json").read_text())
    pdf = (ROOT / "testdata" / "boarding_pass.pdf").read_bytes()
    sample_case = next(case for case in cases if case["name"] == "sample")
    go_sources = [ROOT / "go.mod", ROOT / "go.sum"] + [
        path
        for directory in ("bcbp", "pdf")
        for path in (ROOT / "internal" / directory).glob("*.go")
        if not path.name.endswith("_test.go")
    ]
    python_sources = list((source / "boarding_pass").rglob("*.py"))
    sources_before = {
        "go": fingerprint(ROOT, go_sources),
        "python": fingerprint(source, python_sources),
    }

    operations = {}
    for case in cases:
        payload = case["payload"]
        if case["invalid"]:

            def invalid_operation(payload=payload):
                try:
                    parse(payload)
                except APIError as error:
                    if error.code != "invalid_bcbp":
                        raise
                    return None
                raise RuntimeError("Python accepted the invalid benchmark fixture")

            operations[case["name"]] = invalid_operation
            invalid_operation()
        else:
            result = parse(payload).model_dump(mode="json")
            if result != case["expected"]:
                raise RuntimeError(f"Python output differs for {case['name']}")
            operations[case["name"]] = lambda payload=payload: parse(payload)
    payload = extract(pdf)
    if (
        payload != sample_case["payload"]
        or parse(payload).model_dump(mode="json") != sample_case["expected"]
    ):
        raise RuntimeError("Python PDF output differs from the shared fixture")
    operations["pdf_to_bcbp"] = lambda: parse(extract(pdf))

    iterations = {
        name: args.pdf_iterations
        if name == "pdf_to_bcbp"
        else calibrate(operation, args.seconds)
        for name, operation in operations.items()
    }
    results = {name: {"go": [], "python": []} for name in operations}
    raw_go = []
    benchmark_line = re.compile(
        r"^Benchmark(BCBP/\w+|PDFToBCBP)(?:-\d+)?\s+(\d+)\s+([\d.]+)\s+ns/op",
        re.MULTILINE,
    )

    with tempfile.TemporaryDirectory(prefix="boarding-pass-benchmark-") as directory:
        binary = str(Path(directory) / "benchmark.test")
        run(["go", "test", "-c", "-o", binary, "./benchmarks"], cwd=ROOT)
        run([binary, "-test.run=^TestBenchmarkFixtures$"], cwd=BENCHMARKS)
        print(
            "Shared BCBP fixtures verified; each PDF pipeline checks its output before timing.",
            flush=True,
        )

        def measure_go():
            seen = set()
            for pattern, duration in (
                ("^BenchmarkBCBP$", f"{args.seconds}s"),
                ("^BenchmarkPDFToBCBP$", f"{args.pdf_iterations}x"),
            ):
                output = run(
                    [
                        binary,
                        "-test.run=^$",
                        f"-test.bench={pattern}",
                        f"-test.benchtime={duration}",
                        "-test.benchmem",
                        "-test.cpu=1",
                    ],
                    cwd=BENCHMARKS,
                )
                raw_go.append(output)
                for line in output.splitlines():
                    match = benchmark_line.match(line)
                    if not match:
                        continue
                    name = (
                        "pdf_to_bcbp"
                        if match[1] == "PDFToBCBP"
                        else match[1].split("/", 1)[1]
                    )
                    if name not in results or name in seen:
                        raise RuntimeError(f"Unexpected Go benchmark: {name}")
                    seen.add(name)
                    entry = {"iterations": int(match[2]), "ns_per_op": float(match[3])}
                    for field, unit in (
                        ("bytes_per_op", "B/op"),
                        ("allocs_per_op", "allocs/op"),
                    ):
                        value = re.search(r"(\d+)\s+" + re.escape(unit), line)
                        if value:
                            entry[field] = int(value[1])
                    results[name]["go"].append(entry)
            if seen != set(operations):
                raise RuntimeError(
                    f"Missing Go benchmark results: {set(operations) - seen}"
                )

        def measure_python():
            for name, operation in operations.items():
                results[name]["python"].append(
                    python_sample(operation, iterations[name])
                )

        for round_index in range(args.rounds):
            # Alternating the order limits a systematic warm-up/thermal bias.
            order = (
                (measure_go, measure_python)
                if round_index % 2 == 0
                else (measure_python, measure_go)
            )
            for measure in order:
                measure()
            print(f"Round {round_index + 1}/{args.rounds} complete.", flush=True)

    sources_after = {
        "go": fingerprint(ROOT, go_sources),
        "python": fingerprint(source, python_sources),
    }
    if sources_after != sources_before:
        raise RuntimeError(
            "Source files changed during the run; discard these measurements and retry"
        )
    summary = {}
    for name, samples in results.items():
        summary[name] = {}
        for language, values in samples.items():
            times = [value["ns_per_op"] for value in values]
            summary[name][language] = {
                "median_ns_per_op": statistics.median(times),
                "min_ns_per_op": min(times),
                "max_ns_per_op": max(times),
            }
        summary[name]["python_over_go"] = (
            summary[name]["python"]["median_ns_per_op"]
            / summary[name]["go"]["median_ns_per_op"]
        )

    poppler = subprocess.run(
        ["pdftoppm", "-v"], capture_output=True, text=True, check=True
    )
    report = {
        "measured_at_utc": datetime.now(UTC).isoformat(),
        "environment": {
            "cpu": cpu_name(),
            "os": platform.platform(),
            "go": run(["go", "version"]).strip(),
            "python": sys.version,
            "python_implementation": platform.python_implementation(),
            "python_packages": {
                name: importlib.metadata.version(name)
                for name in ("pydantic", "pypdfium2", "zxing-cpp", "Pillow")
            },
            "poppler": (poppler.stderr or poppler.stdout).splitlines()[0],
            "go_decoder": run(
                ["go", "list", "-m", "github.com/ericlevine/zxinggo"], cwd=ROOT
            ).strip(),
        },
        "settings": {
            "rounds": args.rounds,
            "target_bcbp_seconds": args.seconds,
            "pdf_iterations_per_sample": args.pdf_iterations,
            "go_gomaxprocs": 1,
            "race_and_coverage": False,
            "concurrent_requests": 1,
            "gc": "enabled in both runtimes",
        },
        "source_sha256": sources_before,
        "fixtures_sha256": hashlib.sha256(
            (BENCHMARKS / "cases.json").read_bytes()
        ).hexdigest(),
        "pdf_sha256": hashlib.sha256(pdf).hexdigest(),
        "scope": "BCBP validation and native model construction; PDF case also includes production rendering/decoding and child-process startup. No HTTP, airport API, database, or JSON response serialization.",
        "summary": summary,
        "samples": results,
        "raw_go_output": raw_go,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print("\nMedian per operation (5+ rounds recommended):")
    for name, row in summary.items():
        print(
            f"{name:20s} Go {row['go']['median_ns_per_op'] / 1000:12.3f} us | Python {row['python']['median_ns_per_op'] / 1000:12.3f} us | Python/Go {row['python_over_go']:.2f}x"
        )
    print(f"\nRaw measurements and environment: {args.output}")


if __name__ == "__main__":
    main()
