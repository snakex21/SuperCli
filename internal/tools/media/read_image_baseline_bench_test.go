package media

// Exact c5afaf85 Execute implementation for reproducible fresh-main A/B benchmarks.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

func (t *ReadImageTool) executeBaseline(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path        string               `json:"path"`
		ImageDetail string               `json:"image_detail"`
		Crop        *AnalysisImageRegion `json:"crop"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_image: bad args: %w", err)}, err
	}
	if params.ImageDetail != "" && params.ImageDetail != "auto" && params.ImageDetail != "original" {
		err := fmt.Errorf("read_image: image_detail must be auto or original")
		return Result{Err: err}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_image: path is required")
		return Result{Err: err}, err
	}

	full, err := sandbox.ResolveSafe(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("read_image: %w", err)}, nil
	}

	f, err := os.Open(full)
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_image: %q is a directory", full)
		return Result{Err: err}, err
	}
	if !info.Mode().IsRegular() {
		err := fmt.Errorf("read_image: %q is not a regular file", full)
		return Result{Err: err}, err
	}
	limit := t.MaxBytes
	if limit > DefaultMaxScreenshotBytes {
		limit = DefaultMaxScreenshotBytes
	}
	if info.Size() > limit {
		err := fmt.Errorf("read_image: file too large: %d bytes > %d max", info.Size(), limit)
		return Result{Err: err}, err
	}

	// Keep the opened descriptor and cap the read even if the file grows after Stat.
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}

	if int64(len(data)) > limit {
		err := fmt.Errorf("read_image: file grew beyond %d max bytes", limit)
		return Result{Err: err}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	mime := detectImageMIME(data)
	if mime == "" {
		err := fmt.Errorf("read_image: %q is not a recognised image (magic bytes)", filepath.Base(full))
		return Result{Err: err}, err
	}

	analysisData, analysisType, analysis, err := prepareAnalysisImageRegion(ctx, data, mime, params.ImageDetail, params.Crop)
	if err != nil {
		return Result{Err: err}, err
	}
	text := fmt.Sprintf("Loaded image %s (%d bytes, %s)", params.Path, info.Size(), mime)
	if analysis.Resized || params.Crop != nil {
		metadata, _ := json.Marshal(analysis)
		text += "\nAnalysis image: " + string(metadata)
	}
	return Result{
		Text:  text,
		Image: &ImageContent{MediaType: analysisType, Data: analysisData},
	}, nil
}
