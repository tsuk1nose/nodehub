package edge

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Discovery reads local SSH/socket configuration, never scans arbitrary ports.
func sshEndpoint(ctx context.Context, configured int) (string, error) {
	if configured != 0 {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(configured)), nil
	}
	output := func(name string, args ...string) (string, error) {
		b, err := exec.CommandContext(ctx, name, args...).Output()
		return string(b), err
	}
	return discoverSSHEndpoint(output)
}

func discoverSSHEndpoint(output func(string, ...string) (string, error)) (string, error) {
	socket, _ := output("systemctl", "show", "ssh.socket", "--property=ActiveState", "--property=Listen")
	var addresses []string
	if strings.Contains(socket, "ActiveState=active") {
		// Listen supports multiple stream sockets and IPv4/IPv6 endpoints.
		fields := strings.Fields(strings.ReplaceAll(socket, "Listen=", ""))
		for i := 1; i < len(fields); i++ {
			if fields[i] == "(Stream)" {
				addresses = append(addresses, fields[i-1])
			}
		}
	} else {
		config, err := output("/usr/sbin/sshd", "-G")
		if err != nil {
			return "", errors.New("cannot read effective sshd configuration")
		}
		for _, line := range strings.Split(config, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "listenaddress" {
				addresses = append(addresses, fields[1])
			}
		}
	}
	listeners, err := output("ss", "-H", "-ltn")
	if err != nil {
		return "", errors.New("cannot inspect local SSH listeners")
	}
	return selectSSHEndpoint(addresses, listeners)
}

func selectSSHEndpoint(addresses []string, listeners string) (string, error) {
	listening := map[string]bool{}
	for _, line := range strings.Split(listeners, "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 {
			listening[f[3]] = true
		}
	}
	candidates := map[string]bool{}
	for _, address := range addresses {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}
		target := ip
		if ip.IsUnspecified() {
			if ip.To4() != nil {
				target = net.ParseIP("127.0.0.1")
			} else {
				target = net.ParseIP("::1")
			}
		}
		if !target.IsLoopback() {
			continue
		}
		// ss may render wildcard listeners as *:port on Linux.
		active := listening[address] || (ip.IsUnspecified() && listening["*:"+port])
		if active {
			candidates[net.JoinHostPort(target.String(), port)] = true
		}
	}
	result := make([]string, 0, len(candidates))
	for address := range candidates {
		result = append(result, address)
	}
	sort.Slice(result, func(i, j int) bool {
		hi, pi, _ := net.SplitHostPort(result[i])
		hj, pj, _ := net.SplitHostPort(result[j])
		if hi != hj {
			if hi == "127.0.0.1" || hj == "127.0.0.1" {
				return hi == "127.0.0.1"
			}
			return hi < hj
		}
		ni, _ := strconv.Atoi(pi)
		nj, _ := strconv.Atoi(pj)
		return ni < nj
	})
	if len(result) == 0 {
		return "", errors.New("sshd has no matching loopback listener")
	}
	return result[0], nil
}
