package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func dashWidget(id, typ string, x, y, w, h int, config string) dashboardWidget {
	var raw json.RawMessage
	if config != "" {
		raw = json.RawMessage(config)
	}
	return dashboardWidget{ID: id, Type: typ, X: x, Y: y, W: w, H: h, Config: raw}
}

func TestDashboardLayoutValidation(t *testing.T) {
	h := &handlers{}
	first := dashWidget("a", "stat", 0, 0, 3, 2, `{"metric":"online"}`)
	valid := dashboardLayout{Version: dashboardLayoutVersion, Widgets: []dashboardWidget{first, dashWidget("b", "traffic", 3, 0, 9, 5, "")}}
	if err := h.validateDashboardLayout(t.Context(), valid); err != nil {
		t.Fatalf("valid layout refused: %v", err)
	}
	cases := map[string][]dashboardWidget{
		"unknown type":      {first, dashWidget("b", "piechart", 0, 4, 3, 2, "")},
		"duplicate id":      {first, dashWidget("a", "stat", 3, 4, 3, 2, "")},
		"outside grid":      {first, dashWidget("b", "stat", 10, 4, 3, 2, "")},
		"too narrow":        {first, dashWidget("b", "stat", 3, 4, 2, 2, "")},
		"config not object": {first, dashWidget("b", "stat", 3, 4, 3, 2, `[1,2]`)},
	}
	for name, ws := range cases {
		t.Run(name, func(t *testing.T) {
			err := h.validateDashboardLayout(t.Context(), dashboardLayout{Version: dashboardLayoutVersion, Widgets: ws})
			var se huma.StatusError
			if !errors.As(err, &se) || se.GetStatus() != http.StatusBadRequest {
				t.Fatalf("%s: want a 400 error, got %v", name, err)
			}
		})
	}
}
