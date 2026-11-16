// Package export renders documentation pages to PDF with the external
// wkhtmltopdf tool (must be installed and on PATH).
package export

import (
	"fmt"
	"os/exec"
)

// RenderPDF converts an HTML file to PDF.
func RenderPDF(htmlPath, pdfPath string) error {
	bin, err := exec.LookPath("wkhtmltopdf")
	if err != nil {
		return fmt.Errorf("export: wkhtmltopdf not installed: %w", err)
	}
	out, err := exec.Command(bin, "--quiet", htmlPath, pdfPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("export: wkhtmltopdf failed: %v: %s", err, out)
	}
	return nil
}
