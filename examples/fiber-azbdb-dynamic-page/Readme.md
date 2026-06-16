Fiber AZBDB Dynamic Page
========================

This example is a small Fiber web app that uses AZBDB as its database dependency. It renders a dynamic guestbook page, accepts form posts, and stores each message in an Azure Blob-backed SQLite WAL table.

What it demonstrates:

* `github.com/gofiber/fiber/v2` for HTTP routing and form handling.
* `github.com/Hoverhuang-er/azbdb` imported as `azbdb`.
* `azbdb.NewDB` opening the Azure Blob-backed SQLite database from one connection string.
* `azbdb.CreateTable` creating the `messages` virtual table.
* Normal `database/sql` inserts and queries feeding server-rendered HTML.

Run it:

```
export AZB_CONNECTION_STRING='DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=key;EndpointSuffix=core.windows.net;ContainerName=mycontainer'
go run .
```

Then open `http://127.0.0.1:8080/`.

Optional environment:

* `PORT` - HTTP port. Default: `8080`.

AZB also starts its own Prometheus endpoint at `http://127.0.0.1:9190/metrics` when the Azure Blob WAL client opens. That endpoint contains AZB WAL metrics only, not Fiber application metrics.
