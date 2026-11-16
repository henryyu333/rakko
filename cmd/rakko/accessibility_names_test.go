package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/locales"
)

func TestScreenshotToolbarAccessibleNames(t *testing.T) {
	bounds := mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}
	sel := mygo.Rectangle{X: 300, Y: 200, Width: 400, Height: 240}
	d := &shotDisplay{shotTarget: shotTarget{ID: 1, Bounds: bounds, Scale: 1}}
	s := &shotSession{
		displays: []*shotDisplay{d},
		hasSel:   true,
		sel:      sel,
		tool:     shotToolRect,
		color:    shotAnnotationColors[0],
		width:    shotAnnotationWidths[0],
		bar:      shotToolbarLayout(sel, bounds, shotToolRect),
	}

	for _, lang := range []string{"zh-Hans", "en"} {
		t.Run(lang, func(t *testing.T) {
			l := locales.In(lang)
			view := ui.NewTester(func(c *ui.Context) {
				s.buildBar(c, d)
			}, bounds.Width, bounds.Height)
			view.SetLocale(lang)
			view.Frame()
			view.Frame()

			want := []string{
				l.ShotToolRect(), l.ShotToolArrow(), l.ShotToolPen(),
				l.ShotToolMosaic(), l.ShotToolText(), l.ShotToolNumber(),
				l.ShotActionUndo(), l.ShotActionRedo(), l.ShotActionOCR(),
				l.ShotActionPin(), l.ShotActionSave(), l.ShotActionCancel(),
				l.ShotCopyButton(),
				l.ShotColorRed(), l.ShotColorYellow(), l.ShotColorGreen(),
				l.ShotColorBlue(), l.ShotColorWhite(),
				l.ShotWidthThin(), l.ShotWidthMedium(), l.ShotWidthThick(),
			}
			for _, name := range want {
				if _, ok := view.AccessByRole(ui.RoleButton, name); !ok {
					t.Errorf("locale %s: missing screenshot toolbar button with accessible name %q", lang, name)
				}
			}
		})
	}
}

func TestApprovalCardButtonAccessibleNames(t *testing.T) {
	makeApproval := func(cmd string, state approvalState) *approval {
		ask := agentnotify.Ask{
			V: 2, Kind: "ask", ID: "00000000000000000000000000000001",
			Tool: agentnotify.ToolClaude, Event: "PermissionRequest",
			Session: "s", Cwd: "/tmp/demo", ToolName: "Bash",
			Input: json.RawMessage(`{"command":` + `"` + cmd + `"` + `}`),
		}
		an, ok := agentnotify.Analyze(ask.Tool, ask.ToolName, ask.Input)
		if !ok {
			t.Fatal("Analyze failed")
		}
		now := time.Now()
		return &approval{
			id: ask.ID, ask: ask, analysis: an,
			done: make(chan agentnotify.Decision, 1),
			at:   now, deadline: now.Add(approvalWait), state: state,
		}
	}

	for _, lang := range []string{"zh-Hans", "en"} {
		t.Run(lang, func(t *testing.T) {
			l := locales.In(lang)
			a := &shelf{locale: lang}

			// Safe command: Deny, Handle in Terminal, Allow.
			safe := makeApproval("ls -la", approvalShown)
			view := ui.NewTester(func(c *ui.Context) {
				_, h := a.approvalCardSize(safe)
				a.approvalCard(c, approvalCardW, h, 1, safe)
			}, 420, 300)
			view.SetLocale(lang)
			view.Frame()
			view.Frame()
			for _, name := range []string{l.LiveApprovalDeny(), l.LiveApprovalTerminal(), l.LiveApprovalAllow()} {
				if _, ok := view.AccessByRole(ui.RoleButton, name); !ok {
					t.Errorf("locale %s: missing safe approval button %q", lang, name)
				}
			}

			// Risky command: Hold to Allow.
			risky := makeApproval("rm -rf build", approvalShown)
			riskyView := ui.NewTester(func(c *ui.Context) {
				_, h := a.approvalCardSize(risky)
				a.approvalCard(c, approvalCardW, h, 1, risky)
			}, 420, 300)
			riskyView.SetLocale(lang)
			riskyView.Frame()
			riskyView.Frame()
			if _, ok := riskyView.AccessByRole(ui.RoleButton, l.LiveApprovalHold()); !ok {
				t.Errorf("locale %s: missing risky approval button %q", lang, l.LiveApprovalHold())
			}

			// Denying state: Cancel and Deny.
			denying := makeApproval("ls -la", approvalDenying)
			denyView := ui.NewTester(func(c *ui.Context) {
				_, h := a.approvalCardSize(denying)
				a.approvalCard(c, approvalCardW, h, 1, denying)
			}, 420, 300)
			denyView.SetLocale(lang)
			denyView.Frame()
			denyView.Frame()
			for _, name := range []string{l.LiveApprovalCancel(), l.LiveApprovalDeny()} {
				if _, ok := denyView.AccessByRole(ui.RoleButton, name); !ok {
					t.Errorf("locale %s: missing denying button %q", lang, name)
				}
			}
		})
	}
}
