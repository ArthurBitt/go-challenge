# Arquitetura (MVP)

## Dependências

O domínio (`internal/domain`) não importa Fx, HTTP, SQS nem pgx. HTTP e o consumidor SQS chamam o mesmo `service.ProcessWager`. Uber Fx existe só em `cmd/app`: config, pool, OIDC, casos de uso, HTTP e workers.

## Money

`int64` em centavos + moeda de 3 letras. Parse externo só aceita `^(0|[1-9][0-9]*)\.[0-9]{2}$`. Sem `float32`/`float64`. Persistência em `BIGINT`. Entradas financeiras do MVP são BRL; incompatibilidade de moeda é teste de domínio.

## Transação SQL

`BEGIN` → `SELECT wallet FOR UPDATE` → regras → `INSERT` transação/ledger/outbox (e inbox no SQS) → `COMMIT`. O handler HTTP nunca publica na fila de eventos.

## Idempotência

Hash SHA-256 de JSON com chaves ordenadas (Go `encoding/json` em `map`): provider, ids, round, game, kind, amount, currency, referência. Unique `(provider_id, idempotency_key)` e `(provider_id, external_transaction_id)`. Replay equivalente devolve `result_balance_cents` da época. Chave reusada com hash diferente → conflito.

## Locks

Só a linha da carteira. Carteiras distintas não se bloqueiam. Não há mutex global de processo: duas instâncias no mesmo banco são seguras para o saldo.

## Referências pendentes

`REFUND`/`ROLLBACK` (e `WIN` com referência) sem a transação alvo viram `PENDING_REFERENCE`. Um ticker de 2s no mesmo processo tenta de novo até `REFERENCE_MAX_ATTEMPTS` (padrão 10) e rejeita com `REFERENCE_NOT_FOUND`. Sem backoff exponencial nem lease entre instâncias.

## Reversões

Uma `REFUND` ou `ROLLBACK` **processada** consome a BET. Rollback de um `REFUND` aponta para o refund, não para a BET. Unique indexes parciais no Postgres impedem duas reversões processadas do mesmo tipo na mesma referência. Código `REVERSAL_INSUFFICIENT_FUNDS` quando o rollback precisaria debitar mais que o saldo.

## Inbox / outbox

Inbox: unique `(consumer_name, message_id)` no mesmo commit do domínio. A mensagem SQS só é apagada depois do commit. Digest diferente no mesmo `messageId` é conflito.

Outbox: insert no commit; um goroutine publica e marca `published_at`. Um publisher, sem disputa de lease. Republicação usa o mesmo `eventId` como `MessageDeduplicationId`.

## Autenticação

Keycloak (`client_credentials`). JWT validado com issuer, expiração e audience `wager-api`. Claim `provider_id` autoriza o provedor; `role=internal` autoriza carteiras. O `providerId` do body não pode divergir do token. Provedor B consultando transação de A recebe 404/403 sem dados. Sem emissão própria de tokens.

SQS local não aplica IAM: allowlist `SQS_ALLOWED_PROVIDERS` no consumer. Emulador local: ElasticMQ (API SQS).

## Fx e shutdown

`OnStart` sobe HTTP e três loops (consumer, publisher, referências). `OnStop` cancela o context dos workers e dá `Shutdown` no HTTP antes de fechar o pool.

## O que este MVP não faz

- Teste automatizado de três processos, failpoints, matar o consumidor entre commit e delete
- Vários publishers disputando a outbox
- Escopos OIDC finos (`wallets:write` etc.)
- Tracing, Prometheus completo, testes de carga, UI, ledger de partidas dobradas
- Cursor de ledger opaco além do `id` em base64
- Aceite `PENDING` intermediário em operações sem referência (vão direto para `PROCESSED`/`REJECTED`)
