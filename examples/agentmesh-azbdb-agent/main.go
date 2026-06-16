package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	azbdb "github.com/Hoverhuang-er/azbdb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const connectionStringEnv = "AZB_CONNECTION_STRING"

type memoryAgent struct {
	db *sql.DB
}

type memoryRecord struct {
	ID        int64  `json:"id"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("agentmesh azbdb agent failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	connectionString := strings.TrimSpace(os.Getenv(connectionStringEnv))
	if connectionString == "" {
		return fmt.Errorf("%s must contain an Azure Storage connection string with ContainerName", connectionStringEnv)
	}

	db, err := azbdb.NewDB(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("open azbdb: %w", err)
	}
	defer db.Close()
	if err := azbdb.CreateTable(ctx, db, "agent_memory", "id primary key, topic, content, created_at"); err != nil {
		return fmt.Errorf("create agent memory table: %w", err)
	}

	agent := memoryAgent{db: db}
	meshAgent := server.NewMCPServer(
		"azbdb-memory-agent",
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions("Use this agent to persist and recall durable memory through AZBDB. It is designed to run next to AgentMesh Go, which handles peer discovery and message delivery."),
	)
	meshAgent.AddTool(rememberTool(), agent.remember)
	meshAgent.AddTool(recallTool(), agent.recall)
	meshAgent.AddTool(statusTool(), agent.status)

	slog.Info("starting AgentMesh-compatible AZBDB memory agent")
	return server.ServeStdio(meshAgent)
}

func rememberTool() mcp.Tool {
	return mcp.NewTool("azbdb_remember",
		mcp.WithDescription("Persist a memory item in AZBDB so mesh peers can rely on durable state."),
		mcp.WithString("topic", mcp.Required(), mcp.Description("Short topic key for the memory item.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Memory content to persist.")),
	)
}

func recallTool() mcp.Tool {
	return mcp.NewTool("azbdb_recall",
		mcp.WithDescription("Recall recent AZBDB memory items, optionally filtered by topic."),
		mcp.WithString("topic", mcp.Description("Optional topic key to filter by.")),
	)
}

func statusTool() mcp.Tool {
	return mcp.NewTool("azbdb_agent_status",
		mcp.WithDescription("Report the durable-memory agent status."),
	)
}

func (a memoryAgent) remember(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, ok := stringArg(request, "topic")
	if !ok {
		return mcp.NewToolResultError("topic must be a string"), nil
	}
	content, ok := stringArg(request, "content")
	if !ok {
		return mcp.NewToolResultError("content must be a string"), nil
	}
	topic = strings.TrimSpace(topic)
	content = strings.TrimSpace(content)
	if topic == "" || content == "" {
		return mcp.NewToolResultError("topic and content must not be empty"), nil
	}

	record := memoryRecord{
		ID:        time.Now().UnixNano(),
		Topic:     topic,
		Content:   content,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, err := a.db.ExecContext(ctx, `insert into agent_memory values (?, ?, ?, ?)`, record.ID, record.Topic, record.Content, record.CreatedAt)
	if err != nil {
		slog.Error("remember failed", slog.Any("error", err))
		return mcp.NewToolResultError("could not persist memory"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("remembered %q at %s", record.Topic, record.CreatedAt)), nil
}

func (a memoryAgent) recall(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, _ := stringArg(request, "topic")
	records, err := a.queryMemory(ctx, topic)
	if err != nil {
		slog.Error("recall failed", slog.Any("error", err))
		return mcp.NewToolResultError("could not recall memory"), nil
	}
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (a memoryAgent) status(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var count int64
	if err := a.db.QueryRowContext(ctx, `select count(*) from agent_memory`).Scan(&count); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("azbdb-memory-agent ready; stored memories=%d", count)), nil
}

func (a memoryAgent) queryMemory(ctx context.Context, topic string) ([]memoryRecord, error) {
	query := `select id, topic, content, created_at from agent_memory order by created_at desc limit 20`
	args := []any{}
	if topic != "" {
		query = `select id, topic, content, created_at from agent_memory where topic = ? order by created_at desc limit 20`
		args = append(args, topic)
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memoryRecord{}
	for rows.Next() {
		var record memoryRecord
		if err := rows.Scan(&record.ID, &record.Topic, &record.Content, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func stringArg(request mcp.CallToolRequest, name string) (string, bool) {
	value, ok := request.Params.Arguments[name]
	if !ok || value == nil {
		return "", false
	}
	s, ok := value.(string)
	return s, ok
}
