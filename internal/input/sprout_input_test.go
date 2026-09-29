package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"testing"
)

func TestSproutCapturesPaneKey(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o.ShowSprout = true
	HandleInput(tea.KeyPressMsg{Code: 'f', Text: "f"}, o)
	if string(pty.got) != "" {
		t.Fatalf("sprout leaked %q to pane", pty.got)
	}
	if o.Sprout.Filter != 1 {
		t.Fatalf("sprout did not receive key: %d", o.Sprout.Filter)
	}
}
