package main

import (
	"fmt"
)

func (r *runState) newPage(address string, width, height int) error {
	var err error
	r.page, err = r.browser.NewPage(r.ctx, "about:blank")
	if err != nil {
		return err
	}
	if err := r.page.SetViewport(r.ctx, width, height); err != nil {
		return err
	}
	return r.page.Navigate(r.ctx, address)
}

// Background CDP targets can defer native resize notification. Dispatch only
// the standard DOM signals after device metrics change; do not focus a window
// or call application layout functions directly.
func (r *runState) viewport(width, height int) error {
	if err := r.page.SetViewport(r.ctx, width, height); err != nil {
		return err
	}
	return r.page.Eval(r.ctx, "window.dispatchEvent(new Event('resize')); if (window.visualViewport) window.visualViewport.dispatchEvent(new Event('resize')); void 0", nil)
}

func (r *runState) fixtureCheck() error {
	f, err := newFixture(r.opts.Root, r.opts.Case, "")
	if err != nil {
		return err
	}
	defer f.close()
	width, height := 1250, 900
	if r.opts.Case == "delegation-ui" {
		height = 1000
	}
	if r.opts.Case == "session-activity" {
		width, height = 1280, 850
	}
	if err := r.newPage(f.server.URL+"/", width, height); err != nil {
		return err
	}
	switch r.opts.Case {
	case "delegation-ui":
		if err := r.phase("delegation", "initial", nil); err != nil {
			return err
		}
		if err := r.screenshot("delegation-desktop"); err != nil {
			return err
		}
		if err := r.viewport(700, 850); err != nil {
			return err
		}
		if err := r.phase("delegation", "narrow", nil); err != nil {
			return err
		}
		if err := r.screenshot("delegation-narrow"); err != nil {
			return err
		}
		if err := r.viewport(1250, 1000); err != nil {
			return err
		}
		if err := r.phase("delegation", "active", nil); err != nil {
			return err
		}
		if err := r.screenshot("delegation-active"); err != nil {
			return err
		}
		if err := r.phase("delegation", "cleanup", nil); err != nil {
			return err
		}
	case "reasoning-ui":
		if err := r.phase("reasoning", "initial", nil); err != nil {
			return err
		}
		if err := r.screenshot("toggle-desktop"); err != nil {
			return err
		}
		if err := r.viewport(520, 900); err != nil {
			return err
		}
		if err := r.phase("reasoning", "narrow", nil); err != nil {
			return err
		}
		if err := r.screenshot("toggle-narrow"); err != nil {
			return err
		}
		if err := r.viewport(1250, 900); err != nil {
			return err
		}
		if err := r.phase("reasoning", "keyboard", nil); err != nil {
			return err
		}
		stale := f.hold("models", false)
		defer stale.release()
		if err := r.phase("reasoning", "startStale", nil); err != nil {
			return err
		}
		if err := stale.wait(r.ctx); err != nil {
			return err
		}
		if err := r.phase("reasoning", "chooseHigh", nil); err != nil {
			return err
		}
		stale.release()
		if err := r.phase("reasoning", "finishStale", nil); err != nil {
			return err
		}
		pending := f.hold("reasoning", false)
		defer pending.release()
		if err := r.phase("reasoning", "startPending", nil); err != nil {
			return err
		}
		if err := pending.wait(r.ctx); err != nil {
			return err
		}
		if err := r.phase("reasoning", "resumeB", nil); err != nil {
			return err
		}
		pending.release()
		if err := r.phase("reasoning", "finish", nil); err != nil {
			return err
		}
		if err := r.screenshot("graded-desktop"); err != nil {
			return err
		}
		r.report.Limitations = append(r.report.Limitations, "Keyboard handlers use native DOM KeyboardEvent; Enter activation uses button.click because the shared driver has no trusted Input.dispatchKeyEvent API.")
	case "session-activity":
		if err := r.phase("session", "initial", nil); err != nil {
			return err
		}
		if err := r.phase("session", "send", nil); err != nil {
			return err
		}
		if err := f.chat.wait(r.ctx); err != nil {
			return err
		}
		if err := f.activity.wait(r.ctx); err != nil {
			return err
		}
		if err := r.phase("session", "promoted", nil); err != nil {
			return err
		}
		if err := r.screenshot("promoted-while-streaming"); err != nil {
			return err
		}
		f.chat.release()
		if err := r.phase("session", "complete", nil); err != nil {
			return err
		}
		if err := r.collect(); err != nil {
			return err
		}
		if err := r.page.Navigate(r.ctx, f.server.URL+"/"); err != nil {
			return err
		}
		if err := r.phase("session", "reloaded", nil); err != nil {
			return err
		}
		for _, fail := range []bool{false, true} {
			f.setProject("A")
			stale := f.hold("list", fail)
			defer stale.release()
			if err := r.phase("session", "startStale", nil); err != nil {
				return err
			}
			if err := stale.wait(r.ctx); err != nil {
				return err
			}
			f.setProject("B")
			if err := r.phase("session", "newProject", nil); err != nil {
				return err
			}
			stale.release()
			if err := r.phase("session", "finishStale", fail); err != nil {
				return err
			}
		}
		if err := r.phase("session", "finish", nil); err != nil {
			return err
		}
	}
	return r.collect()
}

