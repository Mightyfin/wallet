package financial

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestLoanFundingAndTransfer(t *testing.T) {
	databaseURL := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("WALLET_LEDGER_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	tenant := fmt.Sprintf("tenant-a-%d", time.Now().UnixNano())
	entity, err := s.CreateLegalEntity(ctx, "MightyFin Zambia Test", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: tenant, OwnerType: "customer", OwnerID: "customer-a", Currency: "ZMW", SourceApplication: "integration-test", IdempotencyKey: "wallet-source-001"})
	if err != nil {
		t.Fatal(err)
	}
	replayedSource, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: tenant, OwnerType: "customer", OwnerID: "customer-a", Currency: "ZMW", SourceApplication: "integration-test", IdempotencyKey: "wallet-source-001"})
	if err != nil || replayedSource.ID != source.ID {
		t.Fatalf("wallet creation replay: id=%q want=%q err=%v", replayedSource.ID, source.ID, err)
	}
	destination, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: tenant, OwnerType: "customer", OwnerID: "customer-b", Currency: "ZMW", SourceApplication: "integration-test", IdempotencyKey: "wallet-destination-001"})
	if err != nil {
		t.Fatal(err)
	}
	if source.Status != "pending" || destination.Status != "pending" {
		t.Fatal("new wallets must remain pending until eligibility approval")
	}
	source, err = s.TransitionWallet(ctx, WalletLifecycleRequest{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, TargetStatus: "active", Reason: "KYC and product eligibility approved", EvidenceReference: "eligibility:source-1", ActorSubject: "compliance-checker", SourceApplication: "kyc-service", IdempotencyKey: "activate-source-0001"})
	if err != nil {
		t.Fatal(err)
	}
	destination, err = s.TransitionWallet(ctx, WalletLifecycleRequest{LegalEntityID: entity.ID, TenantID: tenant, WalletID: destination.ID, TargetStatus: "active", Reason: "KYC and product eligibility approved", EvidenceReference: "eligibility:destination-1", ActorSubject: "compliance-checker", SourceApplication: "kyc-service", IdempotencyKey: "activate-destination-0001"})
	if err != nil {
		t.Fatal(err)
	}
	disbursement := LoanDisbursement{LegalEntityID: entity.ID, TenantID: tenant, DestinationWalletID: source.ID, FacilityID: "facility-1", DisbursementID: "drawdown-1", Amount: "100.00", Currency: "ZMW", IdempotencyKey: "loan-0001"}
	first, err := s.DisburseLoan(ctx, disbursement)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.DisburseLoan(ctx, disbursement)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent replay failed: %#v %v", replay, err)
	}
	hold, err := s.CreateHold(ctx, HoldRequest{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, Amount: "20.00", Currency: "ZMW", Reason: "payment_authorization", IdempotencyKey: "hold-0001"})
	if err != nil {
		t.Fatal(err)
	}
	heldBalance, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || heldBalance.Available.StringFixed(2) != "80.00" || heldBalance.Held.StringFixed(2) != "20.00" {
		t.Fatalf("held balance: %#v %v", heldBalance, err)
	}
	if _, err = s.ReleaseHold(ctx, entity.ID, tenant, source.ID, hold.ID); err != nil {
		t.Fatal(err)
	}
	postedTransfer, err := s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: tenant, SourceWalletID: source.ID, DestinationWalletID: destination.ID, Amount: "30.00", Currency: "ZMW", IdempotencyKey: "transfer-1"})
	if err != nil {
		t.Fatal(err)
	}
	sourceBalance, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || sourceBalance.Available.StringFixed(2) != "70.00" {
		t.Fatalf("source balance: %#v %v", sourceBalance, err)
	}
	destinationBalance, err := s.GetBalance(ctx, entity.ID, tenant, destination.ID)
	if err != nil || destinationBalance.Available.StringFixed(2) != "30.00" {
		t.Fatalf("destination balance: %#v %v", destinationBalance, err)
	}
	repayment := WalletRepayment{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, FacilityID: "facility-1", Amount: "10.00", Currency: "ZMW", IdempotencyKey: "repay-0001"}
	postedRepayment, err := s.RepayLoanFromWallet(ctx, repayment)
	if err != nil {
		t.Fatal(err)
	}
	replayedRepayment, err := s.RepayLoanFromWallet(ctx, repayment)
	if err != nil || postedRepayment.ID != replayedRepayment.ID {
		t.Fatalf("repayment replay: %#v %v", replayedRepayment, err)
	}
	repaidBalance, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || repaidBalance.Available.StringFixed(2) != "60.00" {
		t.Fatalf("repaid balance: %#v %v", repaidBalance, err)
	}
	external := SettledExternalRepayment{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, FacilityID: "facility-1", SettlementReference: "bank-settlement-1", EvidenceID: "evidence-1", Amount: "5.00", Currency: "ZMW", IdempotencyKey: "external-repay-0001"}
	externalPosted, err := s.RecordSettledExternalRepayment(ctx, external)
	if err != nil {
		t.Fatal(err)
	}
	externalReplay, err := s.RecordSettledExternalRepayment(ctx, external)
	if err != nil || externalPosted.ID != externalReplay.ID {
		t.Fatalf("external replay: %#v %v", externalReplay, err)
	}
	afterExternal, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || afterExternal.Available.StringFixed(2) != "60.00" {
		t.Fatalf("external repayment changed wallet: %#v %v", afterExternal, err)
	}
	reversal := ReversalRequest{LegalEntityID: entity.ID, TenantID: tenant, TransactionID: postedTransfer.ID, Reason: "integration test correction", ApprovalReference: "approval-1", IdempotencyKey: "reversal-0001"}
	postedReversal, err := s.ReverseTransaction(ctx, reversal)
	if err != nil {
		t.Fatal(err)
	}
	replayedReversal, err := s.ReverseTransaction(ctx, reversal)
	if err != nil || postedReversal.ID != replayedReversal.ID {
		t.Fatalf("reversal replay: %#v %v", replayedReversal, err)
	}
	reversedSource, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || reversedSource.Available.StringFixed(2) != "90.00" {
		t.Fatalf("reversed source: %#v %v", reversedSource, err)
	}
	deposit := SettledDeposit{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, SettlementReference: "bank-deposit-1", EvidenceID: "deposit-evidence-1", Amount: "7.00", Currency: "ZMW", IdempotencyKey: "deposit-0001"}
	postedDeposit, err := s.RecordSettledDeposit(ctx, deposit)
	if err != nil {
		t.Fatal(err)
	}
	replayedDeposit, err := s.RecordSettledDeposit(ctx, deposit)
	if err != nil || postedDeposit.ID != replayedDeposit.ID {
		t.Fatalf("deposit replay: %#v %v", replayedDeposit, err)
	}
	depositedBalance, err := s.GetBalance(ctx, entity.ID, tenant, source.ID)
	if err != nil || depositedBalance.Available.StringFixed(2) != "97.00" {
		t.Fatalf("deposited balance: %#v %v", depositedBalance, err)
	}
	_, err = s.TransitionWallet(ctx, WalletLifecycleRequest{LegalEntityID: entity.ID, TenantID: tenant, WalletID: source.ID, TargetStatus: "closed", Reason: "Customer requested account closure", EvidenceReference: "closure-request:source-1", ActorSubject: "operations-checker", SourceApplication: "wallet-operations", IdempotencyKey: "close-source-0001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("non-zero wallet closure must fail, got %v", err)
	}
	_, err = s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: tenant, SourceWalletID: source.ID, DestinationWalletID: destination.ID, Amount: "1000.00", Currency: "ZMW", IdempotencyKey: "transfer-2"})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("expected insufficient balance, got %v", err)
	}
}

