package pulsekit

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// hostRe — хост с необязательным портом: имя/IPv4 или [IPv6].
var hostRe = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

// Host — хост адреса для target зависимости: только имя (без схемы, учётных данных, порта, пути и
// параметров) — по имени pulse находит сервис в кластере для графа зависимостей. Понимает URL
// (postgres://user:pass@db:5432/app, https://api.bank.kz/v1), DSN вида «host=db port=5432
// password=…», адрес gRPC (dns:///orders:9090) и host:port. Несколько хостов — первый. Не
// распознал — пусто (Depend запишет unknown): лучше без адреса, чем строка подключения в манифесте.
func Host(addr string) string {
	addr = strings.TrimSpace(addr)
	var host string
	switch {
	case addr == "":
		return ""
	case strings.Contains(addr, "://"):
		u, err := url.Parse(addr)
		if err != nil {
			return ""
		}
		host = u.Host // без userinfo
		if host == "" {
			host = strings.TrimPrefix(u.Opaque+u.Path, "/") // dns:///orders:9090
		}
	case strings.Contains(addr, "="):
		for _, field := range strings.Fields(addr) {
			key, value, _ := strings.Cut(field, "=")
			switch strings.ToLower(key) {
			case "host", "hostaddr", "server", "addr":
				host = strings.Trim(value, `'"`)
			}
		}
	default:
		host = addr
	}
	host, _, _ = strings.Cut(host, ",")
	if !hostRe.MatchString(host) {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}
