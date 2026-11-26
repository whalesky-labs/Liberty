//go:build companion3d

// A profile-free review tool. It does not replace or launch the Liberty client.
package main

import (
	"flag"
	"fmt"
	"liberty.local/liberty/internal/desktop"
	"os"
)

func main() {
	locale := flag.String("locale", "zh-CN", "zh-CN or en-US")
	assets := flag.String("assets", "", "absolute path to the reviewed pixel action library")
	flag.Parse()
	if *locale != "zh-CN" && *locale != "en-US" {
		fmt.Fprintln(os.Stderr, "unsupported_locale")
		os.Exit(2)
	}
	if err := desktop.RunCompanion3DPreview(*locale, *assets); err != nil {
		fmt.Fprintln(os.Stderr, "companion_3d_preview_unavailable")
		os.Exit(1)
	}
}
