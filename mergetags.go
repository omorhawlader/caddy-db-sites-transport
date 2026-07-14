package dbsites

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)


const accountValuesSQLTemplate = `SELECT value_key, COALESCE(value_text, '') FROM %s WHERE sub_account_id = $1`

const subAccountTZSQLTemplate = `SELECT COALESCE(timezone, '') FROM %s WHERE id = $1 LIMIT 1`

const contactStandardSQLTemplate = `SELECT COALESCE(first_name, ''), COALESCE(last_name, ''), COALESCE(email, ''), COALESCE(phone, ''), COALESCE(company, ''), COALESCE(display_name, '') FROM %s WHERE id = $1 AND sub_account_id = $2 LIMIT 1`

const contactCustomSQLTemplate = `SELECT cf.field_slug, COALESCE(v.value, '') FROM %s v JOIN %s cf ON cf.id = v.field_id WHERE v.contact_id = $1 AND cf.sub_account_id = $2`

var (
	mergePillRe    = regexp.MustCompile(`(?is)<span\b[^>]*\bdata-merge-token="([^"]*)"[^>]*>.*?</span>`)
	mergePercentRe = regexp.MustCompile(`\{%\s*([^%]+?)\s*%\}`)
	mergeTokenRe   = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)
	mergePathRe    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)
	uuidRe         = regexp.MustCompile(`^[0-9a-fA-F-]{32,36}$`)
)

func htmlHasMergeTokens(html string) bool {
	return strings.Contains(html, "{{") || strings.Contains(html, "data-merge-token")
}

// normalizes {% %} to {{ }}.
func prepareHTMLForMerge(html string) string {
	html = mergePillRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := mergePillRe.FindStringSubmatch(m)
		path := strings.TrimSpace(sub[1])
		if path == "" {
			return ""
		}
		return "{{" + path + "}}"
	})
	return mergePercentRe.ReplaceAllString(html, "{{$1}}")
}


func replaceMergeTokens(html string, values map[string]string) string {
	if html == "" {
		return html
	}
	html = prepareHTMLForMerge(html)
	return mergeTokenRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := mergeTokenRe.FindStringSubmatch(m)
		key := strings.TrimSpace(sub[1])
		if v, ok := values[key]; ok {
			return v
		}
		if mergePathRe.MatchString(key) {
			return ""
		}
		return m
	})
}


func (h *Handler) buildMergeValues(ctx context.Context, subAccountID, contactID string) map[string]string {
	values := make(map[string]string)
	if subAccountID == "" {
		// No tenant context: still resolve dynamic time.* so they never render blank.
		overlayTimeValues(values, time.Now().UTC())
		return values
	}

	rows, err := h.pool.Query(ctx, h.accountValuesQuery, subAccountID)
	if err != nil {
		h.logger.Warn("db_sites account values query failed", zap.String("sub_account_id", subAccountID), zap.Error(err))
	} else {
		for rows.Next() {
			var key, val string
			if scanErr := rows.Scan(&key, &val); scanErr == nil {
				if val == "" && (strings.HasPrefix(key, "contact.") || strings.HasPrefix(key, "opportunity.")) {
					continue
				}
				values[key] = val
			}
		}
		rows.Close()
	}

	if contactID != "" && uuidRe.MatchString(contactID) {
		h.addContactValues(ctx, values, subAccountID, contactID)
	}

	// Dynamic time.* tokens are computed live in the sub-account's timezone and
	// applied LAST, so they override the (usually empty) stored time.* placeholder
	// rows in account_custom_values. Mirrors the frontend's runtimeTimeValues().
	overlayTimeValues(values, time.Now().In(h.subAccountLocation(ctx, subAccountID)))
	return values
}

// locCache memoizes parsed *time.Location by IANA name so we don't hit tzdata on
// every request.
var locCache sync.Map // string -> *time.Location

