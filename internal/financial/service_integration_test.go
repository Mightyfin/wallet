package financial

import (
	"context"
	"errors"
	"os"
	"testing"

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
	entity, err := s.CreateLegalEntity(ctx, "MightyFin Zambia Test", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: "tenant-a", OwnerType: "customer", OwnerID: "customer-a", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: "tenant-a", OwnerType: "customer", OwnerID: "customer-b", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	disbursement := LoanDisbursement{LegalEntityID: entity.ID, TenantID: "tenant-a", DestinationWalletID: source.ID, FacilityID: "facility-1", DisbursementID: "drawdown-1", Amount: "100.00", Currency: "ZMW", IdempotencyKey: "loan-0001"}
	first, err := s.DisburseLoan(ctx, disbursement)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.DisburseLoan(ctx, disbursement)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent replay failed: %#v %v", replay, err)
	}
	hold, err := s.CreateHold(ctx, HoldRequest{LegalEntityID: entity.ID, TenantID: "tenant-a", WalletID: source.ID, Amount: "20.00", Currency: "ZMW", Reason: "payment_authorization", IdempotencyKey: "hold-0001"})
	if err != nil {
		t.Fatal(err)
	}
	heldBalance, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || heldBalance.Available.StringFixed(2) != "80.00" || heldBalance.Held.StringFixed(2) != "20.00" {
		t.Fatalf("held balance: %#v %v", heldBalance, err)
	}
	if _, err = s.ReleaseHold(ctx, entity.ID, "tenant-a", source.ID, hold.ID); err != nil {
		t.Fatal(err)
	}
	postedTransfer, err := s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: "tenant-a", SourceWalletID: source.ID, DestinationWalletID: destination.ID, Amount: "30.00", Currency: "ZMW", IdempotencyKey: "transfer-1"})
	if err != nil {
		t.Fatal(err)
	}
	sourceBalance, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || sourceBalance.Available.StringFixed(2) != "70.00" {
		t.Fatalf("source balance: %#v %v", sourceBalance, err)
	}
	destinationBalance, err := s.GetBalance(ctx, entity.ID, "tenant-a", destination.ID)
	if err != nil || destinationBalance.Available.StringFixed(2) != "30.00" {
		t.Fatalf("destination balance: %#v %v", destinationBalance, err)
	}
	repayment := WalletRepayment{LegalEntityID: entity.ID, TenantID: "tenant-a", WalletID: source.ID, FacilityID: "facility-1", Amount: "10.00", Currency: "ZMW", IdempotencyKey: "repay-0001"}
	postedRepayment, err := s.RepayLoanFromWallet(ctx, repayment)
	if err != nil {
		t.Fatal(err)
	}
	replayedRepayment, err := s.RepayLoanFromWallet(ctx, repayment)
	if err != nil || postedRepayment.ID != replayedRepayment.ID {
		t.Fatalf("repayment replay: %#v %v", replayedRepayment, err)
	}
	repaidBalance, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || repaidBalance.Available.StringFixed(2) != "60.00" {
		t.Fatalf("repaid balance: %#v %v", repaidBalance, err)
	}
	external := SettledExternalRepayment{LegalEntityID: entity.ID, TenantID: "tenant-a", WalletID: source.ID, FacilityID: "facility-1", SettlementReference: "bank-settlement-1", EvidenceID: "evidence-1", Amount: "5.00", Currency: "ZMW", IdempotencyKey: "external-repay-0001"}
	externalPosted, err := s.RecordSettledExternalRepayment(ctx, external)
	if err != nil {
		t.Fatal(err)
	}
	externalReplay, err := s.RecordSettledExternalRepayment(ctx, external)
	if err != nil || externalPosted.ID != externalReplay.ID {
		t.Fatalf("external replay: %#v %v", externalReplay, err)
	}
	afterExternal, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || afterExternal.Available.StringFixed(2) != "60.00" {
		t.Fatalf("external repayment changed wallet: %#v %v", afterExternal, err)
	}
	reversal := ReversalRequest{LegalEntityID: entity.ID, TenantID: "tenant-a", TransactionID: postedTransfer.ID, Reason: "integration test correction", ApprovalReference: "approval-1", IdempotencyKey: "reversal-0001"}
	postedReversal, err := s.ReverseTransaction(ctx, reversal)
	if err != nil {
		t.Fatal(err)
	}
	replayedReversal, err := s.ReverseTransaction(ctx, reversal)
	if err != nil || postedReversal.ID != replayedReversal.ID {
		t.Fatalf("reversal replay: %#v %v", replayedReversal, err)
	}
	reversedSource, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || reversedSource.Available.StringFixed(2) != "90.00" {
		t.Fatalf("reversed source: %#v %v", reversedSource, err)
	}
	deposit := SettledDeposit{LegalEntityID: entity.ID, TenantID: "tenant-a", WalletID: source.ID, SettlementReference: "bank-deposit-1", EvidenceID: "deposit-evidence-1", Amount: "7.00", Currency: "ZMW", IdempotencyKey: "deposit-0001"}
	postedDeposit, err := s.RecordSettledDeposit(ctx, deposit)
	if err != nil {
		t.Fatal(err)
	}
	replayedDeposit, err := s.RecordSettledDeposit(ctx, deposit)
	if err != nil || postedDeposit.ID != replayedDeposit.ID {
		t.Fatalf("deposit replay: %#v %v", replayedDeposit, err)
	}
	depositedBalance, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || depositedBalance.Available.StringFixed(2) != "97.00" {
		t.Fatalf("deposited balance: %#v %v", depositedBalance, err)
	}
	withdrawalHold, err := s.CreateHold(ctx, HoldRequest{TenantID: "tenant-a", WalletID: source.ID, Amount: "12.00", Currency: "ZMW", Reason: "external_withdrawal", IdempotencyKey: "withdraw-hold-0001"})
	if err != nil {
		t.Fatal(err)
	}
	withdrawal := SettledWithdrawal{TenantID: "tenant-a", WalletID: source.ID, HoldID: withdrawalHold.ID, SettlementReference: "bank-withdrawal-1", EvidenceID: "withdrawal-evidence-1", Amount: "12.00", Currency: "ZMW", IdempotencyKey: "withdraw-settle-0001"}
	postedWithdrawal, err := s.RecordSettledWithdrawal(ctx, withdrawal)
	if err != nil {
		t.Fatal(err)
	}
	replayedWithdrawal, err := s.RecordSettledWithdrawal(ctx, withdrawal)
	if err != nil || postedWithdrawal.ID != replayedWithdrawal.ID {
		t.Fatalf("withdrawal replay: %#v %v", replayedWithdrawal, err)
	}
	afterWithdrawal, err := s.GetBalance(ctx, entity.ID, "tenant-a", source.ID)
	if err != nil || afterWithdrawal.Available.StringFixed(2) != "85.00" || !afterWithdrawal.Held.IsZero() {
		t.Fatalf("withdrawal balance: %#v %v", afterWithdrawal, err)
	}
	_, err = s.Transfer(ctx, Transfer{LegalEntityID: entity.ID, TenantID: "tenant-a", SourceWalletID: source.ID, DestinationWalletID: destination.ID, Amount: "1000.00", Currency: "ZMW", IdempotencyKey: "transfer-2"})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("expected insufficient balance, got %v", err)
	}
}
