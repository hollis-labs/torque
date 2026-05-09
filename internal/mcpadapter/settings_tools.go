package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSettingsTools() {
	a.addTool(mcp.NewTool("clockwork_settings_get",
		mcp.WithDescription(`Read one settings value by key (e.g. features.sprints, scheduler.enabled).
Use for runtime config introspection; clockwork_settings_save writes. clockwork_health reports enabled_features shortcut.
Response shape: data = {key, value}.
Example: {"key":"features.sprints"}`),
		mcp.WithString("key", mcp.Required(), mcp.Description("Settings key (dotted path)")),
	), a.handleSettingsGet)

	a.addTool(mcp.NewTool("clockwork_settings_save",
		mcp.WithDescription(`Write one settings key/value; persisted across restarts. Feature flags may require a restart to register new tools.
Use for runtime config mutation. clockwork_settings_get reads.
Response shape: data = {key, value}.
Example: {"key":"features.epics","value":"true"}`),
		mcp.WithString("key", mcp.Required(), mcp.Description("Settings key (dotted path)")),
		mcp.WithString("value", mcp.Required(), mcp.Description("Settings value (string)")),
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
