// Package buildinfo contains release metadata shared by every SuperCli binary
// and frontend. Release builds may replace Version with:
//
//	go build -ldflags "-X supercli/internal/buildinfo.Version=v0.7.0"
package buildinfo

var Version = "0.6.0"
