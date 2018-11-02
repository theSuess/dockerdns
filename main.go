package dockerdns

import (
	"context"
	"fmt"
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
		h := &Handler{
			client: cli,
		}
		h.RefreshNames()
		go func() {
			c, _ := cli.Events(context.Background(), types.EventsOptions{})
			for {
				e := <-c
				if e.Type == "container" && (e.Action == "start" || e.Action == "die" || e.Action == "stop") {
					h.RefreshNames()
				}
			}
		}()

		return h
	})
	return nil
}

func (h *Handler) RefreshNames() {
	containers, err := h.client.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}
	nameMap := make(map[string]string)
	for _, container := range containers {
		for _, n := range container.Names {
			nameMap[n[1:]] = container.ID
		}
	}
	h.names = nameMap
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
		if err == nil {
			ip = net.ParseIP(c.NetworkSettings.IPAddress)
		}
	}
	if ip == nil {
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
