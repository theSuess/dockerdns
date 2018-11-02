package dockerdns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/mholt/caddy"
	"github.com/miekg/dns"
)

type Handler struct {
	client *client.Client
	names  map[string]string
}

func init() {
	caddy.RegisterPlugin("dockerdns", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}
func setup(c *caddy.Controller) error {
	c.Next() // 'dockerdns'
	if c.NextArg() {
		return plugin.Error("dockerdns", c.ArgErr())
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		cli, err := client.NewEnvClient()
		if err != nil {
			panic(err)
		}
		containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{})
		if err != nil {
			panic(err)
		}

		nameMap := make(map[string]string)
		for _, container := range containers {
			for _, n := range container.Names {
				nameMap[n[1:]] = container.ID
			}
		}
		return &Handler{
			client: cli,
			names:  nameMap,
		}
	})
	return nil
}

func (h *Handler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true

	var ip net.IP
	n := strings.Split(state.QName(), ".")[0]

	if id, ok := h.names[n]; ok {
		c, err := h.client.ContainerInspect(context.Background(), id)
		if err != nil {
			return 0, err
		}
		ip = net.ParseIP(c.NetworkSettings.IPAddress)
	} else {
		log.Printf("Container %s not found", n)
		log.Println("Available containers:")
		for k, v := range h.names {
			log.Printf("%s:%s", k, v)
		}
		a.SetRcode(r, dns.RcodeNameError)
		a.Ns = []dns.RR{soa(state.QName())}
		w.WriteMsg(a)
		return 0, nil
	}

	rec := new(dns.A)
	rec.A = ip
	rec.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass()}

	a.Extra = []dns.RR{rec}

	state.SizeAndDo(a)
	w.WriteMsg(a)

	return 0, nil
}
func (h Handler) Name() string { return "dockerdns" }

func soa(name string) dns.RR {
	s := fmt.Sprintf("%s 60 IN SOA ns1.%s postmaster.%s 1524370381 14400 3600 604800 60", name, name, name)
	soa, _ := dns.NewRR(s)
	return soa
}