func paletteModels() any {
	models := []map[string]any{}
	for i := 1; i <= 34; i++ {
		models = append(models, map[string]any{"id": fmt.Sprintf("catalog-model-%02d", i), "provider": "any-router", "active": i == 1, "context_length": 100000})
	}
	models = append(models, map[string]any{"id": "gpt-5-codex", "provider": "any-router", "context_length": 400000}, map[string]any{"id": "gpt-5.6-sol", "provider": "any-router", "context_length": 1000000, "reasoning": true})
	return map[string]any{"active": "catalog-model-01", "provider": "any-router", "models": models}
}
func (r *runState) paletteCheck() error {
	server, err := externalSurface(r.opts.URL, paletteModels())
	if err != nil {
		return err
	}
	defer server.Close()
	defer server.CloseClientConnections()
	if err := r.newPage(server.URL+"/", 790, 520); err != nil {
		return err
	}
	if err := r.phase("palette", "run", nil); err != nil {
		return err
	}
	if err := r.screenshot("model-palette-small-window"); err != nil {
		return err
	}
	return r.collect()
}

func (r *runState) chatCheck() error {
	f, err := newFixture(r.opts.Root, "chat", r.opts.URL)
	if err != nil {
		return err
	}
	defer f.close()
	if err := r.newPage(f.server.URL+"/", 1280, 900); err != nil {
		return err
	}
	if err := r.phase("chat", "run", map[string]string{"kind": r.opts.Case, "mode": r.opts.Mode, "callID": r.opts.CallID}); err != nil {
		return err
	}
	return r.collect()
}

func (r *runState) scalingCheck() error {
	specs := []map[string]any{{"name": "SuperCli", "url": r.opts.URL, "key": "ui.uiScale", "attribute": "data-ui-scale", "composer": "#composer", "send": "#send-btn"}}
	if r.opts.NestCafeURL != "" {
		specs = append(specs, map[string]any{"name": "NestCafe", "url": r.opts.NestCafeURL, "key": "ui.scale", "attribute": "data-scale", "composer": "#composer", "send": "#send-button", "settingsDialog": "#settings-page-dialog", "modelDialog": "#model-dialog", "centeredEmptyState": true})
	} else {
		r.report.Limitations = append(r.report.Limitations, "Optional NestCafe scenarios were not requested (-nestcafe-url absent).")
	}
	for _, spec := range specs {
		var models any
		if spec["modelDialog"] != nil {
			rows := []map[string]any{}
			for i := 1; i <= 80; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("test-model-%02d", i), "provider": "test-provider", "tool_use": true})
			}
			models = map[string]any{"active": "test-model-01", "provider": "test-provider", "models": rows}
		}
		server, err := externalSurface(spec["url"].(string), models)
		if err != nil {
			return err
		}
		err = func() error {
			defer server.Close()
			defer server.CloseClientConnections()
			for _, viewport := range [][2]int{{1280, 720}, {900, 620}} {
				if err := r.newPage(server.URL+"/", viewport[0], viewport[1]); err != nil {
					return err
				}
				for _, scale := range []string{"compact", "normal", "large", "xlarge", "huge", "auto"} {
					args := map[string]any{"spec": spec, "scale": scale}
					if err := r.phase("scaling", "save", args); err != nil {
						return err
					}
					if err := r.page.Navigate(r.ctx, server.URL+"/"); err != nil {
						return err
					}
					if err := r.phase("scaling", "run", args); err != nil {
						return err
					}
					if err := r.collect(); err != nil {
						return err
					}
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
