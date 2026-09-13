# KumoDB

PostgreSQL-compatible proxy (session 1: TCP + startup/SSL framing). Requires Go 1.26+ and Docker Compose. The binary does **not** connect to Postgres yet.

```bash
go test -race ./...
go run ./cmd/kumodb                 # listens on 127.0.0.1:15432; Ctrl+C to stop
docker compose up -d --wait
docker compose exec postgres psql -U postgres -d postgres -c 'SELECT 1'
docker compose down
```

Local Postgres is `localhost:5433` (user/password/database: `postgres`). Host 5432 is left free for other stacks. Data is stored in the `postgres_data` volume.

Session write-up: [KumoDB session 1](https://avikmukherjee.com/blog/kumodb-session-1-postgres-wire-protocol)
