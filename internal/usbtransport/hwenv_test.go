//go:build hardware

package usbtransport

import "dji-modem-research/internal/testutil"

func init() {
	testutil.LoadDotEnv(".env")
}
