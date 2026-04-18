package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSettingsTools() {
	a.server.AddTool(mcp.NewTool("clockwork_settings_get",
		mcp.WithDescription("Get a settings value by key"),
		mcp.WithString("key", mcp.Required(), mcp.Description("Settings key")),
	), a.handleSettingsGet)

	a.server.AddTool(mcp.NewTool("clockwork_settings_save",
		mcp.WithDescription("Save a settings key/value pair"),
		mcp.WithString("key", mcp.Required(), mcp.Description("Settings key")),
		mcp.WithString("value", mcp.Required(), mcp.Description("Settings value")),
	), a.handleSettingsSave)
}

func (a *Adapter) handleSettingsGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key := reqStr(req, "key")
	val, err := a.svc.Settings.Get(key)
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]string{"key": key, "value": val})
}

func (a *Adapter) handleSettingsSave(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key := reqStr(req, "key")
	value := reqStr(req, "value")
	if err := a.svc.Settings.Set(key, value); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]string{"key": key, "value": value})
}