func TestConcurrentTransfersPreserveBalanceAndIdempotency(t *testing.T) {
	databaseURL := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("WALLET_LEDGER_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	s := New(pool)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "tenant-concurrency-" + suffix
	entity, err := s.CreateLegalEntity(ctx, "Concurrency Test "+suffix, "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	createActiveWallet := func(owner, key string) Wallet {
		wallet, createErr := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: tenant, OwnerType: "customer", OwnerID: owner, Currency: "ZMW", SourceApplication: "concurrency-test", IdempotencyKey: key})
		if createErr != nil {
			t.Fatal(createErr)
		}
		wallet, createErr = s.TransitionWallet(ctx, WalletLifecycleRequest{LegalEntityID: entity.ID, TenantID: tenant, WalletID: wallet.ID, TargetStatus: "active", Reason: "Synthetic concurrency eligibility approval", EvidenceReference: "edge-audit:" + owner, ActorSubject: "edge-auditor", SourceApplication: "concurrency-test", IdempotencyKey: "activate-" + key})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return wallet
	}

	source := createActiveWallet("source-"+suffix, "create-source-"+suffix)
	destination := createActiveWallet("destination-"+suffix, "create-destination-"+suffix)
	if _, err = s.DisburseLoan(ctx, LoanDisbursement{LegalEntityID: entity.ID, TenantID: tenant, DestinationWalletID: source.ID, FacilityID: "facility-" + suffix, DisbursementID: "drawdown-" + suffix, Amount: "100.00", Currency: "ZMW", IdempotencyKey: "fund-source-" + suffix}); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	for index := range 2 {
		go func(index int) {
			_, transferErr := s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: tenant, SourceWalletID: source.ID, DestinationWalletID: destination.ID, Amount: "80.00", Currency: "ZMW", IdempotencyKey: fmt.Sprintf("competing-%d-%s", index, suffix)})
			results <- transferErr
		}(index)
	}
	successes, insufficient := 0, 0
	for range 2 {
		switch transferErr := <-results; {
		case transferErr == nil:
			successes++
		case errors.Is(transferErr, ErrInsufficientBalance):
			insufficient++
		default:
			t.Fatalf("unexpected competing transfer error: %v", transferErr)
		}
	}
	if successes != 1 || insufficient != 1 {
		t.Fatalf("competing transfers: successes=%d insufficient=%d", successes, insufficient)
	}
	assertBalances(t, ctx, s, entity.ID, tenant, source.ID, destination.ID, "20.00", "80.00")

	retrySource := createActiveWallet("retry-source-"+suffix, "create-retry-source-"+suffix)
	retryDestination := createActiveWallet("retry-destination-"+suffix, "create-retry-destination-"+suffix)
	if _, err = s.DisburseLoan(ctx, LoanDisbursement{LegalEntityID: entity.ID, TenantID: tenant, DestinationWalletID: retrySource.ID, FacilityID: "retry-facility-" + suffix, DisbursementID: "retry-drawdown-" + suffix, Amount: "50.00", Currency: "ZMW", IdempotencyKey: "fund-retry-source-" + suffix}); err != nil {
		t.Fatal(err)
	}
	type transferResult struct {
		transaction Transaction
		err         error
	}
	replays := make(chan transferResult, 2)
	for range 2 {
		go func() {
			transaction, transferErr := s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: tenant, SourceWalletID: retrySource.ID, DestinationWalletID: retryDestination.ID, Amount: "10.00", Currency: "ZMW", IdempotencyKey: "simultaneous-retry-" + suffix})
			replays <- transferResult{transaction: transaction, err: transferErr}
		}()
	}
	first, second := <-replays, <-replays
	if first.err != nil || second.err != nil || first.transaction.ID == "" || first.transaction.ID != second.transaction.ID {
		t.Fatalf("simultaneous idempotent retry: first=%+v second=%+v", first, second)
	}
	assertBalances(t, ctx, s, entity.ID, tenant, retrySource.ID, retryDestination.ID, "40.00", "10.00")
}

func assertBalances(t *testing.T, ctx context.Context, service *Service, entityID, tenant, sourceID, destinationID, expectedSource, expectedDestination string) {
	t.Helper()
	source, err := service.GetBalance(ctx, entityID, tenant, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := service.GetBalance(ctx, entityID, tenant, destinationID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Available.StringFixed(2) != expectedSource || destination.Available.StringFixed(2) != expectedDestination {
		t.Fatalf("balances source=%s destination=%s", source.Available.StringFixed(2), destination.Available.StringFixed(2))
	}
}
