// Package netfix repara la resolución DNS en Android/Termux.
package netfix

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

type Report struct {
	Android    bool
	ResolvConf bool
	Servers    []string
	Source     string // sistema | libc | resolv.conf | getprop | fallback
	Installed  bool
}

func (r Report) Resolver() string {
	switch {
	case r.Installed:
		return "netfix (resolver Go con servidores explícitos)"
	case r.Android && CGOEnabled:
		return "libc/Bionic vía cgo"
	case CGOEnabled:
		return "libc vía cgo"
	default:
		return "netgo (/etc/resolv.conf)"
	}
}

var fallbackServers = []string{"1.1.1.1", "8.8.8.8"}

func Install() Report {
	rep := Report{Android: isAndroid()}
	rep.ResolvConf, rep.Servers = readResolvConf()

	switch {
	case !rep.Android:
		rep.Source = "sistema"
		return rep
	case CGOEnabled:
		rep.Source = "libc"
		rep.Servers = nil
		return rep
	case rep.ResolvConf:
		rep.Source = "resolv.conf"
		return rep
	}

	servers := getprops()
	rep.Source = "getprop"
	if len(servers) == 0 {
		servers = fallbackServers
		rep.Source = "fallback"
	}
	rep.Servers = servers
	install(servers)
	rep.Installed = true
	return rep
}

func install(servers []string) {
	d := &net.Dialer{Timeout: 3 * time.Second}
	var rr uint32
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			n := int(atomic.AddUint32(&rr, 1))
			var last error
			for i := range servers {
				addr := net.JoinHostPort(servers[(n+i)%len(servers)], "53")
				c, err := d.DialContext(ctx, network, addr)
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
}

func isAndroid() bool {
	if runtime.GOOS == "android" {
		return true
	}
	_, err := os.Stat("/system/bin/getprop")
	return err == nil
}

func readResolvConf() (bool, []string) {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return false, nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 2 && net.ParseIP(f[1]) != nil {
			out = append(out, f[1])
		}
	}
	return len(out) > 0, out
}

func getprops() []string {
	bin := "/system/bin/getprop"
	if _, err := os.Stat(bin); err != nil {
		p, err := exec.LookPath("getprop")
		if err != nil {
			return nil
		}
		bin = p
	}
	var out []string
	seen := map[string]bool{}
	for _, k := range []string{"net.dns1", "net.dns2", "net.dns3", "net.dns4"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		b, err := exec.CommandContext(ctx, bin, k).Output()
		cancel()
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(b))
		if v != "" && net.ParseIP(v) != nil && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
