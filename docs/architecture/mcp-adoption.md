# MCP library adoption

Torque's production MCP servers already use `go-mcp/server`: the global
adapter in `internal/mcpadapter/adapter.go` and the per-task adapter in
`internal/mcpadapter/loopback.go`. Both HTTP entry points in
`internal/runtime/bootstrap` use `go-mcp/transport/http`. Stdio uses the same
adapters. There are no direct official-SDK server or HTTP-handler constructors
in Torque's application code.

The SDK remains necessary for protocol middleware, requests, resources and
session handling exposed through `SDKServer()`. Those uses do not construct a
second server.

## Authentication boundary

The daemon mounts `/mcp` behind the shared HTTP API security middleware in
`internal/httpserver/security.go`. Its `security/auth` Guard verifies JWTs and
static API keys, stamps the application principal, and supports live mode and
key reloads. `internal/mcpadapter/authz.go` checks each tool's scope through
that same Guard. Stdio and per-task loopback adapters retain their existing
local trust boundary.

This is an intentional exception to `go-mcp/auth.HTTPMiddleware`, whose
Provider contract returns the official SDK's bearer-token middleware options.
Replacing the Guard would change off/permissive admission, principal context,
reload semantics, per-tool authorization, and the existing HTTP error
contract. A second mandatory bearer middleware would also reject callers the
current local policy admits. Keep this policy until a shared provider can
preserve those contracts; do not replace it with an unconditional static-token
provider or `CanAccess` policy that denies unauthenticated local calls.

The current `plugin-mcp` pin is v0.3.0. Updating to v0.10.0 also changes empty
HTTP Host/Origin allowlists. Such an update must carry explicit transport
configuration and tests for Torque's configured remote-token and CORS paths,
independently of server-constructor adoption.
