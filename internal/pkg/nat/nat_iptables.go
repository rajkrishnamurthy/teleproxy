// +build linux

package nat

import (
	"fmt"
	"log"
	"net"
	"syscall"

	"github.com/datawire/teleproxy/pkg/tpu"
)

type Translator struct {
	commonTranslator
}

func (t *Translator) log(line string, args ...interface{}) {
	log.Printf("NAT: "+line, args...)
}

func (t *Translator) ipt(args ...string) {
	tpu.CmdLogf(append([]string{"iptables", "-t", "nat"}, args...), t.log)
}

func (t *Translator) Enable() {
	// XXX: -D only removes one copy of the rule, need to figure out how to remove all copies just in case
	t.ipt("-D", "OUTPUT", "-j", t.Name)
	// we need to be in the PREROUTING chain in order to get traffic
	// from docker containers, not sure you would *always* want this,
	// but probably makes sense as a default
	t.ipt("-D", "PREROUTING", "-j", t.Name)
	t.ipt("-N", t.Name)
	t.ipt("-F", t.Name)
	t.ipt("-I", "OUTPUT", "1", "-j", t.Name)
	t.ipt("-I", "PREROUTING", "1", "-j", t.Name)
	t.ipt("-A", t.Name, "-j", "RETURN", "--dest", "127.0.0.1/32", "-p", "tcp")
}

func (t *Translator) Disable() {
	// XXX: -D only removes one copy of the rule, need to figure out how to remove all copies just in case
	t.ipt("-D", "OUTPUT", "-j", t.Name)
	t.ipt("-D", "PREROUTING", "-j", t.Name)
	t.ipt("-F", t.Name)
	t.ipt("-X", t.Name)
}

func (t *Translator) ForwardTCP(ip, toPort string) {
	t.forward("tcp", ip, toPort)
}

func (t *Translator) ForwardUDP(ip, toPort string) {
	t.forward("udp", ip, toPort)
}

func (t *Translator) forward(protocol, ip, toPort string) {
	t.clear(protocol, ip)
	t.ipt("-A", t.Name, "-j", "REDIRECT", "--dest", ip+"/32", "-p", protocol, "--to-ports", toPort)
	t.Mappings[Address{protocol, ip}] = toPort
}

func (t *Translator) ClearTCP(ip string) {
	t.clear("tcp", ip)
}

func (t *Translator) ClearUDP(ip string) {
	t.clear("udp", ip)
}

func (t *Translator) clear(protocol, ip string) {
	if previous, exists := t.Mappings[Address{protocol, ip}]; exists {
		t.ipt("-D", t.Name, "-j", "REDIRECT", "--dest", ip+"/32", "-p", protocol, "--to-ports", previous)
		delete(t.Mappings, Address{protocol, ip})
	}
}

const (
	SO_ORIGINAL_DST      = 80
	IP6T_SO_ORIGINAL_DST = 80
)

// get the original destination for the socket when redirect by linux iptables
// refer to https://raw.githubusercontent.com/missdeer/avege/master/src/inbound/redir/redir_iptables.go
//
func (t *Translator) GetOriginalDst(conn *net.TCPConn) (rawaddr []byte, host string, err error) {
	var addr *syscall.IPv6Mreq

	// Get original destination
	// this is the only syscall in the Golang libs that I can find that returns 16 bytes
	// Example result: &{Multiaddr:[2 0 31 144 206 190 36 45 0 0 0 0 0 0 0 0] Interface:0}
	// port starts at the 3rd byte and is 2 bytes long (31 144 = port 8080)
	// IPv6 version, didn't find a way to detect network family
	//addr, err := syscall.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_IPV6, IP6T_SO_ORIGINAL_DST)
	// IPv4 address starts at the 5th byte, 4 bytes long (206 190 36 45)
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, "", err
	}

	err = rawConn.Control(func(fd uintptr) {
		addr, err = syscall.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_IP, SO_ORIGINAL_DST)
	})
	if err != nil {
		return nil, "", err
	}

	// \attention: IPv4 only!!!
	// address type, 1 - IPv4, 4 - IPv6, 3 - hostname, only IPv4 is supported now
	rawaddr = append(rawaddr, byte(1))
	// raw IP address, 4 bytes for IPv4 or 16 bytes for IPv6, only IPv4 is supported now
	rawaddr = append(rawaddr, addr.Multiaddr[4])
	rawaddr = append(rawaddr, addr.Multiaddr[5])
	rawaddr = append(rawaddr, addr.Multiaddr[6])
	rawaddr = append(rawaddr, addr.Multiaddr[7])
	// port
	rawaddr = append(rawaddr, addr.Multiaddr[2])
	rawaddr = append(rawaddr, addr.Multiaddr[3])

	host = fmt.Sprintf("%d.%d.%d.%d:%d",
		addr.Multiaddr[4],
		addr.Multiaddr[5],
		addr.Multiaddr[6],
		addr.Multiaddr[7],
		uint16(addr.Multiaddr[2])<<8+uint16(addr.Multiaddr[3]))

	return rawaddr, host, nil
}
