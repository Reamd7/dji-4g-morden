//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func preloadWintunDLL() error {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "      preload: os.Executable error: %v\n", err)
		return fmt.Errorf("get executable path: %w", err)
	}
	dllPath := filepath.Join(filepath.Dir(exePath), "wintun.dll")
	fmt.Fprintf(os.Stderr, "      preload: dll=%s\n", dllPath)

	const LOAD_WITH_ALTERED_SEARCH_PATH = 0x00000008
	handle, err := windows.LoadLibraryEx(dllPath, 0, LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "      preload: LoadLibraryEx error: %v\n", err)
		return fmt.Errorf("LoadLibraryEx(%s): %w", dllPath, err)
	}
	fmt.Fprintf(os.Stderr, "      preload: wintun.dll loaded OK (handle=0x%x)\n", handle)
	return nil
}
