package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsAmbiguousBodies(t *testing.T) {
	tests := []string{
		`{"amount":"1.00","unknown":true}`,
		`{"amount":"1.00"}{"amount":"2.00"}`,
		`{"amount":"1.00"} trailing`,
	}
	for _, body := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var destination struct {
			Amount string `json:"amount"`
		}
		if decodeJSON(recorder, request, &destination) || recorder.Code != 400 {
			t.Errorf("ambiguous body accepted: %q status=%d", body, recorder.Code)
		}
	}
}
