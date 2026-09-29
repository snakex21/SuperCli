// Command release creates a public portable bundle or combines bundle manifests.
package main

import (
	"flag"
	"fmt"
	"os"
	"supercli/internal/buildinfo"
	"supercli/internal/system/releasebundle"
)

func main() {
	var opts releasebundle.Options
	flag.StringVar(&opts.Version, "version", buildinfo.Version, "stable release version")
	flag.StringVar(&opts.OS, "os", "windows", "target operating system")
	flag.StringVar(&opts.Arch, "arch", "amd64", "target architecture")
	flag.StringVar(&opts.CLI, "cli", "supercli.exe", "compiled CLI path")
	flag.StringVar(&opts.GUI, "gui", "supercli-web.exe", "compiled GUI path")
	flag.StringVar(&opts.Source, "source", ".", "repository with public documentation and built-in skills")
	flag.StringVar(&opts.Output, "output", "dist", "portable bundle output directory")
	manifestOnly := flag.Bool("manifest", false, "only combine existing portable bundles into the update manifest")
	flag.Parse()
	if !*manifestOnly {
		file, err := releasebundle.Pack(opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(file)
	}
	if err := releasebundle.WriteManifest(opts.Version, opts.Output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
