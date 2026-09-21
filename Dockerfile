FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o /out/app ./cmd/app && CGO_ENABLED=0 go build -mod=vendor -o /out/migrate ./cmd/migrate

FROM debian:bookworm-slim
COPY --from=build /out/app /app
COPY --from=build /out/migrate /migrate
COPY migrations /migrations
ENTRYPOINT ["/app"]
