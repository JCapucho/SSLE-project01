package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
)

type NodeSchema struct {
	Name       string `json:"name"`
	Datacenter string `json:"dc"`
	Location   string `json:"location"`
}

type Hostname struct {
	fqdn string
	addr netip.Addr
}

func ParseHostname(v string) (Hostname, error) {
	if v == "" {
		return Hostname{}, errors.New("Hostname must not be empty")
	}

	ip, err := netip.ParseAddr(v)
	if err == nil {
		return HostnameFromAddr(ip), nil
	}

	return HostnameFromFqdn(v), nil
}

func HostnameFromFqdn(fqdn string) Hostname {
	return Hostname{fqdn: fqdn, addr: netip.Addr{}}
}

func HostnameFromAddr(addr netip.Addr) Hostname {
	return Hostname{addr: addr, fqdn: ""}
}

func (hostname Hostname) IsValid() bool {
	return hostname != Hostname{}
}

func (hostname Hostname) IsAddress() bool {
	return hostname.addr.IsValid()
}

func (hostname Hostname) Address() netip.Addr {
	return hostname.addr
}

func (hostname Hostname) Fqdn() string {
	return hostname.fqdn
}

func (hostname Hostname) String() string {
	if hostname.IsAddress() {
		return hostname.addr.String()
	} else {
		return hostname.fqdn
	}
}

func (hostname *Hostname) MarshalJSON() ([]byte, error) {
	return json.Marshal(hostname.String())
}

func (hostname *Hostname) UnmarshalJSON(data []byte) error {
	var temp string
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	h, err := ParseHostname(temp)
	if err != nil {
		return err
	}
	*hostname = h

	return nil
}

func (hostname Hostname) HostWithPort(port uint16) string {
	if hostname.addr.IsValid() {
		if hostname.addr.Is4() {
			return fmt.Sprintf("%v:%v", hostname.addr, port)
		} else {
			return fmt.Sprintf("[%v]:%v", hostname.addr, port)
		}
	} else {
		return fmt.Sprintf("%v:%v", hostname.fqdn, port)
	}
}
