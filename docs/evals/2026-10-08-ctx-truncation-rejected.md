# Rejected command-result projection experiment, 2026-10-08

The existing command-result projection already removes a long exact command echo.
A candidate additionally omitted both `truncated_stdout:false` and
`truncated_stderr:false` when neither stream was truncated. True/mixed states,
public text, retained output, errors and schemas were unchanged.

The frozen overlay and actual Registry → CtxExecute → OutputStore → OpenAI
request tests passed for native and dispatcher calls, success/failure, encoded
arguments and parity/fuzz seeds. Omitting the fields saves exactly 50 bytes of
projected JSON and 54 bytes of escaped HTTP request JSON per eligible result.

Three runs per case (300 ms) showed median projection times of 1,717→1,479 ns
for 512-byte stdout, 5,350→4,589 ns for 2 KiB and 18,876→16,897 ns for 8 KiB.
Unchanged controls also improved by about 8–10%, so these numbers do not prove
an attributable CPU gain. The larger shadow struct slightly increased transient
allocations in the 2/8 KiB cases. No live quality/latency result was established.

The candidate was **not adopted**: its small byte saving did not justify replacing
explicit false status with absence or adding compensating instructions. Production
command output remains unchanged. Overlay, raw benchmark logs and decision data
are retained in ignored `.tmp/optimization-oct8-2026/ctx-truncation-view`.
