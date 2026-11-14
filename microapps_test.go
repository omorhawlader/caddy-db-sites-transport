package dbsites

import (
	"database/sql"
	"net/http"
	"testing"
)

func TestMicroAppStatus(t *testing.T) {
	html := sql.NullString{String: "<html></html>", Valid: true}
	cases := []struct {
		name string
		row  microAppRow
		want int
	}{
		{"live", microAppRow{DomainStatus: "verified", Purpose: "micro_saas", AppStatus: "published", HTML: html}, http.StatusOK},
		{"root not verified", microAppRow{DomainStatus: "pending", Purpose: "micro_saas", AppStatus: "published", HTML: html}, http.StatusForbidden},
		{"root no longer micro saas", microAppRow{DomainStatus: "verified", Purpose: "funnel", AppStatus: "published", HTML: html}, http.StatusForbidden},
		{"draft app", microAppRow{DomainStatus: "verified", Purpose: "micro_saas", AppStatus: "draft"}, http.StatusLocked},
		{"unpublished app", microAppRow{DomainStatus: "verified", Purpose: "micro_saas", AppStatus: "unpublished"}, http.StatusLocked},
		{"published without html", microAppRow{DomainStatus: "verified", Purpose: "micro_saas", AppStatus: "published"}, http.StatusNotFound},
	}
	for _, c := range cases {
		if got := microAppStatus(c.row); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
