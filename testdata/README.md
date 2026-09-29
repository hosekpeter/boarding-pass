`boarding_pass.pdf` is the original example from the assignment:
https://github.com/mcyprian/interview_task/blob/main/boarding_pass.pdf

It is intentionally retained as an integration-test fixture. Its PDF417 payload
is decoded from the barcode, not inferred from visible text or hardcoded into
the application. All synthetic unit-test data is kept in the test source files.
