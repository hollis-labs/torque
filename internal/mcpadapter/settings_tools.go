package mcpadapter

import (
	"context"
)

func (a *Adapter) registerSettingsTools() {
	a.addTool(newTool("torque_settings_get",
		withDescription(`Read one settings value by key (e.g. features.sprints, scheduler.enabled).
Use for runtime config introspection; torque_settings_save writes. torque_health reports enabled_features shortcut.
Response shape: data = {key, value}.
Example: {"key":"features.sprints"}`),
		withString("key", required(), desc("Settings key (dotted path)")),
	), a.handleSettingsGet)

	a.addTool(newTool("torque_settings_save",
		withDescription(`Write one settings key/value; persisted across restarts. Feature flags may require a restart to register new tools.
Use for runtime config mutation. torque_settings_get reads.
Response shape: data = {key, value}.
Example: {"key":"features.epics","value":"true"}`),
		withString("key", required(), desc("Settings key (dotted path)")),
		withString("value", required(), desc("Settings value (string)")),
	), a.handleSettingsSave)
}

func (a *Adapter) handleSettingsGet(ctx context.Context, req map[string]any) (any, error) {
	key := reqStr(req, "key")
	val, err := a.svc.Settings.Get(key)
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]string{"key": key, "value": val})
}

func (a *Adapter) handleSettingsSave(ctx context.Context, req map[string]any) (any, error) {
	key := reqStr(req, "key")
	value := reqStr(req, "value")
	if err := a.svc.Settings.Set(key, value); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]string{"key": key, "value": value})
}
