package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func secretFor(env Env, tool Tool) string {
	if env.Secrets == nil {
		return ""
	}
	return strings.TrimSpace(env.Secrets(tool))
}

func finishMoreStatus(q *Quota, res httpResult, err error) bool {
	if err == nil && res.status == http.StatusPaymentRequired {
		q.Status = QuotaLimitReached
		return true
	}
	return finishStatus(q, res, err)
}

func jsonNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return 0, false
	}
}

func parseFlexibleTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return time.Time{}
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
			return epochTime(f)
		}
	default:
		if f, ok := jsonNumber(v); ok && f > 0 {
			return epochTime(f)
		}
	}
	return time.Time{}
}

func epochTime(v float64) time.Time {
	if v < 1e12 {
		return time.Unix(int64(v), 0)
	}
	return time.UnixMilli(int64(v))
}

// FetchGLM reads GLM Coding Plan's 5-hour and weekly quota windows.
// Zhipu takes the bare API key in Authorization without a Bearer prefix.
func FetchGLM(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolGLM, FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	key, base, ok := readGLMKey(env)
	if !ok {
		key = secretFor(env, ToolGLM)
		base = "https://open.bigmodel.cn"
	}
	if env.GLMBaseURL != "" {
		base = strings.TrimRight(env.GLMBaseURL, "/")
	}
	if key == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, base+"/api/monitor/usage/quota/limit", [][2]string{
		{"Authorization", key},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	var reply struct {
		Success *bool  `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Error   *struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"error"`
		Data *struct {
			Level  string `json:"level"`
			Limits []struct {
				Type          string   `json:"type"`
				Unit          int      `json:"unit"`
				Number        int      `json:"number"`
				Percentage    *float64 `json:"percentage"`
				NextResetTime int64    `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	code := reply.Code
	if code == 0 && reply.Error != nil {
		var s string
		if json.Unmarshal(reply.Error.Code, &s) == nil {
			code, _ = strconv.Atoi(s)
		} else {
			_ = json.Unmarshal(reply.Error.Code, &code)
		}
	}
	switch {
	case code == 401 || (code >= 1000 && code <= 1005):
		q.Status = QuotaExpired
		return q
	case code >= 1302 && code <= 1399:
		q.Status = QuotaRateLimited
		return q
	case code == 1113:
		q.Status = QuotaLimitReached
		return q
	}
	if (reply.Success != nil && !*reply.Success) || reply.Data == nil {
		q.Status = QuotaFailed
		if code != 0 {
			q.Err = fmt.Sprintf("error %d", code)
		} else {
			q.Err = "request failed"
		}
		return q
	}

	q.Plan = prettyPlan(reply.Data.Level)
	for _, l := range reply.Data.Limits {
		if strings.EqualFold(l.Type, "TIME_LIMIT") {
			continue
		}
		var used float64
		if l.Percentage != nil {
			used = max(0, min(100, *l.Percentage))
		}
		var resets time.Time
		if l.NextResetTime > 0 {
			resets = time.UnixMilli(l.NextResetTime)
		}
		switch l.Unit {
		case 3:
			n := max(l.Number, 1)
			if l.Number <= 0 {
				n = 5
			}
			q.Windows = append(q.Windows, Window{
				Name:     fmt.Sprintf("%d hours", n),
				UsedPct:  used,
				Length:   time.Duration(n) * time.Hour,
				ResetsAt: resets,
			})
		case 6:
			q.Windows = append(q.Windows, Window{
				Name:     "7 days",
				UsedPct:  used,
				Length:   7 * 24 * time.Hour,
				ResetsAt: resets,
			})
		}
	}
	q.Status = QuotaOK
	return q
}

// FetchKimi reads Kimi Code's /usages endpoint (supporting both kimi-cli and
// kimi-code response shapes).
func FetchKimi(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolKimi, FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	key, base, ok := readKimiKey(env)
	if !ok {
		key = secretFor(env, ToolKimi)
		base = "https://api.kimi.com/coding/v1"
	}
	if env.KimiBaseURL != "" {
		base = strings.TrimRight(env.KimiBaseURL, "/")
	}
	if key == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	if strings.HasSuffix(base, "/coding") {
		base += "/v1"
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, base+"/usages", [][2]string{
		{"Authorization", "Bearer " + key},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	type row map[string]any
	var reply struct {
		Usage  row            `json:"usage"`
		Usages map[string]row `json:"usages"`
		Limits []struct {
			Window row `json:"window"`
			Detail row `json:"detail"`
		} `json:"limits"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	var five, week, month *Window
	var monthCode *Window
	// 1. kimi-code usages map format: limit_5h, limit_7d, limit_month_total, limit_month_code
	if len(reply.Usages) > 0 {
		for k, r := range reply.Usages {
			ratio, ok := jsonNumber(r["used_ratio"])
			if !ok {
				ratio, ok = jsonNumber(r["usedRatio"])
			}
			if !ok {
				continue
			}
			used := max(0, min(100, ratio*100))
			resets := parseFlexibleTime(r["reset_time"])
			if resets.IsZero() {
				resets = parseFlexibleTime(r["resetTime"])
			}
			switch k {
			case "limit_5h":
				setWindow(&five, &Window{Name: "5 hours", UsedPct: used, Length: 5 * time.Hour, ResetsAt: resets})
			case "limit_7d":
				setWindow(&week, &Window{Name: "7 days", UsedPct: used, Length: 7 * 24 * time.Hour, ResetsAt: resets})
			case "limit_month_total":
				setWindow(&month, &Window{Name: "Month", UsedPct: used, Length: 30 * 24 * time.Hour, ResetsAt: resets})
			case "limit_month_code":
				setWindow(&monthCode, &Window{Name: "Month", UsedPct: used, Length: 30 * 24 * time.Hour, ResetsAt: resets})
			}
		}
	}
	if month == nil && monthCode != nil {
		month = monthCode
	}
	// 2. kimi-cli limits[] + usage format
	parseDetail := func(d row) (used float64, resets time.Time, ok bool) {
		if d == nil {
			return 0, time.Time{}, false
		}
		limit, okLim := jsonNumber(d["limit"])
		if !okLim || limit <= 0 {
			return 0, time.Time{}, false
		}
		if u, okU := jsonNumber(d["used"]); okU {
			used = 100 * max(0, u) / limit
		} else if rem, okR := jsonNumber(d["remaining"]); okR {
			used = 100 * max(0, limit-rem) / limit
		} else {
			return 0, time.Time{}, false
		}
		resets = parseFlexibleTime(d["resetTime"])
		if resets.IsZero() {
			resets = parseFlexibleTime(d["reset_time"])
		}
		return min(100, max(0, used)), resets, true
	}
	for _, item := range reply.Limits {
		used, resets, ok := parseDetail(item.Detail)
		if !ok {
			continue
		}
		dur, _ := jsonNumber(item.Window["duration"])
		unit, _ := item.Window["timeUnit"].(string)
		span := time.Duration(dur) * time.Minute
		switch {
		case strings.Contains(unit, "MONTH"):
			span = time.Duration(dur) * 30 * 24 * time.Hour
		case strings.Contains(unit, "WEEK"):
			span = time.Duration(dur) * 7 * 24 * time.Hour
		case strings.Contains(unit, "DAY"):
			span = time.Duration(dur) * 24 * time.Hour
		case strings.Contains(unit, "HOUR"):
			span = time.Duration(dur) * time.Hour
		}
		switch {
		case span >= 28*24*time.Hour:
			setWindow(&month, &Window{Name: "Month", UsedPct: used, Length: 30 * 24 * time.Hour, ResetsAt: resets})
		case span >= 6*24*time.Hour:
			setWindow(&week, &Window{Name: "7 days", UsedPct: used, Length: 7 * 24 * time.Hour, ResetsAt: resets})
		default:
			if span <= 0 {
				span = 5 * time.Hour
			}
			setWindow(&five, &Window{Name: "5 hours", UsedPct: used, Length: span, ResetsAt: resets})
		}
	}
	if five != nil {
		q.Windows = append(q.Windows, *five)
	}
	if week != nil {
		q.Windows = append(q.Windows, *week)
	} else if used, resets, ok := parseDetail(reply.Usage); ok {
		q.Windows = append(q.Windows, Window{Name: "7 days", UsedPct: used, Length: 7 * 24 * time.Hour, ResetsAt: resets})
	}
	if month != nil {
		q.Windows = append(q.Windows, *month)
	}
	if len(q.Windows) == 0 {
		q.Status = QuotaFailed
		q.Err = "no usage windows"
		return q
	}
	q.Status = QuotaOK
	return q
}

// FetchOpenCodeGo reads OpenCode Go's rolling (5h) and weekly/monthly windows.
func FetchOpenCodeGo(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolOpencode, FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	key, ok := readOpenCodeGoKey(env)
	if !ok {
		key = secretFor(env, ToolOpencode)
	}
	if key == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	url := "https://opencode.ai/zen/go/v1/usage"
	if env.OpenCodeGoURL != "" {
		url = env.OpenCodeGoURL
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, url, [][2]string{
		{"Authorization", "Bearer " + key},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	type wireWin struct {
		Status   string   `json:"status"`
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resetsAt"`
	}
	var reply struct {
		Usage map[string]wireWin `json:"usage"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	for _, spec := range []struct {
		key, name string
		span      time.Duration
	}{
		{"rolling", "5 hours", 5 * time.Hour},
		{"weekly", "7 days", 7 * 24 * time.Hour},
		{"monthly", "Month", 30 * 24 * time.Hour},
	} {
		w, ok := reply.Usage[spec.key]
		if !ok || w.Percent == nil {
			continue
		}
		win := Window{
			Name:    spec.name,
			UsedPct: max(0, min(100, *w.Percent)),
			Length:  spec.span,
		}
		if *w.Percent > 0 && w.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
				win.ResetsAt = t
			}
		}
		q.Windows = append(q.Windows, win)
	}
	if len(q.Windows) == 0 {
		q.Status = QuotaFailed
		q.Err = "no usage windows"
		return q
	}
	q.Status = QuotaOK
	return q
}

// FetchCommandCode reads Command Code's /alpha/billing/credits windows.
func FetchCommandCode(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolCommandCode, FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	key, ok := readCommandCodeKey(env)
	if !ok {
		key = secretFor(env, ToolCommandCode)
	}
	if key == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	base := "https://api.commandcode.ai"
	if env.CommandCodeBaseURL != "" {
		base = strings.TrimRight(env.CommandCodeBaseURL, "/")
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, base+"/alpha/billing/credits", [][2]string{
		{"Authorization", "Bearer " + key},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	type cmdWin struct {
		Used    any `json:"used"`
		Cap     any `json:"cap"`
		ResetAt any `json:"resetAt"`
	}
	var reply struct {
		Credits struct {
			PlanID string `json:"planId"`
		} `json:"credits"`
		WindowLimits *struct {
			FiveHour *cmdWin `json:"fiveHour"`
			Weekly   *cmdWin `json:"weekly"`
		} `json:"windowLimits"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	q.Plan = prettyCommandCodePlan(reply.Credits.PlanID)
	if reply.WindowLimits != nil {
		for _, spec := range []struct {
			name string
			span time.Duration
			w    *cmdWin
		}{
			{"5 hours", 5 * time.Hour, reply.WindowLimits.FiveHour},
			{"7 days", 7 * 24 * time.Hour, reply.WindowLimits.Weekly},
		} {
			if spec.w == nil {
				continue
			}
			used, okU := jsonNumber(spec.w.Used)
			capVal, okC := jsonNumber(spec.w.Cap)
			if !okU || !okC || capVal <= 0 {
				continue
			}
			q.Windows = append(q.Windows, Window{
				Name:     spec.name,
				UsedPct:  min(100, 100*max(0, used)/capVal),
				Length:   spec.span,
				ResetsAt: parseFlexibleTime(spec.w.ResetAt),
			})
		}
	}
	if len(q.Windows) == 0 {
		q.Status = QuotaFailed
		q.Err = "no usage windows"
		return q
	}
	q.Status = QuotaOK
	return q
}

func prettyCommandCodePlan(id string) string {
	s := strings.ReplaceAll(strings.ToLower(id), "_", "-")
	switch {
	case strings.HasPrefix(s, "individual-ultra"):
		return "Ultra"
	case strings.HasPrefix(s, "individual-max"):
		return "Max"
	case strings.HasPrefix(s, "individual-goat"):
		return "GOAT"
	case strings.HasPrefix(s, "individual-pro"):
		return "Pro"
	case strings.HasPrefix(s, "individual-go"):
		return "Go"
	case strings.HasPrefix(s, "teams-pro"):
		return "Teams Pro"
	default:
		return ""
	}
}

// FetchDeepSeek reads DeepSeek's API account balance (/user/balance).
func FetchDeepSeek(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolDeepSeek, Plan: "API", FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	key, ok := readDeepSeekKey(env)
	if !ok {
		key = secretFor(env, ToolDeepSeek)
	}
	if key == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	base := "https://api.deepseek.com"
	if env.DeepSeekBaseURL != "" {
		base = strings.TrimRight(env.DeepSeekBaseURL, "/")
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, base+"/user/balance", [][2]string{
		{"Authorization", "Bearer " + key},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	var reply struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    any    `json:"total_balance"`
			GrantedBalance  any    `json:"granted_balance"`
			ToppedUpBalance any    `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	if len(reply.BalanceInfos) == 0 {
		q.Status = QuotaFailed
		q.Err = "no balance info"
		return q
	}
	chosen := reply.BalanceInfos[0]
	for _, b := range reply.BalanceInfos {
		if strings.EqualFold(b.Currency, "CNY") {
			chosen = b
			break
		}
	}
	total, okTotal := jsonNumber(chosen.TotalBalance)
	if !okTotal {
		q.Status = QuotaFailed
		q.Err = "invalid balance"
		return q
	}
	sign := "¥"
	if strings.EqualFold(chosen.Currency, "USD") {
		sign = "$"
	} else if chosen.Currency != "" && !strings.EqualFold(chosen.Currency, "CNY") {
		sign = chosen.Currency
	}
	bal := &BalanceInfo{
		Currency: sign,
		Amount:   total,
		Text:     fmt.Sprintf("%s %.2f", sign, total),
	}
	if granted, okG := jsonNumber(chosen.GrantedBalance); okG && granted > 0 {
		topped, _ := jsonNumber(chosen.ToppedUpBalance)
		bal.TopUp = trimBalanceDecimal(topped)
		bal.Granted = fmt.Sprintf("%.2f", granted)
	}
	q.Balance = bal
	q.Status = QuotaOK
	return q
}

func trimBalanceDecimal(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// FetchCursor reads Cursor's current billing-period usage via ConnectRPC JSON.
func FetchCursor(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolCursor, FetchedAt: env.now()}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	tok, plan, ok := readCursorCreds(ctx, env)
	if !ok {
		tok = secretFor(env, ToolCursor)
	}
	if tok == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	q.Plan = plan
	base := "https://api2.cursor.sh"
	if env.CursorBaseURL != "" {
		base = strings.TrimRight(env.CursorBaseURL, "/")
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.post(ctx, base+"/aiserver.v1.DashboardService/GetCurrentPeriodUsage", [][2]string{
		{"Authorization", "Bearer " + tok},
		{"Content-Type", "application/json"},
		{"Accept", "application/json"},
	}, []byte("{}"))
	q.FetchedAt = env.now()
	if finishMoreStatus(&q, res, err) {
		return q
	}

	var reply struct {
		Enabled         bool `json:"enabled"`
		BillingCycleEnd any  `json:"billingCycleEnd"`
		PlanUsage       *struct {
			TotalPercentUsed *float64 `json:"totalPercentUsed"`
			AutoPercentUsed  *float64 `json:"autoPercentUsed"`
			APIPercentUsed   *float64 `json:"apiPercentUsed"`
			TotalSpend       *float64 `json:"totalSpend"`
			Limit            *float64 `json:"limit"`
		} `json:"planUsage"`
	}
	if err := decodeJSON(res.body, &reply); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	if reply.PlanUsage == nil {
		q.Status = QuotaFailed
		q.Err = "no plan usage"
		return q
	}
	resets := parseFlexibleTime(reply.BillingCycleEnd)
	var used float64
	haveUsed := false
	switch {
	case reply.PlanUsage.TotalPercentUsed != nil:
		used, haveUsed = *reply.PlanUsage.TotalPercentUsed, true
	case reply.PlanUsage.TotalSpend != nil && reply.PlanUsage.Limit != nil && *reply.PlanUsage.Limit > 0:
		used, haveUsed = 100*(*reply.PlanUsage.TotalSpend)/(*reply.PlanUsage.Limit), true
	case reply.PlanUsage.AutoPercentUsed != nil:
		used, haveUsed = *reply.PlanUsage.AutoPercentUsed, true
	}
	if !haveUsed {
		q.Status = QuotaFailed
		q.Err = "no usage percent"
		return q
	}
	q.Windows = append(q.Windows, Window{
		Name:     "Month",
		UsedPct:  max(0, min(100, used)),
		Length:   30 * 24 * time.Hour,
		ResetsAt: resets,
	})
	if reply.PlanUsage.AutoPercentUsed != nil && reply.PlanUsage.APIPercentUsed != nil {
		q.Windows = append(q.Windows, Window{
			Name:     "API",
			UsedPct:  max(0, min(100, *reply.PlanUsage.APIPercentUsed)),
			Length:   30 * 24 * time.Hour,
			ResetsAt: resets,
		})
	}
	q.Status = QuotaOK
	return q
}
