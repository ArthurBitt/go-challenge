package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort             string
	DatabaseURL          string
	OIDCIssuerURL        string
	OIDCAudience         string
	AWSRegion            string
	SQSEndpoint          string
	IngressQueueURL      string
	EventQueueURL        string
	AllowedProviders     []string
	ReferenceMaxAttempts int
}

func Load() (Config, error) {
	c := Config{
		HTTPPort:             getenv("HTTP_PORT", "8080"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		OIDCIssuerURL:        os.Getenv("OIDC_ISSUER_URL"),
		OIDCAudience:         getenv("OIDC_AUDIENCE", "wager-api"),
		AWSRegion:            getenv("AWS_REGION", "us-east-1"),
		SQSEndpoint:          os.Getenv("SQS_ENDPOINT"),
		IngressQueueURL:      os.Getenv("SQS_INGRESS_QUEUE_URL"),
		EventQueueURL:        os.Getenv("SQS_EVENT_QUEUE_URL"),
		AllowedProviders:     split(getenv("SQS_ALLOWED_PROVIDERS", "provider-a,provider-b")),
		ReferenceMaxAttempts: atoi(getenv("REFERENCE_MAX_ATTEMPTS", "10"), 10),
	}
	if c.DatabaseURL == "" || c.OIDCIssuerURL == "" || c.IngressQueueURL == "" || c.EventQueueURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL, OIDC_ISSUER_URL, SQS_INGRESS_QUEUE_URL and SQS_EVENT_QUEUE_URL are required")
	}
	return c, nil
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoi(s string, d int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}
