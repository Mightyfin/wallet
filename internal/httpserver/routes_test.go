package httpserver

import (
	"net/http"
	"testing"
)

func TestAPIRoutesRegisterWithoutConflict(t *testing.T) {
	(&api{}).routes(http.NewServeMux())
}
