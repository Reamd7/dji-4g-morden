package modem

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// config.go — Function level reset and Quectel extended configuration.
// Both vohive-sourced. See plans/at-commands-roadmap.md Phase C.

// SetFunctionLevel sets the modem's functional level (AT+CFUN=<fun>[,<rst>]).
// fun: 0=minimum, 1=full, 4=airplane (RF off). If reset is true, appends ,1
// to trigger a modem restart (AT+CFUN=1,1). See EC25 AT Commands Manual §2.22.
func (m *Modem) SetFunctionLevel(ctx context.Context, fun int, reset bool) error {
	if reset {
		_, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CFUN=%d,1", fun), 20*time.Second)
		return err
	}
	_, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CFUN=%d", fun), 5*time.Second)
	return err
}

// SetQCFG sets a Quectel extended configuration parameter
// (AT+QCFG="<type>"[,<args>...]). See EC25 AT Commands Manual §4.3.
// Example: SetQCFG(ctx, "urc/cache", "1")
func (m *Modem) SetQCFG(ctx context.Context, cfgType string, args ...string) error {
	parts := []string{fmt.Sprintf(`"%s"`, cfgType)}
	parts = append(parts, args...)
	_, err := m.SendAndWait(ctx, "AT+QCFG="+strings.Join(parts, ","), 5*time.Second)
	return err
}
