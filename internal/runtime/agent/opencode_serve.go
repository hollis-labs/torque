package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/provider"
	gopermission "github.com/hollis-labs/substrate/harness/interception/permission"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"

	"github.com/hollis-labs/torque/internal/config"
)

// opencode serve-http (CW-20261001-0148). `opencode run` gets the profile's
// model and Torque's planted agent as --model and --agent on every turn, and
// its permission prompts never block. `opencode serve` takes neither flag,
// agentkit's prompt_async body names no model or agent, and a permission
// prompt waits for an HTTP reply. The overnight smoke's serve session ran
// opencode's default model (big-pickle) as its built-in build agent, then
// hung on an external_directory prompt for a README outside its worktree.

const opencodeConfigContentEnv = "OPENCODE_CONFIG_CONTENT"

// opencodeServeEnv gives an opencode serve-http session the profile's model
// and Torque's planted agent through OPENCODE_CONFIG_CONTENT, which opencode
// merges over its other config, so every prompt runs with them (model and
// default_agent). Keys already in an inherited OPENCODE_CONFIG_CONTENT are
// kept unless Torque sets them. Other sessions' env is returned unchanged.
func opencodeServeEnv(env []string, kind RuntimeKind, cli provider.CLIAdapter) []string {
	oc, ok := cli.(*provider.OpencodeAdapter)
	if !ok || kind != RuntimeKindServeHTTP || (oc.Model == "" && oc.Agent == "") {
		return env
	}
	content := map[string]any{}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, found := strings.CutPrefix(kv, opencodeConfigContentEnv+"="); found {
			_ = json.Unmarshal([]byte(v), &content)
			continue
		}
		out = append(out, kv)
	}
	if oc.Model != "" {
		content["model"] = oc.Model
	}
	if oc.Agent != "" {
		content["default_agent"] = oc.Agent
	}
	b, err := json.Marshal(content)
	if err != nil {
		return env
	}
	return append(out, opencodeConfigContentEnv+"="+string(b))
}

// usesOpencodePermissionReplies reports whether a session's permission
// prompts wait for Torque's reply: opencode serve-http's do.
func usesOpencodePermissionReplies(profile config.AgentProfile, kind RuntimeKind) bool {
	return runtimeIDFor(profile.Provider) == string(runtimes.OpenCode) && kind == RuntimeKindServeHTTP
}

// opencodeReadOnlyTools are the opencode tools, and the permissions of the
// same names, that only read the tree or the agent's own state. Every
// posture short of plan grants them, and an external_directory prompt that
// one of them raised.
var opencodeReadOnlyTools = map[string]bool{
	"read": true, "glob": true, "grep": true, "list": true, "lsp": true,
	"codesearch": true, "todoread": true, "todowrite": true, "skill": true,
}

// decideOpencodePermission answers an opencode permission prompt under the
// profile's posture, as the ACP and Codex responders do (#145, #164):
//
//	plan         nothing
//	default      read-only tools, and external_directory for one of them
//	accept-edits those, and edit
//	yolo         everything (bypassPermissions)
//
// permission is opencode's permission name and tool the tool that asked,
// when known. external_directory is asked by any tool reaching outside the
// project dir, so it is granted short of yolo only when a read-only tool
// asked: an edit, a command or an unknown tool outside the worktree is
// declined, accept-edits included. Everything else (bash, task, webfetch,
// websearch, doom_loop, a new permission) is granted only under yolo. A
// grant is "once", never "always", so no grant outlives the call.
func decideOpencodePermission(posture gopermission.Mode, permission, tool string) (reply, reason string) {
	asked := permission
	if tool != "" {
		asked += " (" + tool + ")"
	}
	granted := false
	switch posture {
	case gopermission.ModeYolo:
		granted = true
	case gopermission.ModePlan:
	default:
		switch {
		case permission == "external_directory":
			granted = opencodeReadOnlyTools[tool]
		case opencodeReadOnlyTools[permission]:
			granted = true
		case permission == "edit":
			granted = posture == gopermission.ModeAcceptEdits
		}
	}
	if granted {
		return "once", fmt.Sprintf("%s is granted under %s", asked, posture)
	}
	return "reject", fmt.Sprintf("%s is not granted under %s", asked, posture)
}

// opencodeServeEvent is the part of an opencode /event SSE frame the
// responder reads. A /global/event frame wraps the same event in payload.
type opencodeServeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	Payload    *struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	} `json:"payload"`
}

type opencodePermissionAsked struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Permission string   `json:"permission"`
	Patterns   []string `json:"patterns"`
	Tool       *struct {
		CallID string `json:"callID"`
	} `json:"tool"`
}

type opencodePartUpdated struct {
	Part struct {
		Type   string `json:"type"`
		CallID string `json:"callID"`
		Tool   string `json:"tool"`
	} `json:"part"`
}

