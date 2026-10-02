package media

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

// Compare prepared coefficients with geometric pixel overlap. This also covers
// fractional ratios and targets larger than the source, rather than only 4K.
func TestAnalysisAreaPreparedWeightsMatchOverlap(t *testing.T) {
	for _, source := range []int{1, 2, 3, 17, 257, 1280, 1537, 3840, 8193} {
		for _, target := range []int{1, 2, 3, 7, 113, 511, 1280} {
			areas := analysisAreas(source, target)
			if len(areas) != target {
				t.Fatal("area count")
			}
			scale := float64(source) / float64(target)
			for i, area := range areas {
				left, right := float64(i)*scale, float64(i+1)*scale
				if area.span != right-left {
					t.Fatalf("span %d/%d [%d]", source, target, i)
				}
				for position := area.first; position <= area.last; position++ {
					want := math.Min(right, float64(position+1)) - math.Max(left, float64(position))
					if got := area.weight(position); got != want {
						t.Fatalf("weight %d/%d [%d] pixel=%d got=%.17g want=%.17g", source, target, i, position, got, want)
					}
				}
			}
		}
	}
}

func TestAnalysisAreaScaleCancellationRemainsImmediate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := analysisAreaScale(ctx, analysisPattern(4, 3), 2, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

type analysisCancelPixel struct {
	image.Image
	cancel context.CancelFunc
}

func (i analysisCancelPixel) At(x, y int) color.Color {
	i.cancel()
	return i.Image.At(x, y)
}

func TestAnalysisAreaScaleCancellationAtLastPixel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := analysisCancelPixel{Image: analysisPattern(1, 1), cancel: cancel}
	output, err := analysisAreaScale(ctx, source, 1, 1)
	if output != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("last pixel returned %v, %v", output, err)
	}
}
