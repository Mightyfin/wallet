package financial

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrInsufficientBalance = errors.New("insufficient available balance")
)

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type LegalEntity struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CountryCode  string `json:"country_code"`
	BaseCurrency string `json:"base_currency"`
	Status       string `json:"status"`
}

type Wallet struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	TenantID      string `json:"tenant_id"`
	OwnerType     string `json:"owner_type"`
	OwnerID       string `json:"owner_id"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
}

type Balance struct {
	WalletID, Currency      string
	Ledger, Held, Available decimal.Decimal
}

type Transfer struct {
	LegalEntityID, TenantID, SourceWalletID, DestinationWalletID  string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem string
	ExternalReference                                             string
}

type Transaction struct {
	ID, Status, Currency, IdempotencyKey, CorrelationID string
	Amount                                              decimal.Decimal
}

type LoanDisbursement struct {
	LegalEntityID, TenantID, DestinationWalletID, FacilityID, DisbursementID string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem            string
}

func (s *Service) CreateLegalEntity(ctx context.Context, name, country, currency string) (LegalEntity, error) {
	country, currency = strings.ToUpper(strings.TrimSpace(country)), strings.ToUpper(strings.TrimSpace(currency))
	if strings.TrimSpace(name) == "" || len(country) != 2 || len(currency) != 3 {
		return LegalEntity{}, fmt.Errorf("invalid legal entity: %w", ErrConflict)
	}
	entity := LegalEntity{ID: newID("le"), Name: strings.TrimSpace(name), CountryCode: country, BaseCurrency: currency, Status: "active"}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LegalEntity{}, err
	}
	defer tx.Rollback(ctx)
	var entityUUID string
	err = tx.QueryRow(ctx, `INSERT INTO legal_entities(public_id,name,country_code,base_currency,status) VALUES($1,$2,$3,$4,$5) RETURNING id::text`,
		entity.ID, entity.Name, entity.CountryCode, entity.BaseCurrency, entity.Status).Scan(&entityUUID)
	if err != nil {
		return LegalEntity{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status) VALUES
		($1,$2,$3,'asset','loan_receivable','debit',$4,'active'),
		($5,$2,$6,'asset','bank_clearing','debit',$4,'active')`,
		newID("acc"), entityUUID, "LOAN_RECEIVABLE:"+currency, currency, newID("acc"), "BANK_CLEARING:"+currency)
	if err != nil {
		return LegalEntity{}, err
	}
	return entity, tx.Commit(ctx)
}

func (s *Service) CreateWallet(ctx context.Context, wallet Wallet) (Wallet, error) {
	wallet.ID, wallet.Currency, wallet.Status = newID("wal"), strings.ToUpper(strings.TrimSpace(wallet.Currency)), "active"
	if wallet.LegalEntityID == "" || wallet.TenantID == "" || wallet.OwnerID == "" || len(wallet.Currency) != 3 {
		return Wallet{}, fmt.Errorf("invalid wallet: %w", ErrConflict)
	}
	switch wallet.OwnerType {
	case "customer", "organization", "partner", "system":
	default:
		return Wallet{}, fmt.Errorf("invalid owner type: %w", ErrConflict)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Wallet{}, err
	}
	defer tx.Rollback(ctx)
	var entityUUID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM legal_entities WHERE public_id=$1 AND status='active'`, wallet.LegalEntityID).Scan(&entityUUID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Wallet{}, ErrNotFound
		}
		return Wallet{}, err
	}
	var walletUUID string
	err = tx.QueryRow(ctx, `INSERT INTO wallets(public_id,legal_entity_id,tenant_id,owner_type,owner_id,currency,status)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, wallet.ID, entityUUID, wallet.TenantID, wallet.OwnerType, wallet.OwnerID, wallet.Currency, wallet.Status).Scan(&walletUUID)
	if err != nil {
		return Wallet{}, err
	}
	accountID := newID("acc")
	_, err = tx.Exec(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,wallet_id,account_code,account_class,account_purpose,normal_side,currency,status)
		VALUES($1,$2,$3,$4,'liability','wallet_available','credit',$5,'active')`, accountID, entityUUID, walletUUID, "WALLET:"+wallet.ID, wallet.Currency)
	if err != nil {
		return Wallet{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload)
		VALUES($1,'wallet.created','wallet',$2::varchar,jsonb_build_object('wallet_id',$2::varchar,'tenant_id',$3::text))`, newID("evt"), wallet.ID, wallet.TenantID)
	if err != nil {
		return Wallet{}, err
	}
	return wallet, tx.Commit(ctx)
}

