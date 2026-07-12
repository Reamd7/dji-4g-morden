//go:build hardware

package qmitransport

import "dji-modem-research/internal/testutil"

func init() {
	testutil.LoadDotEnv(".env")
}
