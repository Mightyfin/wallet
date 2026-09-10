package financial

import (
	"context"
	"errors"
	"testing"
)

func testLiquidityRead(t *testing.T, ctx context.Context, s *Service, in LiquidityRequest, r LiquidityReservation) {
	t.Helper()
	out, err := s.ReadLenderLiquidity(ctx, in.TenantID, in.LegalEntityID, r.ID)
	if err != nil || out.Reservation != r || out.LegalEntityID != in.LegalEntityID || !out.SourceActive || out.VerifiedAt.IsZero() {
		t.Fatal(out, err)
	}
	for _, keys := range [][3]string{{"foreign", in.LegalEntityID, r.ID}, {in.TenantID, "foreign", r.ID}, {in.TenantID, in.LegalEntityID, "missing"}} {
		if _, err := s.ReadLenderLiquidity(ctx, keys[0], keys[1], keys[2]); !errors.Is(err, ErrNotFound) {
			t.Fatal("scope not enforced", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE ledger_accounts SET status='blocked' WHERE public_id=$1`, in.AccountID); err != nil {
		t.Fatal(err)
	}
	out, err = s.ReadLenderLiquidity(ctx, in.TenantID, in.LegalEntityID, r.ID)
	if err != nil || out.SourceActive || out.Reservation != r {
		t.Fatal("inactive source lost history or remained active", out, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE ledger_accounts SET status='active' WHERE public_id=$1`, in.AccountID); err != nil {
		t.Fatal(err)
	}
}
