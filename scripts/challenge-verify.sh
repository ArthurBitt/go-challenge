#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

export DATABASE_URL="${DATABASE_URL:-postgres://wager:wager@localhost:5432/wager?sslmode=disable}"

echo "== health =="
curl -sf http://localhost:8080/health/live
echo
curl -sf http://localhost:8080/health/ready
echo

echo "== go vet ./... =="
go vet ./...

echo "== go test ./... =="
go test ./...

echo "== go test -race ./... =="
go test -race ./...

echo "== go test ./internal/integration -count=1 -v =="
go test ./internal/integration -count=1 -v

echo "OK"
