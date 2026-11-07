package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"scilla/internal/panel/store/db"
)

// The dashboard builder keeps each admin's layout of the overview page as one JSON
// document in the settings table, under a key of their own: "dashboard_layout:<admin id>".
// The page falls back to its built-in layout while the document is absent, so a fresh
// panel and a panel from before the builder look the same.

const (
	dashboardLayoutVersion   = 1
	dashboardMaxWidgets      = 50
	dashboardMaxBytes        = 32 << 10
	dashboardGlobalLayoutKey = "dashboard_layout:global"
)

// dashboardWidgetTypes are the widget kinds the builder knows; the page registers the
// same list, and an unknown kind is refused here so a broken client cannot store junk.
var dashboardWidgetTypes = map[string]bool{
	"stat":       true, // overview counters (online, active, traffic today, expiring)
	"traffic":    true, // server traffic chart
	"shares":     true, // per-node traffic shares
	"nodes":      true, // node statuses
	"payments":   true, // recent payments
	"top":        true, // top users by traffic
	"audit":      true, // recent audit events
	"inbounds":   true, // protocol statuses
	"attention":  true, // users to attend to
	"server":     true, // node system card
	"nodehealth": true, // single-node health detail
}

type dashboardWidget struct {
	ID     string          `json:"id" minLength:"1" maxLength:"64"`
	Type   string          `json:"type"`
	X      int             `json:"x" minimum:"0" maximum:"11"`
	Y      int             `json:"y" minimum:"0" maximum:"200"`
	W      int             `json:"w" minimum:"3" maximum:"12"`
	H      int             `json:"h" minimum:"2" maximum:"16"`
	Config json.RawMessage `json:"config,omitempty"`
}

type dashboardLayout struct {
	Version int               `json:"version"`
	Widgets []dashboardWidget `json:"widgets"`
}

type dashboardLayoutOutput struct {
	Body dashboardLayout
}

// dashboardLayoutWrite is what the page sends: the layout plus an optional flag
// asking to store it as the panel-wide default instead of the admin's own.
type dashboardLayoutWrite struct {
	Version int               `json:"version"`
	Widgets []dashboardWidget `json:"widgets"`
	Global  bool              `json:"global,omitempty"`
}

type dashboardLayoutInput struct {
	Body dashboardLayoutWrite
}

func (h *handlers) registerDashboard() {
	huma.Register(h.api, huma.Operation{OperationID: "dashboard-layout-get", Method: http.MethodGet, Path: "/api/v1/dashboard/layout", Summary: "Макет дашборда", Tags: []string{"stats"}}, h.getDashboardLayout)
	huma.Register(h.api, huma.Operation{OperationID: "dashboard-layout-put", Method: http.MethodPut, Path: "/api/v1/dashboard/layout", Summary: "Сохранить макет дашборда", Tags: []string{"stats"}}, h.putDashboardLayout)
}

func dashboardLayoutKey(adminID int64) string {
	return fmt.Sprintf("dashboard_layout:%d", adminID)
}

func (h *handlers) getDashboardLayout(ctx context.Context, _ *struct{}) (*dashboardLayoutOutput, error) {
	out := &dashboardLayoutOutput{}
	out.Body.Version = dashboardLayoutVersion
	out.Body.Widgets = []dashboardWidget{}
	layout, ok, err := h.loadDashboardLayout(ctx, dashboardLayoutKey(sessionOf(ctx).AdminID))
	if err != nil {
		return nil, err
	}
	if !ok {
		// No layout of their own: the panel-wide default, when one is set.
		layout, ok, err = h.loadDashboardLayout(ctx, dashboardGlobalLayoutKey)
		if err != nil {
			return nil, err
		}
	}
	if ok {
		out.Body = layout
	}
	return out, nil
}

