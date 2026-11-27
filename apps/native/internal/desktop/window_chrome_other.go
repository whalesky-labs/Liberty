//go:build !windows

package desktop

import "github.com/egoist/mygo"

func configureWindowChrome(*mygo.Window, mygo.WindowOptions) {}
func syncWindowChrome()                                      {}
