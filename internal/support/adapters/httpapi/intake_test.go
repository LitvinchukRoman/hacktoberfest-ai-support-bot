package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityToken(t *testing.T) {
	cases := map[string]string{
		"":                               "",
		"short":                          "",
		"olena-support-token-0123456789": "olena-support-token-0123456789",
		"eyJ1IjoxfQ.c2lnbmF0dXJl_-+/=~":  "eyJ1IjoxfQ.c2lnbmF0dXJl_-+/=~",
		"has spaces in the token value":  "",
		"newline\r\nX-Injected: 1 token": "",
		strings.Repeat("a", 1024):        strings.Repeat("a", 1024),
		strings.Repeat("a", 1025):        "",
	}
	for header, want := range cases {
		r := httptest.NewRequest("POST", "/api/support/messages", nil)
		r.Header.Set(IdentityHeader, header)
		if got := identityToken(r); got != want {
			t.Errorf("header %.40q: got %.40q, want %.40q", header, got, want)
		}
	}
}
