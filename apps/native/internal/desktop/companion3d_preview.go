//go:build companion3d

package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/native"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/desktop/presentation/companion3d"
	"liberty.local/liberty/internal/infrastructure/companion3d/metalbridge"
)

// Profile-free native integration: the existing MyGo transparent-window and
// input path, with a single GPU scene shared by the review and floating window.
// This does not change the production pet or open the user's database.
type companion3DPreview struct {
	locale                                                   string
	renderer                                                 metalbridge.Renderer
	texture                                                  *native.Texture
	model                                                    *companion3d.PixelModel
	library                                                  *companion3d.PixelLibrary
	controller                                               *client.CompanionPreview[*companion3d.PixelModel]
	err                                                      error
	window, floating                                         *mygo.Window
	primaryVisible, floatingVisible, dirty, lighting, closed bool
	packet                                                   []byte
	pose                                                     int
	yaw, pitch                                               float64
}

func RunCompanion3DPreview(locale, assetDirectory string) error {
	if !filepath.IsAbs(assetDirectory) {
		return companion3d.ErrModel
	}
	library, err := companion3d.OpenPixelLibrary(os.DirFS(assetDirectory))
	if err != nil {
		return err
	}
	r, err := metalbridge.NewRenderer()
	if err != nil {
		return err
	}
	defer r.Close()
	v := &companion3DPreview{locale: locale, renderer: r, texture: native.NewTexture(), library: library, primaryVisible: true, pose: -1}
	changed := func() {
		if v.closed {
			return
		}
		if v.window != nil {
			v.window.Invalidate()
		}
		if v.floating != nil {
			v.floating.Invalidate()
		}
	}
	v.controller = client.NewCompanionPreview(func(ctx context.Context, name string) (*companion3d.PixelModel, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		model, err := library.Load(name)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			err = uploadCompanionPixel(r, v.texture, model, 512)
			if err == nil {
				model.ReleaseGeometry()
			}
		}
		return model, err
	}, mygo.RunOnMain, func(model *companion3d.PixelModel, err error) {
		v.err = err
		if err != nil {
			return
		}
		v.model = model
		v.pose = -1
		v.dirty = true
	}, changed)
	defer v.controller.Close()
	mygo.App.WhenReady(func() {
		v.window = mygo.NewWindow(mygo.WindowOptions{Title: v.text("Libby · 原生像素动作", "Libby · Native pixel actions"), Width: 940, Height: 760, MinWidth: 740, MinHeight: 650, Content: ui.View(v.view)})
		v.window.OnHide(func() { v.primaryVisible = false; v.visibility() })
		v.window.OnMinimize(func() { v.primaryVisible = false; v.visibility() })
		wake := func() { v.primaryVisible = true; v.visibility() }
		v.window.OnShow(wake)
		v.window.OnRestore(wake)
		v.window.OnClosed(func() {
			v.closed = true
			v.controller.Close()
			if v.floating != nil {
				v.floating.Destroy()
				v.floating = nil
			}
			_ = r.Unload()
			_ = r.Close()
		})
		mygo.App.SetMenu(ApplicationMenu(locale))
		v.controller.Select("crush")
	})
	return mygo.App.Run()
}
func (v *companion3DPreview) text(zh, en string) string { return localized(v.locale, zh, en) }
func (v *companion3DPreview) visibility() {
	v.controller.SetActive(v.primaryVisible || v.floatingVisible, time.Now())
	v.dirty = true
}
func (v *companion3DPreview) frame(c *ui.Context) {
	pose, next := v.controller.Tick(c.Now())
	if v.controller.Active && next > 0 && !c.Preferences().ReduceMotion && v.err == nil {
		c.After(next)
	}
	if !v.controller.Active || v.controller.Loading || v.err != nil || v.model == nil {
		return
	}
	if c.Preferences().ReduceMotion {
		pose = 0
	}
	if !v.dirty && pose == v.pose {
		return
	}
	v.pose = pose
	v.dirty = false
	seconds := float64(pose)*.3 + 1e-6
	v.packet = v.model.Pose(v.packet, seconds, v.yaw, v.pitch)
	v.err = v.renderer.DrawPixel(v.packet, pose, v.lighting)
}
func (v *companion3DPreview) view(c *ui.Context) {
	v.frame(c)
	ui.Column(c).Fill().Padding(20).Gap(14).Children(func() {
		ui.Text(c, v.text("像素动作 · 原生 GPU", "Pixel actions · Native GPU")).FontSize(22).Bold().Role(ui.RoleHeading).Level(1)
		ui.Text(c, v.text("15 组动作，骨骼、表情与道具同步播放。", "15 actions with synchronized skeletons, expressions and props.")).TextColor(c.Theme().TextMuted)
		options := v.library.Actions()
		labels := make([]string, len(options))
		selected := ""
		for i, a := range options {
			labels[i] = v.text(a.Title[0], a.Title[1])
			if a.ID == v.controller.Selected {
				selected = labels[i]
			}
		}
		ui.Row(c).Gap(10).Children(func() {
			if ui.Select(c, &selected, labels).Width(220).Label(v.text("选择动作", "Choose action")).Changed() {
				for i, label := range labels {
					if selected == label {
						v.controller.Select(options[i].ID)
						break
					}
				}
			}
			if v.controller.Loading {
				ui.Text(c, v.text("正在加载动作…", "Loading action…"))
			}
		})
		ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(20).Children(func() {
			ui.Column(c).Width(300).Gap(8).Children(func() {
				ui.Text(c, v.text("原图参考", "Original reference")).Bold()
				if v.model != nil {
					ui.Image(c, presentation.PetActionPose(v.model.Name, max(0, v.pose))).Size(290, 360).Fit(ui.Contain).Label(v.text("当前动作原帧", "Current original pose"))
				}
			})
			ui.Image(c, v.texture).Grow(1).FillHeight().Fit(ui.Contain).Label(v.text("原生三维角色", "Native 3D character"))
		})
		if v.err != nil {
			ui.Text(c, v.text("动作加载或渲染失败，可重新选择动作重试。", "Action loading or rendering failed. Select an action to retry.")).Role(ui.RoleAlert)
		}
		ui.Row(c).Gap(8).Children(func() {
			label := v.text("播放动作", "Play")
			if v.controller.Playing {
				label = v.text("暂停动作", "Pause")
			}
			if ui.Button(c, label).Disabled(v.controller.Loading || v.model == nil || c.Preferences().ReduceMotion).Clicked() {
				v.controller.SetPlaying(!v.controller.Playing, c.Now())
			}
			if ui.Button(c, v.text("上一帧", "Previous pose")).Disabled(v.model == nil).Clicked() {
				v.controller.SetPlaying(false, c.Now())
				v.controller.Seek((max(0, v.pose)+8)%9, c.Now())
			}
			if ui.Button(c, v.text("下一帧", "Next pose")).Disabled(v.model == nil).Clicked() {
				v.controller.SetPlaying(false, c.Now())
				v.controller.Seek((max(0, v.pose)+1)%9, c.Now())
			}
			ui.Text(c, fmt.Sprintf(v.text("第 %d / 9 帧", "Pose %d / 9"), max(0, v.pose)+1))
		})
		ui.Row(c).Gap(8).Children(func() {
			for _, angle := range []struct {
				zh, en string
				yaw    float64
			}{{"正面", "Front", 0}, {"侧转 30°", "Turn 30°", .5235987756}, {"背面", "Back", 3.1415926536}} {
				if ui.Button(c, v.text(angle.zh, angle.en)).Clicked() {
					v.yaw = angle.yaw
					v.dirty = true
				}
			}
			light := v.text("开启体积光照", "Volume lighting")
			if v.lighting {
				light = v.text("原图配色", "Original colours")
			}
			if ui.Button(c, light).Clicked() {
				v.lighting = !v.lighting
				v.dirty = true
			}
			if ui.Button(c, v.text("透明桌面预览", "Transparent desktop preview")).Clicked() {
				v.openFloating()
			}
		})
		if v.model != nil {
			ui.Text(c, fmt.Sprintf(v.text("%s · %d 三角面 · %d 骨骼 · 每 300 ms 同步更新", "%s · %d triangles · %d joints · synchronized every 300 ms"), v.model.Name, v.model.Triangles, v.model.JointCount)).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.Text(c, v.text("原生接入验证；尚未替换客户端桌宠。", "Native integration preview; the client companion has not been replaced.")).FontSize(11).TextColor(c.Theme().TextMuted)
	})
	// Buttons are evaluated after frame(). A paused camera/lighting change
	// needs one more frame, then returns to having no scheduled wakeups.
	if v.dirty && !v.controller.Loading && v.err == nil {
		c.After(time.Millisecond)
	}
}
func (v *companion3DPreview) openFloating() {
	if v.floating != nil {
		v.floating.Focus()
		return
	}
	var pointer companionPointer
	var floating *mygo.Window
	floating = mygo.NewWindow(mygo.WindowOptions{Title: v.text("Libby · 原生透明预览", "Libby · Native transparent preview"), Width: 320, Height: 320, Frameless: true, Transparent: true, AlwaysOnTop: true, SkipTaskbar: true, DisableShadow: true, DisableResize: true, Content: ui.View(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		v.frame(c)
		ui.Image(c.Key("pet3d-pixel"), v.texture).Fill().Fit(ui.Contain).Label(v.text("拖动三维角色", "Drag 3D character")).HandleInput(pointer.input)
	})})
	pointer.window = floating
	floating.SetInputRegion([]mygo.Rectangle{{X: 32, Y: 20, Width: 256, Height: 286}})
	v.floating = floating
	v.floatingVisible = true
	v.visibility()
	floating.OnHide(func() { v.floatingVisible = false; v.visibility() })
	floating.OnShow(func() { v.floatingVisible = true; v.visibility() })
	floating.OnClosed(func() {
		if v.floating == floating {
			v.floating = nil
			v.floatingVisible = false
			if !v.closed {
				v.visibility()
			}
		}
	})
}
