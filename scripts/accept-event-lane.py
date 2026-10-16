#!/usr/bin/env python3
"""Exercise a deployed Torque event lane using one temporary manual task.

Requires Python 3, curl, TORQUE_BASE_URL (server origin) and TORQUE_API_TOKEN.
The token needs task read/write/delete, MCP tool and event-stream access.
Uses /api/v1/events via curl -N; no database access or scheduler configuration.
Deletes its task on success or failure. Durable event/audit history may remain.
Prints checks and its own task/event cursors, never tokens or server payloads.
"""

import json
import os
import queue
import re
import shutil
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class CheckFailed(Exception):
    pass


def check(condition, message):
    if not condition:
        raise CheckFailed(message)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self):
        self.base = os.environ.get("TORQUE_BASE_URL", "").rstrip("/")
        self.token = os.environ.get("TORQUE_API_TOKEN", "")
        url = urllib.parse.urlsplit(self.base)
        check(url.scheme in ("http", "https") and url.netloc and not
              (url.username or url.password or url.query or url.fragment or url.path),
              "TORQUE_BASE_URL must be an HTTP(S) server origin")
        check(self.token and not any(c in self.token for c in "\r\n"),
              "TORQUE_API_TOKEN must be set and contain no line breaks")
        check(shutil.which("curl"), "curl is required")
        self.opener = urllib.request.build_opener(NoRedirect())
        self.rpc_id = 0
        self.mcp_headers = {}

    def request(self, method, path, body=None, headers=None, expected=200):
        hdr = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        hdr.update(headers or {})
        data = None
        if body is not None:
            data = json.dumps(body, ensure_ascii=False).encode()
            hdr["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base + path, data, hdr, method=method)
        try:
            response = self.opener.open(req, timeout=15)
        except urllib.error.HTTPError as err:
            response = err
        except (OSError, urllib.error.URLError):
            raise CheckFailed("HTTP transport failed") from None
        with response:
            check(response.status == expected,
                  f"{method} {path}: expected HTTP {expected}, got {response.status}")
            return response.read(), response.headers

    def api(self, method, path, body=None, headers=None, expected=200):
        raw, _ = self.request(method, "/api/v1" + path, body, headers, expected)
        return json.loads(raw) if raw else None

    def rpc(self, method, params):
        self.rpc_id += 1
        raw, headers = self.request("POST", "/mcp", {
            "jsonrpc": "2.0", "id": self.rpc_id, "method": method, "params": params,
        }, {"Accept": "application/json, text/event-stream", **self.mcp_headers})
        if headers.get("Mcp-Session-Id"):
            self.mcp_headers["Mcp-Session-Id"] = headers["Mcp-Session-Id"]
        if "text/event-stream" in headers.get("Content-Type", ""):
            replies = [frame for _, frame in frames(raw.decode().splitlines())]
            reply = next((r for r in replies if r.get("id") == self.rpc_id), {})
        else:
            reply = json.loads(raw)
        check(reply.get("id") == self.rpc_id and "result" in reply,
              "MCP request failed: " + method)
        return reply["result"]

    def initialize(self):
        result = self.rpc("initialize", {"protocolVersion": "2025-03-26",
                          "capabilities": {}, "clientInfo": {
                              "name": "torque-event-acceptance", "version": "1"}})
        self.mcp_headers["MCP-Protocol-Version"] = result["protocolVersion"]
        self.request("POST", "/mcp", {"jsonrpc": "2.0", "method": "notifications/initialized"},
                     {"Accept": "application/json, text/event-stream", **self.mcp_headers}, 202)

    def tool(self, name, args, error=None):
        result = self.rpc("tools/call", {"name": name, "arguments": args})
        content = result.get("structuredContent")
        if content is None:
            texts = [v["text"] for v in result.get("content", []) if v.get("type") == "text"]
            check(len(texts) == 1, "MCP response has no structured result")
            content = json.loads(texts[0])
        if error:
            check(result.get("isError") and content.get("error", {}).get("code") == error,
                  name + " did not return " + error)
            return content
        check(not result.get("isError") and content.get("ok") is True, name + " failed")
        return content["data"]


def frames(lines):
    cursor, data = "", []
    for line in lines:
        line = line.rstrip("\r\n")
        if not line:
            if data:
                yield cursor, json.loads("\n".join(data))
            cursor, data = "", []
        elif line.startswith("id:"):
            cursor = line[3:].strip()
        elif line.startswith("data:"):
            data.append(line[5:].lstrip())


class Stream:
    def __init__(self, client, resume=None):
        self.messages = queue.Queue()
        # Feed headers through stdin config: the token never appears in argv,
        # shell tracing, output files, diagnostics, or the URL.
        config = "header = " + json.dumps("Authorization: Bearer " + client.token) + "\n"
        if resume:
            config += "header = " + json.dumps("Last-Event-ID: " + resume) + "\n"
        self.proc = subprocess.Popen([
            "curl", "--disable", "-N", "--silent", "--fail", "--max-time", "45",
            "--config", "-", client.base + "/api/v1/events",
        ], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        self.proc.stdin.write(config)
        self.proc.stdin.close()
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        try:
            for frame in frames(self.proc.stdout):
                self.messages.put(frame)
        except (ValueError, OSError):
            pass  # Surface a generic stream failure; never dump raw event text.
        finally:
            self.messages.put(None)

    def next(self, deadline):
        try:
            item = self.messages.get(timeout=max(0.001, deadline - time.monotonic()))
        except queue.Empty:
            raise CheckFailed("timed out waiting for SSE") from None
        check(item is not None, "curl SSE stream ended early")
        return item

    def connected(self):
        _, frame = self.next(time.monotonic() + 15)
        check(frame.get("type") == "connected", "SSE did not acknowledge connection")

    def through(self, task_id, stop_status):
        own, previous, generation = [], -1, None
        deadline = time.monotonic() + 15
        while True:
            cursor, frame = self.next(deadline)
            check(frame.get("type") != "gap", "SSE reported a replay gap")
            if not cursor:
                continue  # best-effort CRUD pings have no cursor
            check(re.fullmatch(r"\d+:\d+", cursor), "invalid SSE cursor")
            gen, seq = map(int, cursor.split(":"))
            generation = gen if generation is None else generation
            check(gen == generation and seq > previous, "SSE generation changed or sequence regressed")
            previous = seq
            event = frame.get("envelope", {})
            if event.get("scope", {}).get("task_id") != task_id:
                continue  # other hub traffic is allowed; never print its payload
            check(re.fullmatch(r"evt_[0-9a-f]{32}", event.get("id", "")), "invalid canonical event ID")
            own.append((cursor, event))
            if event.get("kind") == "task.state_changed" and event["payload"].get("to") == stop_status:
                return own

    def close(self):
        if self.proc.poll() is None:
            self.proc.terminate()
        try:
            self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait()
        self.reader.join(timeout=1)
        self.proc.stdout.close()


def run():
    client, task_id, streams = Client(), None, []
    try:
        client.initialize()
        live = Stream(client)
        streams.append(live)
        live.connected()
        task = client.api("POST", "/tasks", {
            "title": "Event acceptance " + uuid.uuid4().hex[:12], "kind": "issue",
            "manual": True, "progress_detail": "step", "checkpoint_mode": "blocking",
        }, expected=201)
        task_id = task["id"]
        check(re.fullmatch(r"[A-Za-z0-9_-]+", task_id), "unexpected task ID")
        path = "/tasks/" + task_id
        print("Created temporary manual task " + task_id, flush=True)
        client.api("POST", path + "/transition", {"status": "doing"}, {"X-Client": "browser"})
        client.api("POST", path + "/transition", {"status": "blocked"})
        client.tool("torque_task_transition", {"id": task_id, "status": "paused"})
        milestone = client.tool("torque_task_progress", {
            "task_id": task_id, "label": "milestone", "level": "milestone", "idempotency_key": "milestone"})
        step_input = {"task_id": task_id, "label": "step", "level": "step", "idempotency_key": "step"}
        step = client.tool("torque_task_progress", step_input)
        # Same durable limiter across transports. Do this immediately after step.
        limited = client.api("POST", path + "/progress", {"label": "limited", "level": "step", "idempotency_key": "limited"})
        duplicate = client.tool("torque_task_progress", step_input)
        for report in (milestone, step):
            check(report.get("emitted") is True and report.get("effective_level") == "step"
                  and report.get("detail_source") == "task", "progress detail or emission mismatch")
        check(limited.get("emitted") is False and limited.get("suppressed_reason") == "rate_limited",
              "HTTP report did not share the MCP rate limit (requires requests within two seconds)")
        check(duplicate.get("emitted") is False and duplicate.get("suppressed_reason") == "duplicate"
              and duplicate.get("event_id") == step["event_id"], "duplicate receipt mismatch")
        data = client.tool("torque_task_data_emit", {"task_id": task_id, "schema": "acceptance@1",
                           "data": {"accepted": True}, "idempotency_key": "data"})
        client.tool("torque_task_data_emit", {"task_id": task_id, "schema": "acceptance@1",
                    "data": {"oversized": "x" * 16385}}, error="data_too_large")
        check(client.api("GET", path)["status"] == "paused", "self-report parked or transitioned the task")
        # A final state change is a committed-stream barrier after suppressed calls.
        client.api("POST", path + "/transition", {"status": "review"})
        events = live.through(task_id, "review")
        expected = ["task.state_changed"] * 3 + ["progress.checkpoint"] * 2 + ["data.emitted", "task.state_changed"]
        check([e["kind"] for _, e in events] == expected, "unexpected events or emission from a suppressed call")
        check(len({e["id"] for _, e in events}) == len(events), "duplicate canonical event ID")
        for (_, event), trigger, status in zip(events[:3], ["human", "agent", "agent"], ["doing", "blocked", "paused"]):
            check(event["payload"]["trigger"]["kind"] == trigger and event["payload"]["to"] == status,
                  "HTTP/MCP state trigger mismatch")
        check([e["id"] for _, e in events[3:6]] == [milestone["event_id"], step["event_id"], data["event_id"]],
              "report receipts do not match streamed events")
        check([e["payload"]["detail_level"] for _, e in events[3:5]] == ["milestone", "step"],
              "streamed progress levels mismatch")
        check(events[5][1]["payload"]["data"] == {"accepted": True}, "inline data mismatch")
        live.close()
        streams.remove(live)
        resumed = Stream(client, events[0][0])
        streams.append(resumed)
        resumed.connected()
        replay = resumed.through(task_id, "review")
        check(replay == events[1:], "Last-Event-ID did not replay exactly the subsequent events")
        print("PASS HTTP/MCP triggers; progress levels, duplicate and shared rate limit; inline/oversized data; non-parking")
        print(f"PASS curl -N live + Last-Event-ID replay; cursors {events[0][0]} through {events[-1][0]}")
    finally:
        for stream in streams:
            stream.close()
        if task_id:
            client.api("DELETE", "/tasks/" + task_id, expected=204)
            client.api("GET", "/tasks/" + task_id, expected=404)
            print("Deleted temporary task " + task_id, flush=True)


if __name__ == "__main__":
    try:
        run()
    except KeyboardInterrupt:
        print("FAIL interrupted", file=sys.stderr)
        sys.exit(1)
    except Exception as err:
        # Unexpected exceptions may contain raw response text or credentials.
        print("FAIL " + (str(err) if isinstance(err, CheckFailed) else type(err).__name__), file=sys.stderr)
        sys.exit(1)
