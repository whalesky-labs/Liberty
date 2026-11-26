//go:build !darwin

package presentation

func systemColors(dark bool) SystemColors { return fallbackSystemColors(dark) }
