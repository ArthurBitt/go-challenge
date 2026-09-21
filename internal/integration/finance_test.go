package integration

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"

	"junglego/internal/domain"
	"junglego/internal/postgres"
	"junglego/internal/service"
)

func setup(t *testing.T) (*service.Service, context.Context) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	store, err := postgres.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return service.New(store, slog.Default()), ctx
}

func open(t *testing.T, svc *service.Service, ctx context.Context, amount string) service.WalletView {
	t.Helper()
	m, err := domain.ParseMoney(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.OpenWallet(ctx, service.OpenWalletInput{PlayerID: uuid.Must(uuid.NewV7()), InitialBalance: m})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestFiftyIdenticalBetsDebitOnce(t *testing.T) {
	svc, ctx := setup(t)
	w := open(t, svc, ctx, "1000.00")
	money, _ := domain.ParseMoney("25.00", "BRL")
	key := uuid.Must(uuid.NewV7()).String()
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ProcessWager(ctx, service.ProcessInput{
				ProviderID:            "provider-a",
				ExternalTransactionID: key,
				IdempotencyKey:        key,
				PlayerID:              w.PlayerID,
				WalletID:              w.ID,
				RoundID:               "r1",
				GameID:                "g1",
				Kind:                  domain.KindBet,
				Money:                 money,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance.AmountString() != "975.00" {
		t.Fatalf("balance %s", got.Balance.AmountString())
	}
	n, err := svc.Store.CountLedgerByWallet(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 { // opening + one bet
		t.Fatalf("ledger entries %d", n)
	}
}

func TestTwoBetsEightyOnHundred(t *testing.T) {
	svc, ctx := setup(t)
	w := open(t, svc, ctx, "100.00")
	money, _ := domain.ParseMoney("80.00", "BRL")
	prefix := uuid.Must(uuid.NewV7()).String()
	var wg sync.WaitGroup
	type res struct {
		r   service.ProcessResult
		err error
	}
	ch := make(chan res, 2)
	for i, key := range []string{prefix + "-a", prefix + "-b"} {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			r, err := svc.ProcessWager(ctx, service.ProcessInput{
				ProviderID:            "provider-a",
				ExternalTransactionID: key,
				IdempotencyKey:        key,
				PlayerID:              w.PlayerID,
				WalletID:              w.ID,
				RoundID:               "r1",
				GameID:                "g1",
				Kind:                  domain.KindBet,
				Money:                 money,
			})
			ch <- res{r, err}
		}(i, key)
	}
	wg.Wait()
	close(ch)
	var processed, rejected int
	for r := range ch {
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch r.r.Status {
		case domain.StatusProcessed:
			processed++
		case domain.StatusRejected:
			rejected++
			if r.r.FailureCode != domain.FailureInsufficientFunds {
				t.Fatalf("code %s", r.r.FailureCode)
			}
		}
	}
	if processed != 1 || rejected != 1 {
		t.Fatalf("processed=%d rejected=%d", processed, rejected)
	}
	got, err := svc.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance.AmountString() != "20.00" {
		t.Fatalf("balance %s", got.Balance.AmountString())
	}
	rec, err := svc.Reconcile(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Consistent {
		t.Fatalf("not consistent %+v", rec)
	}
}

func TestReplayKeepsOriginalBalance(t *testing.T) {
	svc, ctx := setup(t)
	w := open(t, svc, ctx, "100.00")
	bet, _ := domain.ParseMoney("10.00", "BRL")
	key := uuid.Must(uuid.NewV7()).String()
	key2 := uuid.Must(uuid.NewV7()).String()
	first, err := svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID: "provider-a", ExternalTransactionID: key, IdempotencyKey: key,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: bet,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID: "provider-a", ExternalTransactionID: key2, IdempotencyKey: key2,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: bet,
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID: "provider-a", ExternalTransactionID: key, IdempotencyKey: key,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: bet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("expected replay")
	}
	if replay.Balance.AmountString() != first.Balance.AmountString() {
		t.Fatalf("replay balance %s vs %s", replay.Balance.AmountString(), first.Balance.AmountString())
	}
}

func TestSameOperationHTTPThenSQS(t *testing.T) {
	svc, ctx := setup(t)
	w := open(t, svc, ctx, "100.00")
	bet, _ := domain.ParseMoney("10.00", "BRL")
	key := uuid.Must(uuid.NewV7()).String()
	msg := uuid.Must(uuid.NewV7()).String()
	first, err := svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID: "provider-a", ExternalTransactionID: key, IdempotencyKey: key,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: bet,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ProcessWager(ctx, service.ProcessInput{
		ProviderID: "provider-a", ExternalTransactionID: key, IdempotencyKey: key,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "r", GameID: "g", Kind: domain.KindBet, Money: bet,
		MessageID: msg, MessageDigest: "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.IdempotentReplay {
		t.Fatal("sqs path should replay")
	}
	got, err := svc.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance.AmountString() != first.Balance.AmountString() {
		t.Fatalf("moved twice: %s vs %s", got.Balance.AmountString(), first.Balance.AmountString())
	}
}
