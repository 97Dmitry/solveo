package audiowindow

import "testing"

func TestRendererCloseDistinguishesUserHideAndParentShutdown(t *testing.T) {
	var policy ClosePolicy
	hidden := 0
	if !policy.BeforeClose(func() { hidden++ }) || hidden != 1 {
		t.Fatal("user close must hide and cancel process quit")
	}
	policy.BeginQuit()
	if policy.BeforeClose(func() { hidden++ }) || hidden != 1 {
		t.Fatal("parent shutdown must quit without hiding")
	}
}
