package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/go-playground/validator/v10"
)

var Validate *validator.Validate = validator.New(validator.WithRequiredStructEnabled())

type PathSegment string

func (path PathSegment) String() string {
	return string(path)
}

func (path PathSegment) Valid() error {
	if path == "" {
		return errors.New("Must not be empty")
	}

	if strings.Contains(path.String(), "/") {
		return errors.New("Must not contain any forward slashes")
	}

	return nil
}

func (path *PathSegment) UnmarshalJSON(data []byte) error {
	var temp string
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	*path = PathSegment(temp)

	if err := path.Valid(); err != nil {
		return err
	}

	return nil
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

	err = Validate.VarWithKey("hostname", v, "fqdn")
	return HostnameFromFqdn(v), err
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

type PortSpec struct {
	Name     PathSegment `json:"name" validate:"required"`
	Port     uint16      `json:"port" validate:"required"`
	Protocol string      `json:"proto"`
}

type ServiceSpec struct {
	ServiceName PathSegment `json:"name" validate:"required"`
	Instance    PathSegment `json:"instance" validate:"required"`

	Location   PathSegment `json:"location" validate:"required"`
	DataCenter PathSegment `json:"datacenter" validate:"required"`

	Addresses   []Hostname `json:"addrs"`
	Ports       []PortSpec `json:"ports"`
	MetricsPort uint16     `json:"metricsPort,omitempty"`
}

type PrometheusServiceLabels struct {
	Location   string `json:"__meta_location"`
	DataCenter string `json:"__meta_datacenter"`
	Service    string `json:"__meta_prometheus_job"`
	Instance   string `json:"__meta_instance"`
}

type PrometheusService struct {
	Targets []string                `json:"targets"`
	Labels  PrometheusServiceLabels `json:"labels"`
}

func PrometheusServiceFromServiceSpec(spec *ServiceSpec) *PrometheusService {
	if spec.MetricsPort == 0 {
		return nil
	}

	Targets := make([]string, len(spec.Addresses))
	for i, addr := range spec.Addresses {
		Targets[i] = addr.HostWithPort(spec.MetricsPort)
	}

	return &PrometheusService{
		Targets: Targets,
		Labels: PrometheusServiceLabels{
			Location:   spec.Location.String(),
			DataCenter: spec.DataCenter.String(),
			Service:    spec.DataCenter.String(),
			Instance:   spec.Instance.String(),
		},
	}
}

type RegisterServiceRequest struct {
	Instance PathSegment `json:"instance" validate:"required"`

	Addresses   []Hostname `json:"addrs"`
	Ports       []PortSpec `json:"ports"`
	MetricsPort uint16     `json:"metricsPort"`
}

func (spec *RegisterServiceRequest) UnmarshalJSON(data []byte) error {
	type RawRequest RegisterServiceRequest
	raw := RawRequest{
		Addresses: []Hostname{},
		Ports:     []PortSpec{},
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*spec = RegisterServiceRequest(raw)
	return nil
}
