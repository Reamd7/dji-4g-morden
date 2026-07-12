//go:build hardware

package qmidatapath

import "dji-modem-research/internal/testutil"

func init() {
	testutil.LoadDotEnv(".env")
}
