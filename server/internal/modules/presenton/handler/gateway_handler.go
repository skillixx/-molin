package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/modules/presenton/service"
)

// cookieName D2 反代会话 cookie 名（HttpOnly，浏览器不可读其值，仅作会话标识）。
const cookieName = "molin_psid"

// 注入给内网 presenton 的可信头（与 fork 端 MolinIdentityMiddleware 约定一致）。
const (
	hdrUser    = "X-Molin-User-Id"
	hdrKey     = "X-Molin-LLM-Key"
	hdrBaseURL = "X-Molin-LLM-Base-Url"
	hdrModel   = "X-Molin-LLM-Model"
	hdrSecret  = "X-Molin-Auth-Secret"
)

// ticketConsumer 一次性取回并删除票据（*service.RedisTicketStore 实现）。
type ticketConsumer interface {
	Consume(ctx context.Context, ticket string) (*service.TicketPayload, error)
}

// sessionStore 反代会话读写（*service.RedisSessionStore 实现）。
type sessionStore interface {
	Save(ctx context.Context, sid string, p service.TicketPayload, ttl time.Duration) error
	Load(ctx context.Context, sid string) (*service.TicketPayload, error)
}

// GatewayHandler 实现 D2 反代：
//   - Launch：校验一次性票据 → 换出会话 cookie → 重定向到反代根；
//   - Proxy：凭 cookie 取会话 → 注入可信 X-Molin-* 头 → 反代到内网 presenton。
type GatewayHandler struct {
	tickets      ticketConsumer
	sessions     sessionStore
	proxy        *httputil.ReverseProxy
	pathPrefix   string // 公开挂载前缀，如 /app/presenton
	llmBaseURL   string // 可选：注入 X-Molin-LLM-Base-Url（空则 presenton 用其 CUSTOM_LLM_URL）
	trustSecret  string // 共享密钥，注入 X-Molin-Auth-Secret（与 fork MOLIN_TRUST_SECRET 对应）
	sessionTTL   time.Duration
	cookieSecure bool
}

// NewGatewayHandler 构造反代 handler。target 为内网 presenton 基址。
func NewGatewayHandler(
	tickets ticketConsumer,
	sessions sessionStore,
	target *url.URL,
	pathPrefix string,
	llmBaseURL string,
	trustSecret string,
	sessionTTL time.Duration,
	cookieSecure bool,
) *GatewayHandler {
	prefix := strings.TrimRight(pathPrefix, "/")
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target) // 设置 scheme/host，并把入站 path join 到 target
			// 去掉公开前缀，转为 presenton 内部路径（/app/presenton/foo → /foo）。
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
			pr.Out.URL.RawPath = ""
			pr.SetXForwarded()
		},
	}
	return &GatewayHandler{
		tickets:      tickets,
		sessions:     sessions,
		proxy:        proxy,
		pathPrefix:   prefix,
		llmBaseURL:   llmBaseURL,
		trustSecret:  trustSecret,
		sessionTTL:   sessionTTL,
		cookieSecure: cookieSecure,
	}
}

// Launch GET {prefix}/launch?ticket=...
// 校验一次性票据 → 建会话 → 下发 cookie → 302 重定向到反代根。
func (h *GatewayHandler) Launch(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		http.Error(w, "缺少 ticket", http.StatusBadRequest)
		return
	}
	// 一次性消费票据（防重放）。
	payload, err := h.tickets.Consume(r.Context(), ticket)
	if err != nil {
		if errors.Is(err, service.ErrTicketNotFound) {
			http.Error(w, "票据无效或已过期，请重新打开", http.StatusUnauthorized)
			return
		}
		http.Error(w, "打开失败", http.StatusInternalServerError)
		return
	}
	// 建会话。
	sid, err := randomID()
	if err != nil {
		http.Error(w, "打开失败", http.StatusInternalServerError)
		return
	}
	if err := h.sessions.Save(r.Context(), sid, *payload, h.sessionTTL); err != nil {
		http.Error(w, "打开失败", http.StatusInternalServerError)
		return
	}
	// 下发会话 cookie（HttpOnly，限定在反代前缀路径）。
	// 跨源 iframe 内嵌需 SameSite=None+Secure；https 下启用，本地 http 退回 Lax。
	sameSite := http.SameSiteLaxMode
	if h.cookieSecure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sid,
		Path:     h.pathPrefix,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: sameSite,
		MaxAge:   int(h.sessionTTL.Seconds()),
	})
	http.Redirect(w, r, h.pathPrefix+"/", http.StatusFound)
}

// Proxy 反代其余所有 {prefix}/* 请求：凭 cookie 取会话 → 注入可信头 → 转发内网 presenton。
func (h *GatewayHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		http.Error(w, "会话缺失，请重新打开应用", http.StatusUnauthorized)
		return
	}
	payload, err := h.sessions.Load(r.Context(), c.Value)
	if err != nil {
		http.Error(w, "会话已过期，请重新打开应用", http.StatusUnauthorized)
		return
	}

	// 反伪造：先清掉客户端可能伪造的可信头，再注入服务端可信值。
	r.Header.Del(hdrUser)
	r.Header.Del(hdrKey)
	r.Header.Del(hdrBaseURL)
	r.Header.Del(hdrModel)
	r.Header.Del(hdrSecret)

	r.Header.Set(hdrUser, strconv.FormatUint(payload.UserID, 10))
	r.Header.Set(hdrKey, payload.APIKey)
	if h.llmBaseURL != "" {
		r.Header.Set(hdrBaseURL, h.llmBaseURL)
	}
	// 用户所选模型（F-D）：会话有则注入，presenton 据此用对应模型；无则 presenton 回退 CUSTOM_MODEL。
	if payload.Model != "" {
		r.Header.Set(hdrModel, payload.Model)
	}
	if h.trustSecret != "" {
		r.Header.Set(hdrSecret, h.trustSecret)
	}

	h.proxy.ServeHTTP(w, r)
}

// randomID 生成 32 字节随机会话 id（hex）。
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
