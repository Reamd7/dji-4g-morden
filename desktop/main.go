// Verification probe: can an independent module named dji-modem-research/desktop
// (replace => ..) import the PARENT's internal/ packages?
// Go internal rule is import-path-prefix based: dji-modem-research/desktop
// is rooted under dji-modem-research/, so internal/ should be visible.
package main

import (
	"fmt"

	_ "dji-modem-research/internal/usbdesc" // blank import: proves path + internal visibility
)

func main() {
	fmt.Println("desktop build OK: parent internal/ importable across module boundary")
}
