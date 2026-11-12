package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// WithHostGuard 只接受「本机主机名」或「本次显式绑定的地址」作为 Host。
//
// 目的是挡住 DNS rebinding：把任意域名解析到 127.0.0.1 之后，浏览器仍然会带着
// 那个域名发请求，Origin 校验也看不出来，只有 Host 能识别。
func WithHostGuard(next http.Handler, bindAddr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, bindAddr) {
			writeError(w, http.StatusForbidden,
				"拒绝访问：本服务只接受本机地址（localhost / 127.0.0.1）。需要局域网访问时请用 -addr 显式指定网卡 IP。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoopbackOnly 报告 addr 是否只绑定回环地址（":8080" 这种写法等于所有网卡）。
func LoopbackOnly(addr string) bool {
	return isLoopbackName(strings.Trim(bindHostName(addr), "[]"))
}

// OriginAllowed 判断 Origin 头是否来自本机页面；端口不限，方便 npm run dev 直连。
func OriginAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return isLoopbackName(u.Hostname())
}

func hostAllowed(hostHeader, bindAddr string) bool {
	name := hostName(hostHeader)
	if isLoopbackName(name) {
		return true
	}
	bind := strings.Trim(bindHostName(bindAddr), "[]")
	if bind == "" || bind == "0.0.0.0" || bind == "::" {
		// 绑定到所有网卡时，没有哪个「外部地址」可以拿来比对，只认本机。
		return false
	}
	return strings.EqualFold(name, bind)
}

func isLoopbackName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Trim(name, "[].")
	if name == "localhost" {
		return true
	}
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func hostName(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return strings.Trim(hostPort, "[]")
}

func bindHostName(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return strings.Trim(addr, "[]")
}
