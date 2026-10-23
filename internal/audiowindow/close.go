package audiowindow

import "sync/atomic"

// ClosePolicy separates the native close button from explicit application shutdown.
// Wails HideWindowOnClose bypasses OnBeforeClose; callers must leave that option disabled.
type ClosePolicy struct{ quitting atomic.Bool }

func (p *ClosePolicy) BeginQuit() { p.quitting.Store(true) }
func (p *ClosePolicy) BeforeClose(hide func()) bool {
	if p.quitting.Load() {
		return false
	}
	hide()
	return true
}
