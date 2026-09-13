# KumoDB

PostgreSQL-compatible proxy (early: process lifecycle only). Requires Go 1.26+ and Docker Compose. The binary does not connect to Postgres yet.

```bash
go test ./...
go run ./cmd/kumodb          # Ctrl+C to stop
docker compose up -d
docker compose exec postgres psql -U postgres -d postgres -c 'SELECT 1'
docker compose down
```

Local Postgres is `localhost:5432` (user/password/database: `postgres`). Data is stored in the `postgres_data` volume.
