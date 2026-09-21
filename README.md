# Distributed wager processing (MVP)

Serviço Go para movimentar carteiras a partir de `BET`, `WIN`, `LOSS`, `REFUND` e `ROLLBACK` via HTTP e SQS. O Postgres é a autoridade financeira; o processo aplica regras com `SELECT … FOR UPDATE` na carteira.

Decisões e limitações: [ARCHITECTURE.md](ARCHITECTURE.md).

## Pré-requisitos

- Docker Compose
- Go 1.23+ (testes e segunda instância fora do container)

Imagens oficiais do Docker Hub (`postgres`, `golang`, `debian`, `eclipse-temurin`). Keycloak e ElasticMQ (API SQS) são build local. Em redes com TLS corporativo, `go mod download` dentro do Docker pode falhar — o repositório inclui `vendor/` e os binários são baixados no host:

```bash
chmod +x deploy/fetch-binaries.sh
./deploy/fetch-binaries.sh
cp .env.example .env
docker compose up --build
```

```bash
curl -s http://localhost:8080/health/live
curl -s http://localhost:8080/health/ready
```

Encerrar: `Ctrl+C` ou `docker compose down -v`.

## Variáveis

Ver `.env.example`. Obrigatórias: `DATABASE_URL`, `OIDC_ISSUER_URL`, `OIDC_AUDIENCE`, `SQS_INGRESS_QUEUE_URL`, `SQS_EVENT_QUEUE_URL`.

## Identidades (Keycloak)

Realm `wager` em `deploy/keycloak/wager-realm.json`. Issuer no host: `http://localhost:8180/realms/wager`.

| Client | Secret | Papel |
| --- | --- | --- |
| `provider-a` | `local-provider-a` | provedor A |
| `provider-b` | `local-provider-b` | provedor B |
| `internal-service` | `local-internal-service` | carteiras e reconciliação |

```bash
PROVIDER_TOKEN=$(curl -s -X POST http://localhost:8180/realms/wager/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=provider-a -d client_secret=local-provider-a \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

INTERNAL_TOKEN=$(curl -s -X POST http://localhost:8180/realms/wager/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=internal-service -d client_secret=local-internal-service \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
```

## Exemplos

```bash
PLAYER_ID=$(python3 -c 'import uuid; print(uuid.uuid4())')

curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$PLAYER_ID\",\"initialBalance\":{\"amount\":\"1000.00\",\"currency\":\"BRL\"}}"
```

Use o `id` como `$WALLET_ID`.

```bash
curl -s http://localhost:8080/wallets/$WALLET_ID -H "Authorization: Bearer $INTERNAL_TOKEN"

curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: provider-a:ext-1' \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"ext-1\",\"playerId\":\"$PLAYER_ID\",\"walletId\":\"$WALLET_ID\",\"roundId\":\"round-1\",\"gameId\":\"game-1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}"

curl -s "http://localhost:8080/wallets/$WALLET_ID/ledger?limit=50" -H "Authorization: Bearer $INTERNAL_TOKEN"
curl -s -X POST http://localhost:8080/wallets/$WALLET_ID/reconciliation -H "Authorization: Bearer $INTERNAL_TOKEN"
```

Rotas públicas: `GET /health/live`, `GET /health/ready`.

## Filas

ElasticMQ (`deploy/sqs/elasticmq.conf`), porta `9324`:

- `wager-transactions.fifo` (redrive para DLQ após 5 recebimentos)
- `wager-transactions-dlq.fifo`
- `wager-events.fifo`

`MessageGroupId` = walletId; `MessageDeduplicationId` = messageId (entrada) ou eventId (saída).

## Verificação (comandos do desafio)

Com o Compose no ar e `DATABASE_URL` exportado (sem isso, `internal/integration` faz skip):

```bash
export DATABASE_URL=postgres://wager:wager@localhost:5432/wager?sslmode=disable

curl -s http://localhost:8080/health/live
curl -s http://localhost:8080/health/ready

go vet ./...
go test ./...
go test -race ./...
```

Atalho: `./scripts/challenge-verify.sh`

Provas nomeadas:

```bash
go test ./internal/integration -count=1 -v
```

| Teste | Cenário |
| --- | --- |
| `TestFiftyIdenticalBetsDebitOnce` | 50 apostas iguais → um débito |
| `TestTwoBetsEightyOnHundred` | saldo 100, duas BET 80 → uma ok, uma rejeitada, saldo 20 |
| `TestReplayKeepsOriginalBalance` | replay com saldo da época |
| `TestSameOperationHTTPThenSQS` | mesma operação nas duas entradas → um movimento |

Fora do MVP automatizado: três processos, failpoints, DLQ e2e, Keycloak e2e, publishers concorrentes. Auth HTTP: curls acima (token ausente deve retornar 401).

## Migrations

```bash
go run ./cmd/migrate -direction up
go run ./cmd/migrate -direction down
```

O Compose aplica `up` no serviço `migrate` antes da app.

## Segunda instância (manual)

```bash
HTTP_PORT=8081 DATABASE_URL=postgres://wager:wager@localhost:5432/wager?sslmode=disable \
  OIDC_ISSUER_URL=http://localhost:8180/realms/wager OIDC_AUDIENCE=wager-api \
  SQS_ENDPOINT=http://localhost:9324 \
  SQS_INGRESS_QUEUE_URL=http://localhost:9324/000000000000/wager-transactions.fifo \
  SQS_EVENT_QUEUE_URL=http://localhost:9324/000000000000/wager-events.fifo \
  go run ./cmd/app
```
