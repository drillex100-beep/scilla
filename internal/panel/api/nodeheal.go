package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"scilla/internal/panel/nodeheal"
	"scilla/internal/panel/settings"
)

// nodeHealView is what GET /api/v1/node-heal answers.
type nodeHealView struct {
	Body struct {
		Policy nodeheal.Policy     `json:"policy"`
		Nodes  []nodeheal.NodeView `json:"nodes"`
	}
}

type nodeHealResetOutput struct {
	Body struct {
		Reset bool `json:"reset"`
	}
}

func (h *handlers) registerNodeHeal() {
	huma.Register(h.api, huma.Operation{
		OperationID: "get-node-heal", Metadata: sessionOnly, Extensions: sessionOnlyExt,
		Method: http.MethodGet, Path: "/api/v1/node-heal",
		Summary: "Авто-лечение нод: политика и состояние", Tags: []string{"node"}, DefaultStatus: http.StatusOK,
	}, h.getNodeHeal)
	huma.Register(h.api, huma.Operation{
		OperationID: "put-node-heal", Metadata: sessionOnly, Extensions: sessionOnlyExt,
		Method: http.MethodPut, Path: "/api/v1/node-heal",
		Summary: "Авто-лечение нод: сохранить политику", Tags: []string{"node"}, DefaultStatus: http.StatusOK,
	}, h.putNodeHeal)
	huma.Register(h.api, huma.Operation{
		OperationID: "reset-node-heal", Metadata: sessionOnly, Extensions: sessionOnlyExt,
		Method: http.MethodPost, Path: "/api/v1/nodes/{id}/heal/reset",
		Summary: "Авто-лечение нод: сбросить разомкнутую цепь", Tags: []string{"node"}, DefaultStatus: http.StatusOK,
	}, h.resetNodeHeal)
}

func (h *handlers) healEngine() *nodeheal.Engine {
	if h.d.Heal == nil {
		return nil
	}
	return h.d.Heal
}

func (h *handlers) getNodeHeal(ctx context.Context, in *struct{}) (*nodeHealView, error) {
	out := &nodeHealView{}
	if h.healEngine() == nil {
		out.Body.Policy = nodeheal.DefaultPolicy()
		out.Body.Nodes = []nodeheal.NodeView{}
		return out, nil
	}
	out.Body.Policy = h.d.Heal.Policy(ctx)
	nodes, err := h.d.Heal.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out.Body.Nodes = nodes
	return out, nil
}

func (h *handlers) putNodeHeal(ctx context.Context, in *struct {
	Body nodeheal.Policy
}) (*nodeHealView, error) {
	if h.healEngine() == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	pol := in.Body.Normalize()
	if err := settings.Set(ctx, h.d.Settings, nodeheal.KeyPolicy, pol); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "node_heal.policy", "settings", nodeheal.KeyPolicy, pol)
	return h.getNodeHeal(ctx, nil)
}

func (h *handlers) resetNodeHeal(ctx context.Context, in *nodeIDInput) (*nodeHealResetOutput, error) {
	if h.healEngine() == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	if _, err := h.getNode(ctx, in.ID); err != nil {
		return nil, err
	}
	reset, err := h.d.Heal.Reset(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if reset {
		h.audit(ctx, sessionOf(ctx).AdminID, "node_heal.reset", "node", strconv.FormatInt(in.ID, 10), nil)
	}
	out := &nodeHealResetOutput{}
	out.Body.Reset = reset
	return out, nil
}
