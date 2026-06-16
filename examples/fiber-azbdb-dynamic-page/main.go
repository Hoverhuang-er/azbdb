package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	azbdb "github.com/Hoverhuang-er/azbdb"
)

const connectionStringEnv = "AZB_CONNECTION_STRING"

var page = template.Must(template.New("messages").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Fiber + AZBDB guestbook</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 760px; margin: 3rem auto; padding: 0 1rem; }
    form { display: grid; gap: .75rem; margin: 2rem 0; }
    input, textarea, button { font: inherit; padding: .7rem; }
    article { border: 1px solid #ddd; border-radius: 12px; padding: 1rem; margin: 1rem 0; }
    time { color: #666; font-size: .875rem; }
  </style>
</head>
<body>
  <h1>Fiber + AZBDB guestbook</h1>
  <p>This dynamic page stores messages in SQLite virtual tables backed by Azure Blob WAL.</p>
  <form method="post" action="/messages">
    <input name="author" maxlength="80" placeholder="Name" required>
    <textarea name="body" maxlength="2000" placeholder="Message" required></textarea>
    <button type="submit">Save message</button>
  </form>
  {{range .Messages}}
  <article>
    <strong>{{.Author}}</strong>
    <time datetime="{{.CreatedAt}}">{{.CreatedAt}}</time>
    <p>{{.Body}}</p>
  </article>
  {{else}}
  <p>No messages yet.</p>
  {{end}}
</body>
</html>`))

type message struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt string
}

type appState struct {
	db *sql.DB
}

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("fiber azbdb example failed", slog.Any("error", err))
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
	if err := azbdb.CreateTable(ctx, db, "messages", "id primary key, author, body, created_at"); err != nil {
		return fmt.Errorf("create messages table: %w", err)
	}

	state := appState{db: db}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", state.handleIndex)
	app.Post("/messages", state.handleCreateMessage)
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })

	addr := ":8080"
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		addr = ":" + port
	}
	slog.Info("starting Fiber AZBDB example", slog.String("addr", addr))
	return app.Listen(addr)
}

func (s appState) handleIndex(c *fiber.Ctx) error {
	messages, err := listMessages(c.Context(), s.db)
	if err != nil {
		slog.Error("list messages", slog.Any("error", err))
		return fiber.NewError(http.StatusInternalServerError, "could not load messages")
	}
	var out bytes.Buffer
	if err := page.Execute(&out, map[string]any{"Messages": messages}); err != nil {
		return fiber.NewError(http.StatusInternalServerError, err.Error())
	}
	c.Type("html", "utf-8")
	return c.Send(out.Bytes())
}

func (s appState) handleCreateMessage(c *fiber.Ctx) error {
	author := strings.TrimSpace(c.FormValue("author"))
	body := strings.TrimSpace(c.FormValue("body"))
	if author == "" || body == "" {
		return fiber.NewError(http.StatusBadRequest, "author and body are required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := time.Now().UnixNano()
	_, err := s.db.ExecContext(c.Context(), `insert into messages values (?, ?, ?, ?)`, id, author, body, now)
	if err != nil {
		slog.Error("insert message", slog.Any("error", err))
		return fiber.NewError(http.StatusInternalServerError, "could not save message")
	}
	return c.Redirect("/", http.StatusSeeOther)
}

func listMessages(ctx context.Context, db *sql.DB) ([]message, error) {
	rows, err := db.QueryContext(ctx, `select id, author, body, created_at from messages order by created_at desc limit 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []message{}
	for rows.Next() {
		var msg message
		if err := rows.Scan(&msg.ID, &msg.Author, &msg.Body, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}