// subAccountLocation resolves the sub-account's configured IANA timezone. Falls
// back to UTC when it's unset, unknown, or the lookup fails.
func (h *Handler) subAccountLocation(ctx context.Context, subAccountID string) *time.Location {
	var tz string
	if err := h.pool.QueryRow(ctx, h.subAccountTZQuery, subAccountID).Scan(&tz); err != nil {
		if err != pgx.ErrNoRows {
			h.logger.Warn("db_sites sub-account timezone query failed", zap.String("sub_account_id", subAccountID), zap.Error(err))
		}
		return time.UTC
	}
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.UTC
	}
	if v, ok := locCache.Load(tz); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		h.logger.Warn("db_sites unknown sub-account timezone, using UTC", zap.String("sub_account_id", subAccountID), zap.String("timezone", tz), zap.Error(err))
		loc = time.UTC
	}
	locCache.Store(tz, loc)
	return loc
}

func overlayTimeValues(values map[string]string, now time.Time) {
	for k, v := range runtimeTimeValues(now) {
		values[k] = v
	}
}

// runtimeTimeValues computes the dynamic time.* merge tokens, mirroring the
// frontend's runtimeTimeValues() in formMergeValues.ts (en-GB / dd-mm-yyyy,
// zero-padded, English weekday/month names) so {{time.today}}, {{time.now}}, etc.
// resolve on the published site exactly as they do in previews and emails.
func runtimeTimeValues(now time.Time) map[string]string {
	year := now.Format("2006")
	month := now.Format("01")
	day := now.Format("02")
	hour24 := now.Format("15")
	hour12 := now.Format("03")
	minute := now.Format("04")
	second := now.Format("05")
	amPm := now.Format("PM")
	dayName := now.Format("Monday")
	monthName := now.Format("January")
	return map[string]string{
		"time.second":            second,
		"time.minute":            minute,
		"time.hour_24h_format":   hour24,
		"time.hour_am_pm_format": hour12,
		"time.time_24h_format":   hour24 + ":" + minute,
		"time.time_am_pm_format": hour12 + ":" + minute + " " + amPm,
		"time.am_pm":             amPm,
		"time.day":               day,
		"time.day_in_english":    dayName,
		"time.day_of_week":       dayName,
		"time.month":             month,
		"time.month_in_english":  monthName,
		"time.year":              year,
		"time.year_last_2_digit": now.Format("06"),
		"time.date_us_format":    month + "/" + day + "/" + year,
		"time.date_in_format":    day + "/" + month + "/" + year,
		"time.today":             day + "/" + month + "/" + year,
		"time.now":               day + "/" + month + "/" + year + " " + hour12 + ":" + minute + " " + amPm,
	}
}

func (h *Handler) addContactValues(ctx context.Context, values map[string]string, subAccountID, contactID string) {
	set := func(slug, v string) {
		values["contact."+slug] = v
		if _, ok := values[slug]; !ok {
			values[slug] = v 
		}
	}

	var first, last, email, phone, company, display string
	err := h.pool.QueryRow(ctx, h.contactStandardQuery, contactID, subAccountID).
		Scan(&first, &last, &email, &phone, &company, &display)
	if err == nil {
		set("first_name", first)
		set("last_name", last)
		set("email", email)
		set("phone", phone)
		set("company", company)
		set("display_name", display)
		name := strings.TrimSpace(first + " " + last)
		if name == "" {
			name = display
		}
		set("name", name)
	} else if err != pgx.ErrNoRows {
		h.logger.Warn("db_sites contact standard query failed", zap.String("contact_id", contactID), zap.Error(err))
	}

	crows, err := h.pool.Query(ctx, h.contactCustomQuery, contactID, subAccountID)
	if err != nil {
		h.logger.Warn("db_sites contact custom query failed", zap.String("contact_id", contactID), zap.Error(err))
		return
	}
	defer crows.Close()
	for crows.Next() {
		var slug, val string
		if scanErr := crows.Scan(&slug, &val); scanErr == nil && slug != "" {
			set(slug, val)
		}
	}
}
