# Attachment upload: bounded streaming before 1.0.2

The GUI attachment endpoint used `ParseMultipartForm(8 << 20)`, then copied each uploaded file a second time into its final staging folder. Multipart parsing kept large byte buffers and spilled files larger than the memory threshold into the operating system temporary directory.

The endpoint now uses `MultipartReader` and copies each supported file part directly into the existing staging folder. It does not retain a parsed form or create a multipart temporary file. The profile-source upload uses the same path. No extra dependency, model instruction, model call or background process was added.

The request-body, individual-file, total-byte and file-count bounds still apply. A failed, oversized or malformed request removes its partially staged upload directory. Successful filenames, preview paths and response fields remain compatible. Both `files` and `file` form fields are accepted.

## Measurement

Windows x64, Ryzen 7 5800X3D, Go 1.26.2. `BenchmarkAttachmentUpload9MiB`, three 200 ms samples before and after. The benchmark sends the same 9 MiB file through the actual HTTP handler and measures copying to disk; creating the input fixture and deleting the finished upload are outside its timer.

| Metric | Before, median | After, median |
| --- | ---: | ---: |
| Time per upload | 24.87 ms | 20.66 ms |
| Allocated bytes per upload | 33,653,008 | 53,568 |
| Allocations per upload | 135 | 82 |

This is approximately 99.8% fewer allocated bytes for this upload case. These are cumulative Go allocations per operation, not resident memory or a claim about WebView2 idle RAM. Disk/cache state affects timing; the main reproducible gain is avoiding the whole-file multipart buffer and extra temporary file.

## Verification

- The 9 MiB uploaded file has the same SHA256 as the input.
- The large-file regression disables every supported OS temporary-directory location, proving that successful staging does not require an OS-temp spill.
- Over-limit file count, an oversized file and a truncated multipart body return an error with no partially staged files left behind.
- Existing image preview, profile upload, delete and workspace-change tests pass.

Private benchmark logs and the previous handler are retained in the ignored `.tmp/release-1.0.2/` folder.