// loadDashboardLayout reads one stored layout; ok is false when the key is absent,
// the document is not JSON or its version is not this panel's.
func (h *handlers) loadDashboardLayout(ctx context.Context, key string) (layout dashboardLayout, ok bool, err error) {
	raw, err := h.d.Store.Q.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dashboardLayout{}, false, nil
		}
		return dashboardLayout{}, false, err
	}
	var saved dashboardLayout
	if err := json.Unmarshal([]byte(raw), &saved); err != nil || saved.Version != dashboardLayoutVersion {
		// A document this panel no longer understands is treated as absent, not as an error:
		// the page then shows the built-in layout and the next save overwrites it.
		return dashboardLayout{}, false, nil
	}
	saved.Widgets = h.dashWidgetsValid(saved.Widgets)
	return saved, true, nil
}

func (h *handlers) putDashboardLayout(ctx context.Context, in *dashboardLayoutInput) (*dashboardLayoutOutput, error) {
	write := in.Body
	if write.Version != dashboardLayoutVersion {
		return nil, huma.NewError(http.StatusBadRequest, "unsupported layout version")
	}
	layout := dashboardLayout{Version: write.Version, Widgets: write.Widgets}
	if len(layout.Widgets) > dashboardMaxWidgets {
		return nil, huma.NewError(http.StatusBadRequest, fmt.Sprintf("too many widgets: %d (max %d)", len(layout.Widgets), dashboardMaxWidgets))
	}
	raw, err := json.Marshal(layout)
	if err != nil {
		return nil, err
	}
	if len(raw) > dashboardMaxBytes {
		return nil, huma.NewError(http.StatusBadRequest, fmt.Sprintf("layout too large: %d bytes (max %d)", len(raw), dashboardMaxBytes))
	}
	if err := h.validateDashboardLayout(ctx, layout); err != nil {
		return nil, err
	}
	key := dashboardLayoutKey(sessionOf(ctx).AdminID)
	action := "dashboard.layout"
	if write.Global {
		key = dashboardGlobalLayoutKey
		action = "dashboard.layout.global"
	}
	if err := h.d.Store.Q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(raw)}); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, action, "settings", key, map[string]any{"widgets": len(layout.Widgets)})
	out := &dashboardLayoutOutput{}
	out.Body = layout
	return out, nil
}

// validateDashboardLayout refuses documents the page cannot draw: unknown widget kinds,
// overlapping ids, coordinates outside the grid, configs that are not JSON objects.
func (h *handlers) validateDashboardLayout(ctx context.Context, layout dashboardLayout) error {
	seen := map[string]bool{}
	for i, w := range layout.Widgets {
		if !dashboardWidgetTypes[w.Type] {
			return huma.NewError(http.StatusBadRequest, fmt.Sprintf("widget %d: unknown type %q", i, w.Type))
		}
		if seen[w.ID] {
			return huma.NewError(http.StatusBadRequest, fmt.Sprintf("widget %d: duplicate id %q", i, w.ID))
		}
		seen[w.ID] = true
		if w.X+w.W > 12 || w.W < 3 || w.W > 12 || w.H < 2 || w.H > 16 {
			return huma.NewError(http.StatusBadRequest, fmt.Sprintf("widget %q does not fit the grid", w.ID))
		}
		if len(w.Config) > 0 {
			var cfg map[string]any
			if err := json.Unmarshal(w.Config, &cfg); err != nil || cfg == nil {
				return huma.NewError(http.StatusBadRequest, fmt.Sprintf("widget %q: config must be a JSON object", w.ID))
			}
		}
	}
	return nil
}

// dashWidgetsValid drops the widgets a newer page wrote that this panel does not know,
// so an older panel reading a layout saved by a newer one still opens.
func (h *handlers) dashWidgetsValid(widgets []dashboardWidget) []dashboardWidget {
	out := make([]dashboardWidget, 0, len(widgets))
	for _, w := range widgets {
		if dashboardWidgetTypes[w.Type] {
			out = append(out, w)
		}
	}
	return out
}
