package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

// Only playback policy lives here. Geometry and poses come from the pinned
// bloub engine through scripts/generate-native-bloub.mjs.
type liveAvatar struct {
	phase   string
	started time.Time
	player  presentation.BloubPlayer
}

type liveAvatarPalette struct{ ink, paper, accent ui.Color }

func liveAvatarColors(c *ui.Context) liveAvatarPalette {
	ink := c.Theme().Text
	if !c.Theme().Dark {
		ink = ui.Hex("#0a0a0c")
	}
	return liveAvatarPalette{ink: ink, paper: palette(c).Content, accent: c.Theme().Accent}
}

func (a *liveAvatar) observe(phase string, now time.Time) {
	if a.started.IsZero() || a.phase != phase {
		a.phase, a.started = phase, now
	}
}

func (a *liveAvatar) draw(p *ui.Painter, r ui.Rect, amplitude float32, motion bool, colors liveAvatarPalette) {
	elapsed := max(time.Duration(0), p.Now().Sub(a.started))
	clip, animated := "showcase", true
	switch a.phase {
	case "listening":
		clip = "idle"
	case "preparing":
		clip = "comet"
	case "stopping":
		clip = "thinking"
	case "paused":
		clip, animated = "paused", false
	case "failed", "interrupted":
		clip, animated = "failed", false
	}
	if motion && animated {
		// Retain upstream gaze drift with repaint-only frames, suspended when
		// hidden/inactive and when the system requests reduced motion.
		// A timed repaint also caps displays whose refresh rate the platform
		// cannot report; AnimationFrameRate would run at every refresh there.
		p.After(time.Second / 30)
	} else {
		elapsed = time.Second
	}
	gain := min(float32(1), amplitude*2.5)
	if gain > 0 {
		size := min(r.W, r.H)
		p.FillRadialGradient(r, ui.RadialGradient{Center: ui.Point{X: .5, Y: .5}, Stops: []ui.GradientStop{
			{Color: colors.accent.Alpha(.16 * gain)}, {Color: colors.accent.Alpha(0), At: 1},
		}}, size/2)
	}
	a.player.Draw(p, r, clip, elapsed, colors.ink, colors.paper, gain)
}
