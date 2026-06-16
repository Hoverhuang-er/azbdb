AgentMesh AZBDB Agent
=====================

This example is a small Go MCP agent with durable memory in AZBDB. It is designed to run next to AgentMesh Go: AgentMesh handles peer discovery and message delivery, while this agent exposes memory tools backed by Azure Blob SQLite WAL.

What it demonstrates:

* `github.com/Hoverhuang-er/azbdb` imported as `azbdb` for durable SQLite-compatible storage.
* `github.com/mark3labs/mcp-go` for the MCP server shape used by AgentMesh Go.
* A durable `agent_memory` table stored in Azure Blob WAL.
* Agent tools that remember, recall, and report status without keeping process-local state.

Tools exposed by this agent:

* `azbdb_remember(topic, content)` - persist a memory item.
* `azbdb_recall(topic?)` - return recent memory items, optionally filtered by topic.
* `azbdb_agent_status()` - report readiness and memory count.

Run it:

```
export AZB_CONNECTION_STRING='DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=key;EndpointSuffix=core.windows.net;ContainerName=mycontainer'
go run .
```

AgentMesh Go wiring:

1. Install and run AgentMesh Go in the same harness session, usually as `agentmesh serve`.
2. Register this example as another MCP stdio server command: `go run ./examples/agentmesh-azbdb-agent`.
3. Mesh peers can coordinate through AgentMesh; this agent provides shared durable memory through AZBDB.

AZB also starts its own Prometheus endpoint at `http://127.0.0.1:9190/metrics` when the Azure Blob WAL client opens. That endpoint contains AZB WAL metrics only, not host-agent metrics.