func (s *Service) GetBalance(ctx context.Context, legalEntityID, tenantID, walletID string) (Balance, error) {
	var result Balance
	var ledger, held string
	err := s.pool.QueryRow(ctx, `SELECT w.public_id,w.currency,
		COALESCE(SUM(CASE WHEN e.side=a.normal_side THEN e.amount ELSE -e.amount END),0)::text,
		COALESCE((SELECT SUM(h.amount) FROM balance_holds h WHERE h.wallet_id=w.id AND h.status='active' AND (h.expires_at IS NULL OR h.expires_at>now())),0)::text
		FROM wallets w JOIN legal_entities le ON le.id=w.legal_entity_id JOIN ledger_accounts a ON a.wallet_id=w.id LEFT JOIN journal_entries e ON e.account_id=a.id
		WHERE w.public_id=$1 AND le.public_id=$2 AND w.tenant_id=$3 GROUP BY w.id,w.public_id,w.currency`, walletID, legalEntityID, tenantID).Scan(&result.WalletID, &result.Currency, &ledger, &held)
	if errors.Is(err, pgx.ErrNoRows) {
		return Balance{}, ErrNotFound
	}
	if err != nil {
		return Balance{}, err
	}
	result.Ledger, _ = decimal.NewFromString(ledger)
	result.Held, _ = decimal.NewFromString(held)
	result.Available = result.Ledger.Sub(result.Held)
	return result, nil
}

func (s *Service) Transfer(ctx context.Context, in Transfer) (Transaction, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Transaction{}, fmt.Errorf("invalid amount: %w", ErrConflict)
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.SourceWalletID == in.DestinationWalletID || in.LegalEntityID == "" || in.TenantID == "" || in.IdempotencyKey == "" || len(in.Currency) != 3 {
		return Transaction{}, fmt.Errorf("invalid transfer: %w", ErrConflict)
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "wallet-api"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.SourceWalletID, in.DestinationWalletID, amount.StringFixed(2), in.Currency)
	var existing Transaction
	var existingHash, existingAmount string
	err = tx.QueryRow(ctx, `SELECT public_id,status,currency,idempotency_key,correlation_id,request_hash,
		(SELECT COALESCE(SUM(amount),0)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit')
		FROM journal_transactions jt WHERE legal_entity_id=(SELECT id FROM legal_entities WHERE public_id=$1) AND tenant_id=$2 AND source_system=$3 AND idempotency_key=$4`,
		in.LegalEntityID, in.TenantID, in.SourceSystem, in.IdempotencyKey).Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &existingAmount)
	if err == nil {
		if existingHash != requestHash {
			return Transaction{}, ErrConflict
		}
		existing.Amount, _ = decimal.NewFromString(existingAmount)
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	ids := []string{in.SourceWalletID, in.DestinationWalletID}
	sort.Strings(ids)
	type account struct{ wallet, account, entity, currency string }
	accounts := map[string]account{}
	rows, err := tx.Query(ctx, `SELECT w.public_id,a.id::text,w.legal_entity_id::text,w.currency
		FROM wallets w JOIN legal_entities le ON le.id=w.legal_entity_id
		JOIN ledger_accounts a ON a.wallet_id=w.id AND a.account_purpose='wallet_available'
		WHERE w.public_id=ANY($1) AND le.public_id=$2 AND w.tenant_id=$3 AND w.status='active' AND le.status='active'
		ORDER BY w.public_id FOR UPDATE OF w,a`, ids, in.LegalEntityID, in.TenantID)
	if err != nil {
		return Transaction{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a account
		if err = rows.Scan(&a.wallet, &a.account, &a.entity, &a.currency); err != nil {
			return Transaction{}, err
		}
		accounts[a.wallet] = a
	}
	if err = rows.Err(); err != nil {
		return Transaction{}, err
	}
	if len(accounts) != 2 {
		return Transaction{}, ErrNotFound
	}
	src, dst := accounts[in.SourceWalletID], accounts[in.DestinationWalletID]
	if src.entity != dst.entity || src.currency != in.Currency || dst.currency != in.Currency {
		return Transaction{}, ErrConflict
	}
	var entityUUID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM legal_entities WHERE public_id=$1 AND id=$2 AND status='active'`, in.LegalEntityID, src.entity).Scan(&entityUUID); err != nil {
		return Transaction{}, ErrNotFound
	}
	var availableText string
	err = tx.QueryRow(ctx, `SELECT (COALESCE(SUM(CASE WHEN e.side=a.normal_side THEN e.amount ELSE -e.amount END),0)-COALESCE((SELECT SUM(h.amount) FROM balance_holds h JOIN wallets hw ON hw.id=h.wallet_id WHERE hw.public_id=$1 AND h.status='active' AND (h.expires_at IS NULL OR h.expires_at>now())),0))::text FROM ledger_accounts a LEFT JOIN journal_entries e ON e.account_id=a.id WHERE a.id=$2 GROUP BY a.id`, in.SourceWalletID, src.account).Scan(&availableText)
	if err != nil {
		return Transaction{}, err
	}
	available, _ := decimal.NewFromString(availableText)
	if available.LessThan(amount) {
		return Transaction{}, ErrInsufficientBalance
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at) VALUES($1,$2,$3,'wallet_transfer','wallet.internal_transfer','posted',$4,NULLIF($5,''),$6,$7,$8,$9,$10) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.ExternalReference, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC()).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, src.account, amount.StringFixed(2), in.Currency, newID("ent"), dst.account)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.transfer.posted','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'source_wallet_id',$3::text,'destination_wallet_id',$4::text,'amount',$5::text,'currency',$6::text))`, newID("evt"), result.ID, in.SourceWalletID, in.DestinationWalletID, amount.StringFixed(2), in.Currency)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}

