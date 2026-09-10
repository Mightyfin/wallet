package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestGoodsCapacityQuery(t *testing.T) {
	for _, query := range []string{"", "legal_entity_id=", "legal_entity_id=a&legal_entity_id=b", "legal_entity_id=a&tenant_id=b", "legal_entity_id=%20a", "legal_entity_id=a&bad=%zz", "legal_entity_id=a%00"} {
		r := httptest.NewRequest("GET", "/capacity?"+query, nil)
		r.SetPathValue("authorization_id", "gca_test")
		if _, invalid := parseGoodsCapacityQuery(r); !invalid {
			t.Fatal("accepted", query)
		}
	}
	r := httptest.NewRequest("GET", "/capacity?legal_entity_id=lender", nil)
	r.SetPathValue("authorization_id", "gca_test")
	if entity, invalid := parseGoodsCapacityQuery(r); invalid || entity != "lender" {
		t.Fatal(entity, invalid)
	}
}
