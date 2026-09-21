package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"junglego/internal/config"
	"junglego/internal/domain"
	"junglego/internal/postgres"
	"junglego/internal/service"
)

type Client struct {
	SQS             *sqs.Client
	IngressQueueURL string
	EventQueueURL   string
}

func NewClient(ctx context.Context, cfg config.Config) (*Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	}
	if cfg.SQSEndpoint != "" {
		loadOpts = append(loadOpts, awsconfig.WithBaseEndpoint(cfg.SQSEndpoint))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		SQS:             sqs.NewFromConfig(awsCfg),
		IngressQueueURL: cfg.IngressQueueURL,
		EventQueueURL:   cfg.EventQueueURL,
	}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.SQS.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(c.IngressQueueURL),
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages},
	})
	return err
}

type Workers struct {
	Log              *slog.Logger
	Svc              *service.Service
	Store            *postgres.Store
	Client           *Client
	AllowedProviders map[string]struct{}
}

func (w *Workers) allowed(id string) bool {
	_, ok := w.AllowedProviders[id]
	return ok
}

func (w *Workers) RunConsumer(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		out, err := w.Client.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(w.Client.IngressQueueURL),
			MaxNumberOfMessages: 5,
			WaitTimeSeconds:     10,
			VisibilityTimeout:   30,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.Log.Error("sqs receive", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for _, msg := range out.Messages {
			w.handle(ctx, msg)
		}
	}
}

type envelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt string          `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type wagerData struct {
	ProviderID            string `json:"providerId"`
	ExternalTransactionID string `json:"externalTransactionId"`
	IdempotencyKey        string `json:"idempotencyKey"`
	PlayerID              string `json:"playerId"`
	WalletID              string `json:"walletId"`
	RoundID               string `json:"roundId"`
	GameID                string `json:"gameId"`
	Kind                  string `json:"kind"`
	Money                 struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"money"`
	ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
}

func (w *Workers) handle(ctx context.Context, msg types.Message) {
	body := aws.ToString(msg.Body)
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])

	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.MessageID == "" {
		w.Log.Error("invalid sqs payload", "err", err)
		return // leave for redrive / DLQ
	}
	var data wagerData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		w.Log.Error("invalid sqs data", "err", err)
		return
	}
	if !w.allowed(data.ProviderID) {
		w.Log.Error("provider not allowed", "providerId", data.ProviderID)
		return
	}
	kind, err := domain.ParseKind(data.Kind)
	if err != nil {
		w.Log.Error("kind", "err", err)
		return
	}
	player, err := uuid.Parse(data.PlayerID)
	if err != nil {
		return
	}
	wallet, err := uuid.Parse(data.WalletID)
	if err != nil {
		return
	}
	money, err := domain.ParseMoney(data.Money.Amount, data.Money.Currency)
	if err != nil {
		w.Log.Error("money", "err", err)
		return
	}
	_, err = w.Svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID:                     data.ProviderID,
		ExternalTransactionID:          data.ExternalTransactionID,
		IdempotencyKey:                 data.IdempotencyKey,
		PlayerID:                       player,
		WalletID:                       wallet,
		RoundID:                        data.RoundID,
		GameID:                         data.GameID,
		Kind:                           kind,
		Money:                          money,
		ReferenceExternalTransactionID: data.ReferenceExternalTransactionID,
		MessageID:                      env.MessageID,
		MessageDigest:                  digest,
	})
	if err != nil {
		w.Log.Error("process from sqs", "err", err, "messageId", env.MessageID)
		return
	}
	_, err = w.Client.SQS.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.Client.IngressQueueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		w.Log.Error("delete message", "err", err)
	}
}

func (w *Workers) RunPublisher(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rows, err := w.Store.ClaimOutbox(ctx, 20)
			if err != nil {
				w.Log.Error("outbox claim", "err", err)
				continue
			}
			for _, row := range rows {
				var payload map[string]any
				_ = json.Unmarshal(row.Payload, &payload)
				group := fmt.Sprint(payload["aggregateId"])
				if group == "" {
					group = row.EventID.String()
				}
				_, err := w.Client.SQS.SendMessage(ctx, &sqs.SendMessageInput{
					QueueUrl:               aws.String(w.Client.EventQueueURL),
					MessageBody:            aws.String(string(row.Payload)),
					MessageGroupId:         aws.String(strings.ReplaceAll(group, "-", "")),
					MessageDeduplicationId: aws.String(row.EventID.String()),
				})
				if err != nil {
					w.Log.Error("outbox publish", "err", err, "eventId", row.EventID)
					continue
				}
				if err := w.Store.MarkOutboxPublished(ctx, row.EventID); err != nil {
					w.Log.Error("outbox mark", "err", err)
				}
			}
		}
	}
}

func (w *Workers) RunReferences(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rows, err := w.Store.ListPendingReferences(ctx, 20)
			if err != nil {
				w.Log.Error("pending refs", "err", err)
				continue
			}
			for _, row := range rows {
				if err := w.Svc.ResumePending(ctx, row.ID); err != nil {
					w.Log.Error("resume pending", "err", err, "id", row.ID)
				}
			}
		}
	}
}