// DisburseLoan records an already-approved facility as a MightyFin receivable
// and an equal wallet liability. It does not perform underwriting or approval.
func (s *Service) DisburseLoan(ctx context.Context, in LoanDisbursement) (Transaction, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Transaction{}, fmt.Errorf("invalid amount: %w", ErrConflict)
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.DestinationWalletID == "" || in.FacilityID == "" || in.DisbursementID == "" || in.IdempotencyKey == "" || len(in.Currency) != 3 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "lending-service"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.DestinationWalletID, in.FacilityID, in.DisbursementID, amount.StringFixed(2), in.Currency)
	var existing Transaction
	var existingHash, existingAmount string
	err = tx.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,
		(SELECT COALESCE(SUM(amount),0)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit')
		FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id
		WHERE le.public_id=$1 AND jt.tenant_id=$2 AND jt.source_system=$3 AND jt.idempotency_key=$4`,
		in.LegalEntityID, in.TenantID, in.SourceSystem, in.IdempotencyKey).Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &existingAmount)
	if err == nil {
		if existingHash != requestHash {
			return Transaction{}, ErrConflict
		}
		existing.Amount, _ = decimal.NewFromString(existingAmount)
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	var entityUUID, walletAccount, receivableAccount, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT le.id::text,wa.id::text,ra.id::text,w.currency
		FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id
		JOIN ledger_accounts wa ON wa.wallet_id=w.id AND wa.account_purpose='wallet_available'
		JOIN ledger_accounts ra ON ra.legal_entity_id=le.id AND ra.account_purpose='loan_receivable' AND ra.currency=w.currency
		WHERE le.public_id=$1 AND w.public_id=$2 AND w.tenant_id=$3 AND le.status='active' AND w.status='active' FOR UPDATE OF w,wa,ra`,
		in.LegalEntityID, in.DestinationWalletID, in.TenantID).Scan(&entityUUID, &walletAccount, &receivableAccount, &walletCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if walletCurrency != in.Currency {
		return Transaction{}, ErrConflict
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata)
		VALUES($1,$2,$3,'loan_disbursement','loan.disbursement','posted',$4,$5::varchar,$6,$7,$8,$9,$10,jsonb_build_object('facility_id',$11::varchar,'disbursement_id',$5::varchar)) RETURNING id::text`,
		result.ID, entityUUID, in.TenantID, in.Currency, in.DisbursementID, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.FacilityID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES
		($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, receivableAccount, amount.StringFixed(2), in.Currency, newID("ent"), walletAccount)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload)
		VALUES($1,'loan.disbursement.posted','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'facility_id',$3::text,'disbursement_id',$4::text,'wallet_id',$5::text,'amount',$6::text,'currency',$7::text))`,
		newID("evt"), result.ID, in.FacilityID, in.DisbursementID, in.DestinationWalletID, amount.StringFixed(2), in.Currency)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}

func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func hashRequest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}