// opencodePermissionTimeout bounds one permission reply.
const opencodePermissionTimeout = 10 * time.Second

// opencodePermissionResponder answers an opencode serve session's
// permission prompts (decideOpencodePermission) and writes each decision to
// log, the session log. It reads the session's stdout lines, which
// go-agent-wrapper emits for the serve process's output and for each SSE
// frame agentkit reads from /event: the listen URL comes first, then tool
// parts (message.part.updated, which name the tool behind a call) and
// permission.asked. Replies go to POST /permission/{id}/reply.
type opencodePermissionResponder struct {
	posture gopermission.Mode
	workdir string // the serve process's directory, opencode's instance key
	log     io.Writer
	client  *http.Client

	mu       sync.Mutex
	baseURL  string
	tools    map[string]string // callID -> tool
	answered map[string]bool
	logMu    sync.Mutex
	wg       sync.WaitGroup
}

func newOpencodePermissionResponder(posture gopermission.Mode, workdir string, log io.Writer) *opencodePermissionResponder {
	if log == nil {
		log = io.Discard
	}
	return &opencodePermissionResponder{
		posture:  posture,
		workdir:  workdir,
		log:      log,
		client:   &http.Client{Timeout: opencodePermissionTimeout},
		tools:    map[string]string{},
		answered: map[string]bool{},
	}
}

// observe reads one stdout line.
func (r *opencodePermissionResponder) observe(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		r.mu.Lock()
		if r.baseURL == "" {
			r.baseURL = opencodeListenURL(line)
		}
		r.mu.Unlock()
		return
	}
	var ev opencodeServeEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}
	typ, props := ev.Type, ev.Properties
	if ev.Payload != nil && ev.Payload.Type != "" {
		typ, props = ev.Payload.Type, ev.Payload.Properties
	}
	switch typ {
	case "message.part.updated":
		var p opencodePartUpdated
		if json.Unmarshal(props, &p) == nil && p.Part.Type == "tool" && p.Part.CallID != "" && p.Part.Tool != "" {
			r.mu.Lock()
			r.tools[p.Part.CallID] = p.Part.Tool
			r.mu.Unlock()
		}
	case "permission.asked":
		var p opencodePermissionAsked
		if json.Unmarshal(props, &p) != nil || p.ID == "" {
			return
		}
		r.mu.Lock()
		if r.answered[p.ID] {
			r.mu.Unlock()
			return
		}
		r.answered[p.ID] = true
		tool := ""
		if p.Tool != nil {
			tool = r.tools[p.Tool.CallID]
		}
		base := r.baseURL
		r.mu.Unlock()
		reply, reason := decideOpencodePermission(r.posture, p.Permission, tool)
		// Reply off the SSE reader's goroutine: observe runs inside it.
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.reply(base, p, tool, reply, reason)
		}()
	}
}

func (r *opencodePermissionResponder) reply(base string, p opencodePermissionAsked, tool, reply, reason string) {
	outcome := reply
	if err := r.post(base, p.ID, reply, reason); err != nil {
		outcome = reply + " not delivered: " + err.Error()
	}
	if tool == "" {
		tool = "unknown"
	}
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintf(r.log, "opencode permission: permission=%s tool=%s patterns=%q posture=%s reply=%s (%s)\n",
		p.Permission, tool, shortenForLog(strings.Join(p.Patterns, " "), 200), r.posture, outcome, reason)
}

func (r *opencodePermissionResponder) post(base, id, reply, reason string) error {
	if base == "" {
		return fmt.Errorf("the serve listen URL is unknown")
	}
	body := map[string]string{"reply": reply}
	if reply == "reject" {
		// A reply with a message reaches the model as feedback, so the turn
		// goes on without the tool instead of stopping.
		body["message"] = "Torque declined this: " + reason + " (the profile's permission_mode)."
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(base, "/") + "/permission/" + url.PathEscape(id) + "/reply"
	if r.workdir != "" {
		endpoint += "?" + url.Values{"directory": {r.workdir}}.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), opencodePermissionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// wait blocks until every reply in flight has finished.
func (r *opencodePermissionResponder) wait() { r.wg.Wait() }

// opencodeListenURL returns the http(s) URL in a line such as opencode
// serve's "opencode server listening on http://127.0.0.1:4096", or "".
// It reads the line the way agentkit's serve-http runtime does.
func opencodeListenURL(line string) string {
	i := strings.Index(line, "http://")
	if i < 0 {
		i = strings.Index(line, "https://")
	}
	if i < 0 {
		return ""
	}
	fields := strings.Fields(line[i:])
	if len(fields) == 0 {
		return ""
	}
	raw := strings.TrimRight(fields[0], ".,;)")
	if _, err := url.ParseRequestURI(raw); err != nil {
		return ""
	}
	return raw
}
