package financial

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// GoodsAuthorization is an immutable execution instruction, not a credit decision.
// Only dedicated internal workloads may register and execute this instruction.
type GoodsAuthorization struct {
	ID, TenantID, ReservationID, BorrowerPartyID, SupplierPartyID, DestinationWalletID, OrderReference, Amount, Currency, AuthorizedBy string
	AllowPartialUse                                                                                                                    bool
	ExpiresAt                                                                                                                          time.Time
}

func goodsAmount(raw string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(raw)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 || amount.Exponent() > 17 || amount.GreaterThan(decimal.RequireFromString("999999999999999999.99")) {
		return decimal.Zero, ErrConflict
	}
	return amount, nil
}

func (s *Service) RegisterGoodsAuthorization(ctx context.Context, in GoodsAuthorization) error {
	amount, err := goodsAmount(in.Amount)
	if err != nil {
		return err
	}
	for _, value := range []string{in.ID, in.TenantID, in.ReservationID, in.BorrowerPartyID, in.SupplierPartyID, in.DestinationWalletID, in.OrderReference, in.Currency, in.AuthorizedBy} {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 128 || strings.ContainsFunc(value, unicode.IsControl) {
			return ErrConflict
		}
	}
	if len(in.ID) > 64 || len(in.TenantID) > 64 || in.BorrowerPartyID == in.SupplierPartyID || in.ExpiresAt.IsZero() {
		return ErrConflict
	}
	in.ExpiresAt = in.ExpiresAt.UTC().Truncate(time.Microsecond)
	partial := "false"
	if in.AllowPartialUse {
		partial = "true"
	}
	hash := hashRequest(in.ReservationID, in.BorrowerPartyID, in.SupplierPartyID, in.DestinationWalletID, in.OrderReference, amount.StringFixed(2), in.Currency, in.AuthorizedBy, partial, in.ExpiresAt.Format(time.RFC3339Nano))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, hashRequest("goods-authorization", in.ID)); err != nil {
		return err
	}
	var oldHash, tenant string
	err = tx.QueryRow(ctx, `SELECT request_hash,tenant_id FROM goods_credit_authorizations WHERE id=$1`, in.ID).Scan(&oldHash, &tenant)
	if err == nil {
		if oldHash != hash || tenant != in.TenantID {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO goods_credit_authorizations(id,tenant_id,reservation_id,borrower_party_id,supplier_party_id,destination_wallet_id,order_reference,amount,currency,allow_partial_use,expires_at,authorized_by,request_hash)
 SELECT $1,$2::text,r.id,$4,$5,w.id,$7,$8,$9,$10,$11,$12,$13 FROM lender_liquidity_reservations r JOIN wallets w ON w.public_id=$6 WHERE r.public_id=$3 AND r.tenant_id=$2::text`, in.ID, in.TenantID, in.ReservationID, in.BorrowerPartyID, in.SupplierPartyID, in.DestinationWalletID, in.OrderReference, amount.StringFixed(2), in.Currency, in.AllowPartialUse, in.ExpiresAt, in.AuthorizedBy, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'goods.credit.authorized','goods_authorization',$2::text,jsonb_build_object('authorization_id',$2::text,'tenant_id',$3::text))`, newID("evt"), in.ID, in.TenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PostGoodsUse credits only the saved supplier, never a caller-selected wallet.
// Lender liquidity stays reserved to back the supplier liability and unused credit.
func (s *Service) PostGoodsUse(ctx context.Context, tenant, authorization, useID, amountText, actor string) (Transaction, error) {
	amount, err := goodsAmount(amountText)
	if err != nil {
		return Transaction{}, err
	}
	for _, v := range []string{tenant, authorization, useID, actor} {
		if v == "" || strings.TrimSpace(v) != v || len(v) > 128 || strings.ContainsFunc(v, unicode.IsControl) {
			return Transaction{}, ErrConflict
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, hashRequest("goods-use", tenant, useID)); err != nil {
		return Transaction{}, err
	}
	var out Transaction
	var savedAuth, savedAmount string
	err = tx.QueryRow(ctx, `SELECT j.public_id,j.status,j.currency,j.idempotency_key,j.correlation_id,u.authorization_id,u.amount::text FROM goods_credit_uses u JOIN journal_transactions j ON j.id=u.transaction_id WHERE u.tenant_id=$1 AND u.use_id=$2`, tenant, useID).Scan(&out.ID, &out.Status, &out.Currency, &out.IdempotencyKey, &out.CorrelationID, &savedAuth, &savedAmount)
	if err == nil {
		if savedAuth != authorization || savedAmount != amount.StringFixed(2) {
			return Transaction{}, ErrConflict
		}
		out.Amount = amount
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	var walletID, sourceID, entityID, facilityID, borrower, supplier, order, currency, limitText string
	var expiry time.Time
	var partial bool
	err = tx.QueryRow(ctx, `SELECT a.destination_wallet_id::text,r.account_id::text,la.legal_entity_id::text,r.facility_id,a.borrower_party_id,a.supplier_party_id,a.order_reference,a.currency,a.amount::text,a.expires_at,a.allow_partial_use FROM goods_credit_authorizations a JOIN lender_liquidity_reservations r ON r.id=a.reservation_id JOIN ledger_accounts la ON la.id=r.account_id WHERE a.id=$1 AND a.tenant_id=$2 FOR UPDATE OF a`, authorization, tenant).Scan(&walletID, &sourceID, &entityID, &facilityID, &borrower, &supplier, &order, &currency, &limitText, &expiry, &partial)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	var usedText string
	if err = tx.QueryRow(ctx, `SELECT coalesce(sum(amount),0)::text FROM goods_credit_uses WHERE authorization_id=$1`, authorization).Scan(&usedText); err != nil {
		return Transaction{}, err
	}
	limit, _ := decimal.NewFromString(limitText)
	used, _ := decimal.NewFromString(usedText)
	if amount.GreaterThan(limit.Sub(used)) || (!partial && (!used.IsZero() || !amount.Equal(limit))) {
		return Transaction{}, ErrInsufficientBalance
	}
	// Match registration's source-before-wallet lock order to avoid inversions.
	var sourceActive bool
	if err = tx.QueryRow(ctx, `SELECT status='active' AND account_purpose='lender_liquidity' FROM ledger_accounts WHERE id=$1 FOR UPDATE`, sourceID).Scan(&sourceActive); err != nil {
		return Transaction{}, err
	}
	if !sourceActive {
		return Transaction{}, ErrNotFound
	}
	// Keep wallet ownership/status stable during posting, then lock ledger accounts
	// in UUID order for different facilities paying the same supplier.
	var walletPublic string
	err = tx.QueryRow(ctx, `SELECT public_id FROM wallets WHERE id=$1 AND tenant_id=$2 AND owner_id=$3 AND owner_type<>'system' AND currency=$4 AND legal_entity_id=$5 AND status='active' FOR UPDATE`, walletID, tenant, supplier, currency, entityID).Scan(&walletPublic)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	var walletAccount, receivableAccount string
	err = tx.QueryRow(ctx, `SELECT wa.id::text,ra.id::text FROM ledger_accounts wa JOIN ledger_accounts ra ON ra.legal_entity_id=wa.legal_entity_id AND ra.currency=wa.currency AND ra.account_purpose='loan_receivable' WHERE wa.wallet_id=$1 AND wa.legal_entity_id=$2 AND wa.currency=$3 AND wa.account_purpose='wallet_available' AND wa.account_class='liability' AND wa.normal_side='credit' AND ra.account_class='asset' AND ra.normal_side='debit'`, walletID, entityID, currency).Scan(&walletAccount, &receivableAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,status FROM ledger_accounts WHERE id::text=ANY($1::text[]) ORDER BY id FOR UPDATE`, []string{sourceID, walletAccount, receivableAccount})
	if err != nil {
		return Transaction{}, err
	}
	count := 0
	active := true
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			rows.Close()
			return Transaction{}, err
		}
		count++
		active = active && status == "active"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Transaction{}, err
	}
	if count != 3 || !active {
		return Transaction{}, ErrNotFound
	}
	var entityActive bool
	if err = tx.QueryRow(ctx, `SELECT status='active' FROM legal_entities WHERE id=$1 FOR SHARE`, entityID).Scan(&entityActive); err != nil {
		return Transaction{}, err
	}
	if !entityActive {
		return Transaction{}, ErrNotFound
	}
	out = Transaction{ID: newID("txn"), Status: "posted", Currency: currency, IdempotencyKey: useID, CorrelationID: newID("cor"), Amount: amount}
	var transactionID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata) VALUES($1,$2,$3,'goods_credit_use','goods.supplier_wallet','posted',$4,$5::text,$6,$7,'goods-credit',clock_timestamp(),jsonb_build_object('facility_id',$8::text,'authorization_id',$9::text,'borrower_party_id',$10::text,'supplier_party_id',$11::text,'order_reference',$12::text,'use_id',$5::text)) RETURNING id::text`, out.ID, entityID, tenant, currency, useID, hashRequest(authorization, amount.StringFixed(2)), out.CorrelationID, facilityID, authorization, borrower, supplier, order).Scan(&transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO goods_credit_uses(tenant_id,use_id,authorization_id,transaction_id,amount,requested_by) VALUES($1,$2,$3,$4,$5,$6)`, tenant, useID, authorization, transactionID, amount.StringFixed(2), actor); err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionID, receivableAccount, amount.StringFixed(2), currency, newID("ent"), walletAccount); err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'goods.credit.use.posted','journal_transaction',$2::text,jsonb_build_object('transaction_id',$2::text,'tenant_id',$3::text,'facility_id',$4::text,'authorization_id',$5::text,'use_id',$6::text,'borrower_party_id',$7::text,'supplier_party_id',$8::text,'destination_wallet_id',$9::text,'amount',$10::text,'currency',$11::text))`, newID("evt"), out.ID, tenant, facilityID, authorization, useID, borrower, supplier, walletPublic, amount.StringFixed(2), currency); err != nil {
		return Transaction{}, err
	}
	return out, tx.Commit(ctx)
}
