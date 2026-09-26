package pulsekit

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// hostRe — хост с необязательным портом: имя/IPv4 или [IPv6].
var hostRe = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

// Host — хост адреса для target зависимости: без схемы, учётных данных, пути и параметров.
// Понимает URL (postgres://user:pass@db:5432/app, https://api.bank.kz/v1), DSN вида
// «host=db port=5432 password=…», адрес gRPC (dns:///orders:9090) и host:port. Несколько
// хостов — первый. Не распознал — пусто (Depend запишет unknown): лучше без адреса, чем
// строка подключения в манифесте.
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
		var port string
		for _, field := range strings.Fields(addr) {
			key, value, _ := strings.Cut(field, "=")
			switch strings.ToLower(key) {
			case "host", "hostaddr", "server", "addr":
				host = strings.Trim(value, `'"`)
			case "port":
				port = strings.Trim(value, `'"`)
			}
		}
		host, _, _ = strings.Cut(host, ",")
		if host != "" && port != "" && !strings.Contains(host, ":") {
			host = net.JoinHostPort(host, port)
		}
	default:
		host = addr
	}
	host, _, _ = strings.Cut(host, ",")
	if !hostRe.MatchString(host) {
		return ""
	}
	return host
}
