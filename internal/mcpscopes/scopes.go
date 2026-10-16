// Package mcpscopes maps every Torque MCP tool to the scope a bearer must
// hold to call it (libs/security/auth). Deny by default: a tool absent from
// Tools is refused under enforce, and a test fails until it is mapped.
package mcpscopes

import "github.com/hollis-labs/libs/security/auth"

// Scopes a Torque bearer can hold. Admin implies nothing else: a token that
// needs reads and admin carries both (or torque.*).
const (
	Read  = "torque.read"
	Write = "torque.write"
	Admin = "torque.admin"
)

const (
	read  = Read
	write = Write
	admin = Admin
)

// Tools is the hand-reviewed tool -> scope table. read: no state change.
// write: changes tasks, plans, comments and other records. admin: changes
// settings or the scheduler, hard-deletes in bulk, or starts/stops/resumes
// agent sessions and plans (which run agents).
var Tools = map[string]string{
	"torque_aar_submit":                write,
	"torque_artifact_create":           write,
	"torque_artifact_delete":           write,
	"torque_artifact_get":              read,
	"torque_artifact_list":             read,
	"torque_artifact_update":           write,
	"torque_broker_inbox":              read,
	"torque_broker_request":            write,
	"torque_broker_send":               write,
	"torque_collection_archive":        write,
	"torque_collection_create":         write,
	"torque_collection_get":            read,
	"torque_collection_inbox_add":      write,
	"torque_collection_inbox_list":     read,
	"torque_collection_list":           read,
	"torque_collection_task_add":       write,
	"torque_collection_task_move":      write,
	"torque_collection_task_remove":    write,
	"torque_collection_task_reorder":   write,
	"torque_collection_tasks_list":     read,
	"torque_collection_unarchive":      write,
	"torque_collection_update":         write,
	"torque_comment_add":               write,
	"torque_comment_bulk_add":          write,
	"torque_comment_delete":            write,
	"torque_comment_list":              read,
	"torque_comment_search":            read,
	"torque_comment_update":            write,
	"torque_epic_archive":              write,
	"torque_epic_bulk_update":          write,
	"torque_epic_create":               write,
	"torque_epic_delete":               write,
	"torque_epic_facets":               read,
	"torque_epic_get":                  read,
	"torque_epic_list":                 read,
	"torque_epic_unarchive":            write,
	"torque_epic_update":               write,
	"torque_inbox_poll":                write,
	"torque_health":                    read,
	"torque_issue_bulk_transition":     write,
	"torque_issue_bulk_update":         write,
	"torque_issue_create":              write,
	"torque_issue_delete":              write,
	"torque_issue_get":                 read,
	"torque_issue_list":                read,
	"torque_issue_update":              write,
	"torque_models_get":                read,
	"torque_models_list":               read,
	"torque_plan_add_phase":            write,
	"torque_plan_create":               write,
	"torque_plan_delete":               write,
	"torque_plan_get":                  read,
	"torque_plan_list":                 read,
	"torque_plan_list_children":        read,
	"torque_plan_remove_phase":         write,
	"torque_plan_start":                admin,
	"torque_plan_update":               write,
	"torque_planner_board":             read,
	"torque_planner_comment":           write,
	"torque_planner_create":            write,
	"torque_planner_databases":         read,
	"torque_planner_decide":            write,
	"torque_planner_defer":             write,
	"torque_planner_get":               read,
	"torque_planner_inbox_add":         write,
	"torque_planner_inbox_dismiss":     write,
	"torque_planner_inbox_promote":     write,
	"torque_planner_link_add":          write,
	"torque_planner_link_remove":       write,
	"torque_planner_list":              read,
	"torque_planner_relate":            write,
	"torque_planner_reopen":            write,
	"torque_planner_reorder":           write,
	"torque_planner_schema":            read,
	"torque_planner_search":            read,
	"torque_planner_update":            write,
	"torque_project_archive":           write,
	"torque_project_create":            write,
	"torque_project_delete":            write,
	"torque_project_facets":            read,
	"torque_project_get":               read,
	"torque_project_list":              read,
	"torque_project_unarchive":         write,
	"torque_project_update":            write,
	"torque_run_facets":                read,
	"torque_run_get":                   read,
	"torque_run_list":                  read,
	"torque_run_timeseries":            read,
	"torque_scheduler_status":          read,
	"torque_scheduler_toggle":          admin,
	"torque_session_attach":            write,
	"torque_session_checkpoint":        write,
	"torque_session_checkpoint_list":   read,
	"torque_session_create":            write,
	"torque_session_get":               read,
	"torque_session_launch":            admin,
	"torque_session_list":              read,
	"torque_session_resume":            admin,
	"torque_session_stop":              admin,
	"torque_settings_get":              read,
	"torque_settings_save":             admin,
	"torque_sprint_approve":            write,
	"torque_sprint_archive":            write,
	"torque_sprint_bulk_update":        write,
	"torque_sprint_create":             write,
	"torque_sprint_delete":             write,
	"torque_sprint_facets":             read,
	"torque_sprint_get":                read,
	"torque_sprint_list":               read,
	"torque_sprint_start":              admin,
	"torque_sprint_unarchive":          write,
	"torque_sprint_update":             write,
	"torque_steering_dismiss":          write,
	"torque_tag_create":                write,
	"torque_tag_delete":                write,
	"torque_tag_get":                   read,
	"torque_tag_list":                  read,
	"torque_tag_merge":                 write,
	"torque_tag_update":                write,
	"torque_task_blocked":              write,
	"torque_task_bulk_delete":          admin,
	"torque_task_bulk_tag":             write,
	"torque_task_bulk_transition":      write,
	"torque_task_bulk_update":          write,
	"torque_task_checkpoint_cancel":    write,
	"torque_task_checkpoint_emit":      write,
	"torque_task_checkpoint_get":       read,
	"torque_task_checkpoint_list":      read,
	"torque_task_checkpoint_respond":   write,
	"torque_task_checkpoints_pending":  read,
	"torque_task_create":               write,
	"torque_task_create_from_template": write,
	"torque_task_delete":               write,
	"torque_task_facets":               read,
	"torque_task_get":                  read,
	"torque_task_list":                 read,
	"torque_task_review":               write,
	"torque_task_subtodo_add":          write,
	"torque_task_subtodo_bulk_add":     write,
	"torque_task_subtodo_delete":       write,
	"torque_task_subtodo_done":         write,
	"torque_task_subtodo_list":         read,
	"torque_task_subtodo_update":       write,
	"torque_task_summary":              write,
	"torque_task_transition":           write,
	"torque_task_update":               write,
	"torque_template_archive":          write,
	"torque_template_create":           write,
	"torque_template_delete":           write,
	"torque_template_get":              read,
	"torque_template_list":             read,
	"torque_template_update":           write,
	"torque_workstream_claim_next":     write,
	"torque_workstream_heartbeat":      write,
	"torque_workstream_release":        write,
}

// AddTo registers the /mcp transport route and every tool in Tools on t.
// The transport route needs authentication only; the per-tool scope is
// checked by Guard.AuthorizeTool at tools/call.
func AddTo(t *auth.ScopeTable) *auth.ScopeTable {
	t.Route("*", "/mcp", "")
	for name, scope := range Tools {
		t.Tool(name, scope)
	}
	return t
}
