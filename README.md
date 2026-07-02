# CO Tracker

A daily unit-tracking schedule app. A month calendar shows which **business
units** are active each day as color-coded pills; hovering a pill shows the
names of the people assigned (plus any notes). People and schedule data can be
loaded in bulk via CSV.

The backend is a single Go binary with an embedded web UI and an embedded
SQLite database — it compiles with `CGO_ENABLED=0` into a ~14 MB `FROM scratch`
container with **zero runtime dependencies**, which is what makes the
lowest-cost AWS deployments possible (see [docs/deploy-aws.md](docs/deploy-aws.md)).

## Features

- **Month calendar** with previous/next/today navigation.
- **Business units**: create, rename (double-click), recolor (color swatch),
  delete. Each calendar entry is a pill in its unit's color.
- **Hover tooltips**: hovering any calendar pill lists the people assigned to
  that entry, plus notes.
- **People**: add/remove individually or bulk-import via CSV.
- **CSV import** for both people and schedule entries (see formats below).
  Unknown units and people in a schedule CSV are created automatically;
  auto-created units get distinct colors from a built-in palette.
- **JSON REST API** underneath, usable without the UI.

## Quick start

```sh
# with Docker
docker compose up --build
# or natively (Go 1.24+)
go run .
```

Open <http://localhost:8080>. Data is stored in a SQLite file
(`data/co_tracker.db` natively, `/data/co_tracker.db` in the container —
mounted as the `co_tracker_data` volume by docker-compose).

Try the sample data: import `samples/users.csv` under **Import CSV → People**,
then `samples/schedule.csv` under **Schedule**, and navigate to July 2026.

### Configuration

| Env var       | Default              | Purpose                    |
|---------------|----------------------|----------------------------|
| `LISTEN_ADDR` | `:8080`              | HTTP listen address        |
| `DB_PATH`     | `data/co_tracker.db` | SQLite database file path  |

Health check endpoint: `GET /healthz`.

## CSV formats

**People** (`POST /api/import/users`, UI: *Import CSV → People*):

```csv
name,email
Alice Nguyen,alice@example.com
Ben Ortiz,ben@example.com
```

Header row optional. Names are matched case-insensitively; re-importing
updates emails instead of creating duplicates.

**Schedule** (`POST /api/import/entries`, UI: *Import CSV → Schedule*):

```csv
date,unit,users,notes
2026-07-06,Operations,Alice Nguyen;Ben Ortiz,Morning shift
2026-07-07,Field Team,Evan Wright,
```

- `date` — `YYYY-MM-DD`
- `unit` — business unit name; created automatically if new
- `users` — semicolon-separated names; created automatically if new; may be empty
- `notes` — optional free text

Each import row creates a new entry (re-importing the same schedule file will
duplicate entries). Rows with problems are reported per-row without aborting
the rest of the import.

## API

| Method & path                | Description                                    |
|------------------------------|------------------------------------------------|
| `GET /api/units`             | List business units                            |
| `POST /api/units`            | Create `{name, color}`                         |
| `PUT /api/units/{id}`        | Update name/color                              |
| `DELETE /api/units/{id}`     | Delete unit (cascades to its entries)          |
| `GET /api/users`             | List people                                    |
| `POST /api/users`            | Create `{name, email}`                         |
| `DELETE /api/users/{id}`     | Delete person                                  |
| `GET /api/entries?from=&to=` | Entries in date range (inclusive, YYYY-MM-DD)  |
| `POST /api/entries`          | Create `{date, unitId, userIds, notes}`        |
| `PUT /api/entries/{id}`      | Replace an entry                               |
| `DELETE /api/entries/{id}`   | Delete an entry                                |
| `POST /api/import/users`     | CSV import (multipart `file` or raw body)      |
| `POST /api/import/entries`   | CSV import (multipart `file` or raw body)      |

Entries returned by `GET /api/entries` include the resolved unit name/color
and assigned user names, so a client needs no extra lookups to render a
calendar.

## Deploying on AWS

See **[docs/deploy-aws.md](docs/deploy-aws.md)** for cost-ranked options.
Summary: a Lightsail nano instance (or EC2 `t4g.nano`) running the container
with the SQLite volume on local disk is the cheapest (≈ $4–7/month);
ECS Fargate + EFS is the fully managed container-native option (≈ $12+/month).

## Development

```sh
go test ./...   # API + store tests (uses a temp SQLite db)
go vet ./...
```

Project layout:

- `main.go` — entrypoint; embeds `web/` into the binary
- `internal/store` — SQLite persistence (pure-Go driver, no CGO)
- `internal/api` — HTTP handlers, CSV import
- `web/` — vanilla JS/CSS single-page UI (no build step)
- `samples/` — example CSV files
